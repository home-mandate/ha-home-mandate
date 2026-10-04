// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

const (
	sessionTTL           = 10 * time.Minute
	maxSessions          = 200
	maxSessionsPerSender = 10
	secretBytes          = 32
)

// errTooManySessions means the in-memory session store is full.
var errTooManySessions = errors.New("oauth: too many sign-ins in progress")

// Purposes of a browser session.
const (
	purposeAuthorize = "authorize"
	purposePair      = "pair"
)

// session is one human's sign-in and admission in the browser. It lives in memory only,
// for at most sessionTTL, and admits at most one agent.
type session struct {
	expires time.Time
	sender  string // address the sign-in started from
	purpose string
	haState string   // pending Home Assistant sign-in; cleared when used
	user    *ha.User // set after a successful sign-in by an administrator
	csrf    string
	authz   *authzRequest // purposeAuthorize: the agent's authorization request
	device  string        // purposePair: device code hash of the entered pairing code
	// failures counts wrong pairing codes in this session.
	failures int
}

// sessions holds the sessions by the SHA-256 of their cookie value, so that a memory
// dump does not reveal usable cookies.
type sessions struct {
	mu   sync.Mutex
	byID map[[32]byte]*session
	now  func() time.Time
}

func newSessions(now func() time.Time) *sessions {
	return &sessions{byID: map[[32]byte]*session{}, now: now}
}

// create starts a session for sender and returns its cookie value. One sender cannot
// fill the store: it may hold maxSessionsPerSender sessions.
func (s *sessions) create(sender, purpose string, authz *authzRequest) (string, *session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpired()
	fromSender := 0
	for _, sess := range s.byID {
		if sess.sender == sender {
			fromSender++
		}
	}
	if len(s.byID) >= maxSessions || fromSender >= maxSessionsPerSender {
		return "", nil, errTooManySessions
	}
	id := newSecret()
	sess := &session{expires: s.now().Add(sessionTTL), sender: sender, purpose: purpose, haState: newSecret(), csrf: newSecret(), authz: authz}
	s.byID[sha256.Sum256([]byte(id))] = sess
	return id, sess, nil
}

// with runs fn on the session with cookie value id while holding the lock. It reports
// false for unknown or expired sessions.
func (s *sessions) with(id string, fn func(*session)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(id))
	sess, ok := s.byID[key]
	if !ok {
		return false
	}
	if !s.now().Before(sess.expires) {
		delete(s.byID, key)
		return false
	}
	fn(sess)
	return true
}

// rotate gives the session a new cookie value and CSRF token after sign-in, so that a
// cookie planted before the sign-in is useless afterwards (session fixation).
func (s *sessions) rotate(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(id))
	sess, ok := s.byID[key]
	if !ok || !s.now().Before(sess.expires) {
		delete(s.byID, key)
		return "", false
	}
	delete(s.byID, key)
	newID := newSecret()
	sess.csrf = newSecret()
	s.byID[sha256.Sum256([]byte(newID))] = sess
	return newID, true
}

// take removes the session if csrf is its CSRF token. Only the first of several
// concurrent decisions in one session gets true: a session admits at most one agent.
func (s *sessions) take(id, csrf string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(id))
	sess, ok := s.byID[key]
	if !ok || !s.now().Before(sess.expires) || !equalSecret(sess.csrf, csrf) {
		return false
	}
	delete(s.byID, key)
	return true
}

func (s *sessions) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, sha256.Sum256([]byte(id)))
}

func (s *sessions) dropExpired() {
	now := s.now()
	for key, sess := range s.byID {
		if !now.Before(sess.expires) {
			delete(s.byID, key)
		}
	}
}

// newSecret returns 256 bits from crypto/rand, base64url without padding.
func newSecret() string {
	var b [secretBytes]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// equalSecret compares secrets in constant time; empty never matches.
func equalSecret(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
