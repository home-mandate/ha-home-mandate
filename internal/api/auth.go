// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// userIDPattern is the form of Home Assistant user IDs (and of the approver strings a
// mandate may hold); anything else in X-Remote-User-Id is no user.
var userIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// fromSupervisor tells whether the request comes from the Supervisor. Only the peer
// address counts: X-Forwarded-For and Forwarded can be set by anyone.
func fromSupervisor(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().String() == SupervisorAddr
}

// userOf returns the Home Assistant user the Supervisor set for this request.
func userOf(r *http.Request) (string, bool) {
	values := r.Header.Values("X-Remote-User-Id")
	if len(values) != 1 || !userIDPattern.MatchString(values[0]) {
		return "", false
	}
	return values[0], true
}

// usersTTL bounds how long administrator rights and names are taken from memory
// (decision U2): withdrawn rights take effect within it.
const (
	usersTTL     = 30 * time.Second
	usersTimeout = 5 * time.Second
	// usersRetry: after a failed answer Home Assistant is asked again only after this
	// time; meanwhile everyone is refused (still fail closed), and requests do not queue
	// up behind one slow call each.
	usersRetry = 3 * time.Second
)

// errUsersUnavailable means Home Assistant could not be asked who is an administrator.
var errUsersUnavailable = errors.New("api: users unavailable")

// users caches config/auth/list. A failed refresh is never answered from an older list:
// without a current answer, nobody counts as an administrator (fail closed).
type users struct {
	ha  HA
	now func() time.Time

	mu       sync.Mutex
	at       time.Time
	byID     map[string]ha.AuthUser
	failedAt time.Time
	failure  error
}

func newUsers(h HA, now func() time.Time) *users {
	return &users{ha: h, now: now}
}

// all returns the users, refreshing them when older than usersTTL.
func (u *users) all(ctx context.Context) (map[string]ha.AuthUser, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byID != nil && u.now().Sub(u.at) < usersTTL && !u.now().Before(u.at) {
		return u.byID, nil
	}
	if u.ha == nil {
		return nil, errUsersUnavailable
	}
	if u.failure != nil && u.now().Sub(u.failedAt) < usersRetry && !u.now().Before(u.failedAt) {
		return nil, u.failure
	}
	ctx, cancel := context.WithTimeout(ctx, usersTimeout)
	defer cancel()
	list, err := u.ha.ListUsers(ctx)
	if err != nil {
		u.byID, u.failure, u.failedAt = nil, errors.Join(errUsersUnavailable, err), u.now()
		return nil, u.failure
	}
	u.failure = nil
	byID := make(map[string]ha.AuthUser, len(list))
	for _, user := range list {
		byID[user.ID] = user
	}
	u.byID, u.at = byID, u.now()
	return byID, nil
}

// IsAdmin tells whether user is a Home Assistant administrator now; an error means
// it could not be checked, which callers treat as no.
func (u *users) IsAdmin(ctx context.Context, user string) (bool, error) {
	all, err := u.all(ctx)
	if err != nil {
		return false, err
	}
	found, ok := all[user]
	return ok && found.IsAdmin(), nil
}

// name returns the name of a Home Assistant user, nil if unknown or not available.
func (u *users) name(ctx context.Context, id string) *string {
	if id == "" {
		return nil
	}
	all, err := u.all(ctx)
	if err != nil {
		return nil
	}
	if found, ok := all[id]; ok && found.Name != "" {
		return &found.Name
	}
	return nil
}

// IsAdmin is the administrator check of the API, for internal/approval as well.
func (s *Server) IsAdmin(ctx context.Context, user string) (bool, error) {
	return s.users.IsAdmin(ctx, user)
}

// CSRF tokens (decision U4): HMAC-SHA256 under a key that lives only in this process,
// over the user and a 12-hour period. A token is valid in its period and the next, so
// that it lasts 12 to 24 hours; a restart invalidates all (the UI fetches a new session
// and repeats the write once).
const csrfPeriod = 12 * time.Hour

func (s *Server) csrfToken(user string, at time.Time) string {
	return base64.RawURLEncoding.EncodeToString(s.csrfMAC(user, at.Unix()/int64(csrfPeriod/time.Second)))
}

func (s *Server) csrfMAC(user string, period int64) []byte {
	mac := hmac.New(sha256.New, s.csrfKey)
	var p [8]byte
	binary.BigEndian.PutUint64(p[:], uint64(period))
	mac.Write([]byte("hm-csrf\x00"))
	mac.Write(p[:])
	mac.Write([]byte(user))
	return mac.Sum(nil)
}

// validCSRF compares in constant time with the token of the current and the previous
// period.
func (s *Server) validCSRF(user, token string) bool {
	got, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	period := s.now().Unix() / int64(csrfPeriod/time.Second)
	ok := subtle.ConstantTimeCompare(got, s.csrfMAC(user, period))
	ok |= subtle.ConstantTimeCompare(got, s.csrfMAC(user, period-1))
	return ok == 1
}

// limits are fixed-window counters per key, e.g. per user and kind of request. They
// bound what one session can cause; the windows are short, so the memory is too.
type limits struct {
	now func() time.Time

	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	start time.Time
	count int
}

func newLimits(now func() time.Time) *limits {
	return &limits{now: now, windows: map[string]*window{}}
}

// allow counts one request under key and reports whether it is within n per period;
// otherwise it returns the seconds until the window ends.
func (l *limits) allow(key string, n int, period time.Duration) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.windows) > 10000 { // forget old windows rather than grow without bound
		for k, w := range l.windows {
			if now.Sub(w.start) > time.Hour || now.Before(w.start) {
				delete(l.windows, k)
			}
		}
	}
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= period || now.Before(w.start) {
		w = &window{start: now}
		l.windows[key] = w
	}
	if w.count >= n {
		return false, int((period - now.Sub(w.start) + time.Second - 1) / time.Second)
	}
	w.count++
	return true, 0
}
