// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

const (
	codeTTL     = 60 * time.Second
	maxCodes    = 100
	maxStateLen = 512
)

// challengePattern is an S256 code challenge: base64url of a SHA-256 (RFC 7636).
var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// authzRequest is a validated authorization request of an agent.
type authzRequest struct {
	client      Client
	redirectURI string
	state       string
	challenge   string
	resource    string
}

// decision is what the human chose on the consent page or in the UI.
type decision struct {
	name, template  string
	templateDigest  string // the template as the human saw it
	mandateName     string
	confirmCritical bool
	by              string // Home Assistant user ID
}

// authCode is an authorization code waiting to be exchanged, at most codeTTL.
type authCode struct {
	authz    authzRequest
	decision decision
	expires  time.Time
}

// authorize starts the Authorization Code flow: the request is validated, then the
// human signs in through Home Assistant. Errors about the client or its redirect URI
// are shown to the human and never redirected (RFC 6749 section 4.1.2.1).
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q, ok := singleValues(r.URL.Query())
	if !ok {
		s.fail(w, r, http.StatusBadRequest, i18n.PageInvalidRequest)
		return
	}
	client, err := s.cfg.Clients.Resolve(r.Context(), q["client_id"])
	if err != nil || !client.Verified || !redirectAllowed(client.RedirectURIs, q["redirect_uri"]) {
		s.warn("authorization request refused: client or redirect URI", "error", err)
		s.fail(w, r, http.StatusBadRequest, i18n.PageInvalidClient)
		return
	}
	state := q["state"]
	if len(state) > maxStateLen || strings.ContainsFunc(state, func(c rune) bool { return c < ' ' || c > '~' }) {
		s.fail(w, r, http.StatusBadRequest, i18n.PageInvalidRequest)
		return
	}
	authz := &authzRequest{client: client, redirectURI: q["redirect_uri"], state: state, challenge: q["code_challenge"], resource: q["resource"]}
	switch {
	case q["response_type"] != "code":
		s.redirectError(w, r, authz, "unsupported_response_type")
		return
	case q["code_challenge_method"] != "S256" || !challengePattern.MatchString(authz.challenge):
		s.redirectError(w, r, authz, "invalid_request")
		return
	case authz.resource != s.cfg.Resource:
		s.redirectError(w, r, authz, "invalid_target")
		return
	}
	id, sess, err := s.sessions.create(s.clientAddr(r), purposeAuthorize, authz)
	if err != nil {
		s.fail(w, r, http.StatusServiceUnavailable, i18n.PageBusy)
		return
	}
	s.setCookie(w, id)
	http.Redirect(w, r, s.cfg.SignIn.AuthorizeURL(sess.haState), http.StatusFound)
}

// redirectError sends the browser back to the agent with an error (RFC 6749 section
// 4.1.2.1, issuer per RFC 9207).
func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, authz *authzRequest, code string) {
	s.redirectToClient(w, r, authz, url.Values{"error": {code}}, http.StatusFound)
}

func (s *Server) redirectToClient(w http.ResponseWriter, r *http.Request, authz *authzRequest, params url.Values, status int) {
	u, err := url.Parse(authz.redirectURI) // validated against the registered URIs
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, i18n.PageInvalidClient)
		return
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	if authz.state != "" {
		q.Set("state", authz.state)
	}
	q.Set("iss", s.cfg.PublicURL)
	u.RawQuery = q.Encode()
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, u.String(), status)
}

// callback receives the human from Home Assistant. Only an administrator continues,
// with a new session cookie. A failed attempt ends the sign-in.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	id := s.sessionID(r)
	q, ok := singleValues(r.URL.Query())
	purpose, stored, valid := s.signInState(id, q["state"])
	if !ok || !valid {
		s.endSession(w, id)
		s.fail(w, r, http.StatusBadRequest, i18n.PageSessionExpired)
		return
	}
	user, err := s.cfg.SignIn.SignIn(r.Context(), q["code"])
	if err != nil {
		s.warn("sign-in through Home Assistant failed", "error", err)
		s.endSession(w, id)
		s.fail(w, r, http.StatusBadGateway, i18n.PageSignInFailed)
		return
	}
	if !user.IsAdmin {
		s.rejectUser(r.Context(), user.ID, "not_admin")
		s.endSession(w, id)
		s.fail(w, r, http.StatusForbidden, i18n.PageNotAdmin)
		return
	}
	newID, err := s.signedIn(r, id, stored, user)
	switch {
	case errors.Is(err, errTooManySessions):
		s.endSession(w, id)
		s.fail(w, r, http.StatusServiceUnavailable, i18n.PageBusy)
		return
	case err != nil:
		s.endSession(w, id)
		s.fail(w, r, http.StatusBadRequest, i18n.PageSessionExpired)
		return
	}
	s.setCookie(w, newID)
	next := ConsentPath
	if purpose == purposePair {
		next = PairPath
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// signInState checks the state Home Assistant sent back against the browser's sign-in:
// its session, whose state the first attempt uses up, or else a pairState (stored false).
func (s *Server) signInState(id, state string) (purpose string, stored, valid bool) {
	var haState string
	if s.sessions.with(id, func(sess *session) {
		haState, purpose = sess.haState, sess.purpose
		sess.haState = "" // one attempt per sign-in
	}) {
		return purpose, true, equalSecret(haState, state)
	}
	return purposePair, false, s.validPairState(id, state)
}

// signedIn gives the administrator a session with a new cookie value: the stored session
// rotated, or a new one after a pairing sign-in, which had none.
func (s *Server) signedIn(r *http.Request, id string, stored bool, user ha.User) (string, error) {
	var newID string
	if stored {
		var ok bool
		if newID, ok = s.sessions.rotate(id); !ok {
			return "", errSessionExpired
		}
	} else {
		var err error
		if newID, _, err = s.sessions.create(s.clientAddr(r), purposePair, nil); err != nil {
			return "", err
		}
	}
	s.sessions.with(newID, func(sess *session) { sess.user, sess.haState = &user, "" })
	return newID, nil
}

func (s *Server) endSession(w http.ResponseWriter, id string) {
	s.sessions.delete(id)
	s.clearCookie(w)
}

// consentState is a snapshot of a signed-in session that is admitting an agent.
type consentState struct {
	user    ha.User
	csrf    string
	authz   *authzRequest
	device  string
	grantID string
	client  Client
}

// consentSession returns the signed-in session of r with an agent to admit.
func (s *Server) consentSession(r *http.Request) (consentState, bool) {
	var st consentState
	ok := false
	s.sessions.with(s.sessionID(r), func(sess *session) {
		if sess.user == nil {
			return
		}
		st = consentState{user: *sess.user, csrf: sess.csrf, authz: sess.authz, device: sess.device}
		ok = sess.authz != nil || sess.device != ""
	})
	if !ok {
		return consentState{}, false
	}
	if st.authz != nil {
		st.client = st.authz.client
		return st, true
	}
	g, ok := s.pendingGrant(st.device)
	st.client, st.grantID = g.client, g.id
	return st, ok
}

func (s *Server) consentPage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.consentSession(r)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, i18n.PageSessionExpired)
		return
	}
	s.renderConsent(w, r, st, st.client.Name, "", "")
}

func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, st consentState, name, selected string, errKey i18n.Key) {
	templates, err := s.consentTemplates(r.Context(), language(r))
	if err != nil {
		s.cfg.Logger.Error("listing mandate templates failed", "error", err)
		s.fail(w, r, http.StatusServiceUnavailable, i18n.PageBusy)
		return
	}
	if len(templates) == 0 {
		s.message(w, r, http.StatusConflict, i18n.PageConsentTitle, i18n.PageConsentNoTemplates)
		return
	}
	if selected == "" {
		selected = templates[0].Name // the most cautious comes first
	}
	templates = s.withApprovers(r.Context(), templates, st.user.ID)
	p := page{Lang: language(r), Title: i18n.PageConsentTitle, User: st.user.Name, CSRF: st.csrf, Claimed: st.client.Name,
		ClientID: st.client.ID, Verified: st.client.Verified, Name: name, Selected: selected, Templates: templates, Error: errKey}
	formTarget := ""
	if u, err := url.Parse(st.client.ID); err == nil && st.client.Verified {
		p.Host = u.Host
	}
	if st.authz != nil {
		if u, err := url.Parse(st.authz.redirectURI); err == nil {
			p.ReturnHost = u.Host
			formTarget = u.Scheme + "://" + u.Host
		}
	}
	status := http.StatusOK
	if errKey != "" {
		status = http.StatusBadRequest
	}
	s.render(w, status, "consent", p, formTarget)
}

// templateNames are the templates a human may choose: hidden base templates are left out.
func (s *Server) templateNames(r *http.Request) ([]string, error) {
	list, err := s.cfg.Admission.Templates(r.Context())
	if err != nil {
		s.cfg.Logger.Error("listing mandate templates failed", "error", err)
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, t := range list {
		if !t.Hidden {
			names = append(names, t.Name)
		}
	}
	return names, nil
}

// consent takes the human's decision. One session admits at most one agent.
func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, i18n.PageInvalidRequest)
		return
	}
	form, ok := parseForm(w, r)
	st, signedIn := s.consentSession(r)
	if !ok || !signedIn || !equalSecret(st.csrf, form["csrf"]) {
		s.fail(w, r, http.StatusForbidden, i18n.PageSessionExpired)
		return
	}
	id := s.sessionID(r)
	switch form["action"] {
	case "deny":
		if !s.decide(w, id, form["csrf"]) {
			s.fail(w, r, http.StatusForbidden, i18n.PageSessionExpired)
			return
		}
		if st.authz != nil {
			s.redirectToClient(w, r, st.authz, url.Values{"error": {"access_denied"}}, http.StatusSeeOther)
			return
		}
		s.denyGrant(st.device)
		s.message(w, r, http.StatusOK, i18n.PageConsentTitle, i18n.PageDenied)
	case "approve":
		name := strings.TrimSpace(form["name"])
		// The choice names the template and the digest of what the page showed of it.
		tmpl, shown, _ := strings.Cut(form["template"], "@")
		templates, err := s.templateNames(r)
		if err != nil {
			s.fail(w, r, http.StatusServiceUnavailable, i18n.PageBusy)
			return
		}
		if !displayable(name) || !slices.Contains(templates, tmpl) || !strings.HasPrefix(shown, "sha256:") {
			s.renderConsent(w, r, st, name, tmpl, i18n.PageConsentInvalid)
			return
		}
		d := decision{name: name, template: tmpl, templateDigest: shown, by: st.user.ID}
		if !s.decide(w, id, form["csrf"]) {
			s.fail(w, r, http.StatusForbidden, i18n.PageSessionExpired)
			return
		}
		if st.authz != nil {
			code, err := s.issueCode(*st.authz, d)
			if err != nil {
				s.redirectToClient(w, r, st.authz, url.Values{"error": {"temporarily_unavailable"}}, http.StatusSeeOther)
				return
			}
			s.redirectToClient(w, r, st.authz, url.Values{"code": {code}}, http.StatusSeeOther)
			return
		}
		if _, err := s.admitGrant(r.Context(), st.device, st.grantID, d); err != nil {
			switch {
			case errors.Is(err, ErrPairingAdmission):
				s.fail(w, r, http.StatusBadRequest, i18n.PageConsentInvalid)
			case errors.Is(err, ErrPairingUnavailable):
				s.fail(w, r, http.StatusServiceUnavailable, i18n.PageBusy)
			default:
				s.fail(w, r, http.StatusBadRequest, i18n.PagePairInvalid)
			}
			return
		}
		s.message(w, r, http.StatusOK, i18n.PageConsentTitle, i18n.PageAdmitted)
	default:
		s.fail(w, r, http.StatusBadRequest, i18n.PageInvalidRequest)
	}
}

// decide ends the session for the decision; false if another request decided first.
func (s *Server) decide(w http.ResponseWriter, id, csrf string) bool {
	if !s.sessions.take(id, csrf) {
		return false
	}
	s.clearCookie(w)
	return true
}

// issueCode stores an authorization code for the decision and returns it.
func (s *Server) issueCode(authz authzRequest, d decision) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	for k, c := range s.codes {
		if !now.Before(c.expires) {
			delete(s.codes, k)
		}
	}
	if len(s.codes) >= maxCodes {
		return "", errTooManySessions
	}
	code := "hmc_" + newSecret()
	s.codes[hashKey(code)] = &authCode{authz: authz, decision: d, expires: now.Add(codeTTL)}
	return code, nil
}

// takeCode removes and returns an unexpired authorization code: each code is usable once.
func (s *Server) takeCode(code string) (*authCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashKey(code)
	c, ok := s.codes[key]
	delete(s.codes, key)
	if !ok || !s.cfg.Now().Before(c.expires) {
		return nil, false
	}
	return c, true
}

// hashKey is the map key of a secret: its SHA-256 in hex, never the secret itself.
func hashKey(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
