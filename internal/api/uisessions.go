// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// Sessions of the UI in direct mode (ARCHITECTURE section 12): in memory only, so a
// restart signs everyone out; tokens are kept as hashes.
const (
	uiIdle    = 30 * time.Minute
	uiMaxAge  = 12 * time.Hour
	uiPerUser = 5   // a sixth sign-in ends the oldest session of the user
	uiTotal   = 100 // beyond it, nobody can sign in until sessions end

	// Sign-ins in progress. Beyond either bound the oldest one gives way (of the address
	// first): starting sign-ins needs no session, so a full table must never lock the
	// administrators out; a flood only makes a slow sign-in start over.
	signInTTL     = 10 * time.Minute
	signInPerAddr = 10
	signInTotal   = 100
)

// errUISessionsFull means no further session or sign-in can be started now.
var errUISessionsFull = errors.New("api: too many sessions or sign-ins")

type uiSession struct {
	user          string
	created, seen time.Time
	seq           uint64 // order of creation: times can be equal
}

type signIn struct {
	addr    string
	created time.Time
	seq     uint64 // order of starting: times can be equal
}

type uiSessions struct {
	now func() time.Time

	mu       sync.Mutex
	sessions map[[sha256.Size]byte]*uiSession
	signIns  map[[sha256.Size]byte]*signIn
	started  uint64
}

func newUISessions(now func() time.Time) *uiSessions {
	return &uiSessions{now: now, sessions: map[[sha256.Size]byte]*uiSession{}, signIns: map[[sha256.Size]byte]*signIn{}}
}

// newToken is 256 random bits in base64url.
func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go ≥ 1.24)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

// create starts a session of user and returns its token.
func (u *uiSessions) create(user string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now()
	u.sweep(now)
	var oldest [sha256.Size]byte
	count := 0
	for k, s := range u.sessions {
		if s.user != user {
			continue
		}
		if count == 0 || s.seq < u.sessions[oldest].seq {
			oldest = k
		}
		count++
	}
	if count >= uiPerUser {
		// Its event streams end at their next check (adminRecheck), as for an idle session.
		delete(u.sessions, oldest)
	}
	if len(u.sessions) >= uiTotal {
		return "", errUISessionsFull
	}
	token := newToken()
	u.started++
	u.sessions[tokenHash(token)] = &uiSession{user: user, created: now, seen: now, seq: u.started}
	return token, nil
}

// lookup returns the user of a live session and counts the request as use.
func (u *uiSessions) lookup(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	key := tokenHash(token)
	s, ok := u.sessions[key]
	if !ok {
		return "", false
	}
	now := u.now()
	if !live(s, now) {
		delete(u.sessions, key)
		return "", false
	}
	s.seen = now
	return s.user, true
}

// valid tells whether a session is live without counting it as use: an open event
// stream keeps the page, not the person, busy.
func (u *uiSessions) valid(token string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	s, ok := u.sessions[tokenHash(token)]
	return ok && live(s, u.now())
}

func (u *uiSessions) end(token string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.sessions, tokenHash(token))
}

func live(s *uiSession, now time.Time) bool {
	return !now.Before(s.created) && now.Sub(s.created) < uiMaxAge && now.Sub(s.seen) < uiIdle
}

// startSignIn returns the state of a new sign-in from addr; it goes into a cookie and
// into the request to Home Assistant, and must come back in both.
func (u *uiSessions) startSignIn(addr string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now()
	u.sweep(now)
	if key, n := u.oldestSignIn(func(s *signIn) bool { return s.addr == addr }); n >= signInPerAddr {
		delete(u.signIns, key)
	}
	if key, n := u.oldestSignIn(func(*signIn) bool { return true }); n >= signInTotal {
		delete(u.signIns, key)
	}
	state := newToken()
	u.started++
	u.signIns[tokenHash(state)] = &signIn{addr: addr, created: now, seq: u.started}
	return state
}

// oldestSignIn returns the oldest sign-in that match selects and how many it selects;
// called with mu held.
func (u *uiSessions) oldestSignIn(match func(*signIn) bool) ([sha256.Size]byte, int) {
	var oldest [sha256.Size]byte
	n := 0
	for k, s := range u.signIns {
		if !match(s) {
			continue
		}
		if n == 0 || s.seq < u.signIns[oldest].seq {
			oldest = k
		}
		n++
	}
	return oldest, n
}

// finishSignIn uses up the sign-in if the state from the cookie and the one Home
// Assistant sent back are the same and it has not expired.
func (u *uiSessions) finishSignIn(cookie, state string) bool {
	if cookie == "" || subtle.ConstantTimeCompare([]byte(cookie), []byte(state)) != 1 {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	key := tokenHash(state)
	s, ok := u.signIns[key]
	if !ok {
		return false
	}
	delete(u.signIns, key)
	now := u.now()
	return !now.Before(s.created) && now.Sub(s.created) < signInTTL
}

// sweep forgets what has expired; called with mu held.
func (u *uiSessions) sweep(now time.Time) {
	for k, s := range u.sessions {
		if !live(s, now) {
			delete(u.sessions, k)
		}
	}
	for k, s := range u.signIns {
		if now.Before(s.created) || now.Sub(s.created) >= signInTTL {
			delete(u.signIns, k)
		}
	}
}
