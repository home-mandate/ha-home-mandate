// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// Direct mode (ARCHITECTURE section 12): in container mode without Ingress, the UI is
// served on the MCP listener under /ui/ and Home-Mandate signs the human in itself,
// through Home Assistant. No header of the Supervisor or of Home Assistant is read.

// SignIn signs a human in through Home Assistant (oauth.HASignIn with the UI's callback).
type SignIn interface {
	AuthorizeURL(state string) string
	SignIn(ctx context.Context, code string) (ha.User, error)
}

// Paths of direct mode; the UI's own files and API are below DirectPrefix.
const (
	DirectPrefix       = "/ui"
	DirectCallbackPath = DirectPrefix + "/signin/callback"
	directSignInPath   = DirectPrefix + "/signin"
	directSignOutPath  = DirectPrefix + "/signout"

	uiCookie     = "__Host-hm_ui"
	signInCookie = "__Host-hm_signin"

	signInTimeout = 15 * time.Second

	// Sign-ins need no session, and each callback makes the gateway ask Home Assistant
	// (oauth.HASignIn): starts and callbacks are limited per sender (IPv6 by /64) and
	// overall, and only a few wait for Home Assistant at once. Beyond that the browser is
	// told to try again; nobody already signed in is affected.
	signInPeriod    = time.Minute
	signInPerSender = 10
	signInOverall   = 60
	signInAtOnce    = 4
)

// Where a failed sign-in sends the browser: the UI says why, from its own catalog.
const (
	signInDenied = DirectPrefix + "/#/signin?error=denied" // no administrator
	signInFailed = DirectPrefix + "/#/signin?error=failed" // anything else
	signInBusy   = DirectPrefix + "/#/signin?error=busy"   // too many sign-ins
)

type entryKey struct{}

// entry is how a request came in. Requests through Ingress carry none: there the
// Supervisor names the user.
type entry struct {
	direct bool
	user   string // from the session; empty without one
	token  string
}

func entryOf(r *http.Request) (*entry, bool) {
	e, ok := r.Context().Value(entryKey{}).(*entry)
	return e, ok && e.direct
}

// identify is the user of a request: from the session in direct mode, from the
// Supervisor's header behind Ingress.
func identify(r *http.Request) (string, bool) {
	if e, ok := entryOf(r); ok {
		return e.user, e.user != ""
	}
	return userOf(r)
}

// DirectHandler serves direct mode under DirectPrefix; nil when it is off (no sign-in
// configured).
func (s *Server) DirectHandler() http.Handler {
	if s.cfg.Direct == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHeaders(w.Header())
		switch p := r.URL.Path; {
		case p == DirectPrefix:
			http.Redirect(w, r, DirectPrefix+"/", http.StatusMovedPermanently)
		case (p == directSignInPath || p == DirectCallbackPath) && r.Method == http.MethodGet && !s.signInAllowed(r):
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, signInBusy, http.StatusSeeOther)
		case p == directSignInPath && r.Method == http.MethodGet:
			s.startSignIn(w, r)
		case p == DirectCallbackPath && r.Method == http.MethodGet:
			s.finishSignIn(w, r)
		case p == directSignOutPath && r.Method == http.MethodPost:
			s.signOut(w, r)
		case p == DirectPrefix+"/api" || strings.HasPrefix(p, DirectPrefix+"/api/"):
			s.mux.ServeHTTP(w, s.directRequest(r))
		case strings.HasPrefix(p, DirectPrefix+"/") && s.cfg.DirectUI != nil:
			s.cfg.DirectUI.ServeHTTP(w, stripPrefix(r))
		default:
			http.NotFound(w, r)
		}
	})
}

// directHeaders go on every answer in direct mode, which the browser reaches over HTTPS
// at the public URL: stay on HTTPS (the operator's other hosts are not ours to name, so
// no includeSubDomains), and keep the page out of the browsing context of other pages.
// Behind Ingress Home Assistant's frontend owns the origin and sets neither.
func directHeaders(h http.Header) {
	h.Set("Strict-Transport-Security", "max-age=31536000")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
}

// signInAllowed counts a sign-in start or callback against the overall limit and its
// sender's. The overall limit comes first: no sender gets a window of its own unless the
// overall limit let the request through, so unauthenticated requests cannot grow the
// limits' table faster than signInOverall a period.
func (s *Server) signInAllowed(r *http.Request) bool {
	// Only a request its sender may make counts overall, so one sender cannot use up the
	// sign-ins of everyone. A sender's window is created only while there is room overall,
	// which bounds the windows unauthenticated requests can create.
	ok := s.limits.room("signin", signInOverall, signInPeriod)
	if ok {
		ok, _ = s.limits.allow("signin:"+sender(r), signInPerSender, signInPeriod)
	}
	if ok {
		ok, _ = s.limits.allow("signin", signInOverall, signInPeriod)
	}
	if !ok {
		if logged, _ := s.limits.allow("log:signin", 1, time.Minute); logged {
			s.cfg.Logger.Warn("UI sign-ins beyond the limit refused", "remote", peer(r))
		}
	}
	return ok
}

// directRequest strips the prefix and carries the session's user, if any.
func (s *Server) directRequest(r *http.Request) *http.Request {
	e := &entry{direct: true}
	if c, err := r.Cookie(uiCookie); err == nil {
		if user, ok := s.ui.lookup(c.Value); ok {
			e.user, e.token = user, c.Value
		}
	}
	return stripPrefix(r.WithContext(context.WithValue(r.Context(), entryKey{}, e)))
}

func stripPrefix(r *http.Request) *http.Request {
	out := r.Clone(r.Context())
	out.URL.Path = strings.TrimPrefix(r.URL.Path, DirectPrefix)
	out.URL.RawPath = ""
	return out
}

// peer is the address the request came from: the connection's, or behind the reverse
// proxy of HM_PROXY the sender it named (cmd/home-mandate, onlyProxy).
func peer(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sender is whom the limits of sign-ins count a request against: the address, an IPv6
// address by its /64, which one subscriber usually holds whole.
func sender(r *http.Request) string {
	host := peer(r)
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr.String()
	}
	network, _ := addr.Prefix(64) // never fails for an IPv6 address
	return network.String()
}

// startSignIn needs no session and keeps nothing per address beyond the bounded table of
// sign-ins in progress (uisessions.go) and the windows of signInAllowed: unauthenticated
// requests never grow other state.
func (s *Server) startSignIn(w http.ResponseWriter, r *http.Request) {
	state := s.ui.startSignIn(peer(r))
	// Lax: Home Assistant sends the browser back with a top-level navigation from its
	// own origin, which a Strict cookie would not accompany.
	http.SetCookie(w, &http.Cookie{Name: signInCookie, Value: state, Path: "/", MaxAge: int(signInTTL / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.cfg.Direct.AuthorizeURL(state), http.StatusSeeOther)
}

func (s *Server) finishSignIn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, &http.Cookie{Name: signInCookie, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	cookie, _ := r.Cookie(signInCookie)
	if cookie == nil || len(q["state"]) != 1 || len(q["code"]) != 1 || !s.ui.finishSignIn(cookie.Value, q.Get("state")) {
		s.cfg.Logger.Warn("UI sign-in refused: state missing, reused or not matching")
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), signInTimeout)
	defer cancel()
	user, busy, err := s.exchange(ctx, q.Get("code"))
	if busy {
		s.cfg.Logger.Warn("UI sign-in refused: too many waiting for Home Assistant")
		http.Redirect(w, r, signInBusy, http.StatusSeeOther)
		return
	}
	if err != nil || !userIDPattern.MatchString(user.ID) {
		s.cfg.Logger.Warn("UI sign-in through Home Assistant failed", "error", err)
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	admin, err := s.users.IsAdmin(ctx, user.ID)
	switch {
	case err != nil:
		s.cfg.Logger.Warn("administrator check failed, UI sign-in refused", "error", err)
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	case !admin:
		s.cfg.Logger.Warn("UI sign-in of a user who is no administrator refused", "user_id", user.ID)
		http.Redirect(w, r, signInDenied, http.StatusSeeOther)
		return
	}
	token, err := s.ui.create(user.ID)
	if err != nil {
		http.Redirect(w, r, signInBusy, http.StatusSeeOther)
		return
	}
	setUICookie(w, token, 0)
	s.cfg.Logger.Info("signed in to the UI", "user_id", user.ID)
	http.Redirect(w, r, DirectPrefix+"/", http.StatusSeeOther)
}

// exchange signs the user in through Home Assistant with code; busy when signInAtOnce
// sign-ins are waiting for it already.
func (s *Server) exchange(ctx context.Context, code string) (user ha.User, busy bool, err error) {
	select {
	case s.signing <- struct{}{}:
		defer func() { <-s.signing }()
	default:
		return ha.User{}, true, nil
	}
	user, err = s.cfg.Direct.SignIn(ctx, code)
	return user, false, err
}

func setUICookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: uiCookie, Value: value, Path: "/", MaxAge: maxAge, Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode})
}

// signOut ends the session; like every write it needs the page's own request and the
// CSRF token.
func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	r = s.directRequest(r)
	e, _ := entryOf(r) // directRequest always marks the request as direct
	if e.user == "" {
		writeError(w, fail(codeUnauthenticated))
		return
	}
	if err := s.checkWrite(r, e.user); err != nil {
		writeError(w, err)
		return
	}
	s.ui.end(e.token)
	setUICookie(w, "", -1)
	s.hub.endSession(e.token)
	apiHeaders(w.Header())
	w.WriteHeader(http.StatusNoContent)
}

// sameOrigin tells whether an Origin is the public URL, in direct mode the only origin
// of the UI.
func (s *Server) sameOrigin(origin string) bool {
	return s.cfg.PublicURL != "" && strings.EqualFold(origin, s.cfg.PublicURL)
}
