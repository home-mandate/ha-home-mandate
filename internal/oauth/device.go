// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"crypto/rand"
	"net/http"
	"strings"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/i18n"
)

// Device Authorization Grant (RFC 8628): the pairing code.
const (
	deviceTTL        = 10 * time.Minute
	deviceInterval   = 5 * time.Second
	maxGrants        = 20
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ" // no vowels, no look-alikes (RFC 8628 section 6.1)
	userCodeLen      = 8                      // 20^8 ≈ 2^34.6

	// Wrong pairing codes: a session is locked after pairSessionMax, all pairing after
	// pairGlobalMax within pairWindow, for pairWindow.
	pairSessionMax = 5
	pairGlobalMax  = 30
	pairWindow     = 10 * time.Minute
)

// Grant states.
const (
	grantPending  = "pending"
	grantApproved = "approved"
	grantDenied   = "denied"
)

// deviceGrant is a pending pairing, keyed by the hash of its device code.
type deviceGrant struct {
	client   Client
	resource string
	userCode string // hashKey of the normalized user code
	expires  time.Time
	interval time.Duration
	lastPoll time.Time
	status   string
	decision decision
}

// pairingLimit counts wrong pairing codes across all sessions.
type pairingLimit struct {
	failures    []time.Time
	lockedUntil time.Time
}

func (s *Server) deviceAuthorization(w http.ResponseWriter, r *http.Request) {
	form, ok := parseForm(w, r)
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	client, err := s.cfg.Clients.Resolve(r.Context(), form["client_id"])
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	resource := form["resource"]
	if resource == "" {
		resource = s.cfg.Resource
	}
	if resource != s.cfg.Resource {
		oauthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	deviceCode, userCode := "hmd_"+newSecret(), newUserCode()
	now := s.cfg.Now()
	s.mu.Lock()
	for k, g := range s.grants {
		if !now.Before(g.expires) {
			delete(s.grants, k)
		}
	}
	if len(s.grants) >= maxGrants {
		s.mu.Unlock()
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	s.grants[hashKey(deviceCode)] = &deviceGrant{client: client, resource: resource, userCode: hashKey(userCode),
		expires: now.Add(deviceTTL), interval: deviceInterval, status: grantPending}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":      deviceCode,
		"user_code":        userCode[:4] + "-" + userCode[4:],
		"verification_uri": s.cfg.PublicURL + PairPath,
		"expires_in":       int(deviceTTL.Seconds()),
		"interval":         int(deviceInterval.Seconds()),
	})
}

// newUserCode draws userCodeLen characters uniformly from userCodeAlphabet.
func newUserCode() string {
	out := make([]byte, 0, userCodeLen)
	var b [1]byte
	for len(out) < userCodeLen {
		_, _ = rand.Read(b[:])
		if int(b[0]) < 256-256%len(userCodeAlphabet) { // rejection sampling: no modulo bias
			out = append(out, userCodeAlphabet[int(b[0])%len(userCodeAlphabet)])
		}
	}
	return string(out)
}

// normalizeUserCode accepts the code as typed (lower case, with dash or spaces);
// anything else gives "".
func normalizeUserCode(code string) string {
	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(code) != userCodeLen {
		return ""
	}
	for _, c := range code {
		if !strings.ContainsRune(userCodeAlphabet, c) {
			return ""
		}
	}
	return code
}

// pollDevice answers the agent's polling (RFC 8628 section 3.5).
func (s *Server) pollDevice(w http.ResponseWriter, r *http.Request, form map[string]string) {
	if form["device_code"] == "" || form["client_id"] == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key := hashKey(form["device_code"])
	now := s.cfg.Now()
	s.mu.Lock()
	g, ok := s.grants[key]
	switch {
	case !ok || g.client.ID != form["client_id"]:
		s.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "invalid_grant")
	case !now.Before(g.expires):
		delete(s.grants, key)
		s.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "expired_token")
	case g.status == grantDenied:
		delete(s.grants, key)
		s.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "access_denied")
	case g.status == grantApproved:
		delete(s.grants, key)
		s.mu.Unlock()
		s.admit(w, r, g.client, g.resource, g.decision)
	case !g.lastPoll.IsZero() && now.Sub(g.lastPoll) < g.interval:
		g.interval += deviceInterval
		g.lastPoll = now
		s.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "slow_down")
	default:
		g.lastPoll = now
		s.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "authorization_pending")
	}
}

// pendingGrant returns a copy of an unexpired pending grant.
func (s *Server) pendingGrant(key string) (deviceGrant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[key]
	if !ok || g.status != grantPending || !s.cfg.Now().Before(g.expires) {
		return deviceGrant{}, false
	}
	return *g, true
}

func (s *Server) approveGrant(key string, d decision) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[key]
	if !ok || g.status != grantPending || !s.cfg.Now().Before(g.expires) {
		return false
	}
	g.status, g.decision = grantApproved, d
	return true
}

func (s *Server) denyGrant(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.grants[key]; ok && g.status == grantPending {
		g.status = grantDenied
	}
}

// findGrant returns the key of the pending grant with this user code.
func (s *Server) findGrant(userCode string) (string, bool) {
	if userCode == "" {
		return "", false
	}
	want := hashKey(userCode)
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, g := range s.grants {
		if g.userCode == want && g.status == grantPending && now.Before(g.expires) {
			return key, true
		}
	}
	return "", false
}

// pairingLocked reports whether all pairing is locked after too many wrong codes.
func (s *Server) pairingLocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Now().Before(s.pairing.lockedUntil)
}

// recordPairFailure counts a wrong code and locks pairing for everyone once the global
// limit is reached.
func (s *Server) recordPairFailure() {
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := s.pairing.failures[:0]
	for _, t := range s.pairing.failures {
		if now.Sub(t) < pairWindow {
			recent = append(recent, t)
		}
	}
	s.pairing.failures = append(recent, now)
	if len(s.pairing.failures) >= pairGlobalMax {
		s.pairing.lockedUntil = now.Add(pairWindow)
		s.pairing.failures = nil
	}
}

// pairSession is a snapshot of a pairing session.
type pairSession struct {
	user     *ha.User
	csrf     string
	failures int
}

func (s *Server) pairSession(r *http.Request) (pairSession, bool) {
	var ps pairSession
	found := false
	s.sessions.with(s.sessionID(r), func(sess *session) {
		if sess.purpose == purposePair {
			ps = pairSession{user: sess.user, csrf: sess.csrf, failures: sess.failures}
			found = true
		}
	})
	return ps, found
}

// pairPage shows the code entry to a signed-in administrator, or starts the sign-in.
func (s *Server) pairPage(w http.ResponseWriter, r *http.Request) {
	ps, found := s.pairSession(r)
	if !found || ps.user == nil {
		id, sess, err := s.sessions.create(purposePair, nil)
		if err != nil {
			s.fail(w, r, http.StatusServiceUnavailable, i18n.PageBusy)
			return
		}
		s.setCookie(w, id)
		http.Redirect(w, r, s.cfg.SignIn.AuthorizeURL(sess.haState), http.StatusFound)
		return
	}
	if ps.failures >= pairSessionMax || s.pairingLocked() {
		s.fail(w, r, http.StatusTooManyRequests, i18n.PagePairLocked)
		return
	}
	s.renderPair(w, r, ps, http.StatusOK, "")
}

func (s *Server) renderPair(w http.ResponseWriter, r *http.Request, ps pairSession, status int, errKey i18n.Key) {
	s.render(w, status, "pair", page{Lang: language(r), Title: i18n.PagePairTitle, User: ps.user.Name, CSRF: ps.csrf, Error: errKey}, "")
}

// pair takes a pairing code. Every wrong code is logged; too many lock the session and,
// across sessions, all pairing (negative catalog: brute force).
func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, i18n.PageInvalidRequest)
		return
	}
	form, ok := parseForm(w, r)
	ps, found := s.pairSession(r)
	if !ok || !found || ps.user == nil || !equalSecret(ps.csrf, form["csrf"]) {
		s.fail(w, r, http.StatusForbidden, i18n.PageSessionExpired)
		return
	}
	if ps.failures >= pairSessionMax || s.pairingLocked() {
		s.fail(w, r, http.StatusTooManyRequests, i18n.PagePairLocked)
		return
	}
	id := s.sessionID(r)
	key, ok := s.findGrant(normalizeUserCode(form["code"]))
	if !ok {
		s.sessions.with(id, func(sess *session) { sess.failures++ })
		s.recordPairFailure()
		s.rejectUser(r.Context(), ps.user.ID, "pairing_code_invalid")
		if ps.failures+1 >= pairSessionMax || s.pairingLocked() {
			s.fail(w, r, http.StatusTooManyRequests, i18n.PagePairLocked)
			return
		}
		s.renderPair(w, r, ps, http.StatusBadRequest, i18n.PagePairInvalid)
		return
	}
	s.sessions.with(id, func(sess *session) { sess.device = key })
	http.Redirect(w, r, ConsentPath, http.StatusSeeOther)
}
