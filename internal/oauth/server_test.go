// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/spec"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

const (
	testPublicURL = "https://hm.test"
	testResource  = testPublicURL + "/mcp"
	testClient    = "https://claude.example.org/client.json"
	testRedirect  = "http://127.0.0.1:33418/callback"
	testHousehold = "household:hm-0123456789ab"
)

var adminUser = ha.User{ID: "u-admin", Name: "Markus", IsAdmin: true}

type fakeSignIn struct{ users map[string]ha.User }

func (fakeSignIn) AuthorizeURL(state string) string {
	return "https://ha.test/auth/authorize?state=" + url.QueryEscape(state)
}

func (f fakeSignIn) SignIn(_ context.Context, code string) (ha.User, error) {
	u, ok := f.users[code]
	if !ok {
		return ha.User{}, ErrSignInFailed
	}
	return u, nil
}

type fakeClients map[string]Client

func (f fakeClients) Resolve(_ context.Context, id string) (Client, error) {
	c, ok := f[id]
	if !ok {
		return Client{}, ErrInvalidClient
	}
	return c, nil
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type harness struct {
	t      *testing.T
	srv    *httptest.Server
	server *Server
	clock  *testClock
	agents *agent.Store
	adm    *admission.Store
	log    *audit.Log
}

// voiceChoice is the consent page's value for the voice assistant template: its name and
// the digest of what the page showed of it. Every harness stores the same document.
var (
	voiceChoice string
	voiceOnce   sync.Once
)

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), testHousehold)
	agents := agent.New(st.DB(), log)
	mandates := mandate.New(st.DB(), log, testHousehold, "urn:uuid:5b0c9f4e-8f1a-4c2e-9d3b-7a6e5f4d3c2b")
	adm := admission.New(st.DB(), log, agents, mandates, testHousehold)
	data, err := fs.ReadFile(spec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.PutTemplate(ctx, "voice-assistant", data, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	voiceOnce.Do(func() {
		_, info, err := adm.TemplateDocument(ctx, "voice-assistant")
		if err != nil {
			t.Fatal(err)
		}
		voiceChoice = "voice-assistant@" + info.Digest
	})
	h := &harness{t: t, clock: clock, agents: agents, adm: adm, log: log}
	h.server = New(Config{PublicURL: testPublicURL, Resource: testResource,
		SignIn: fakeSignIn{users: map[string]ha.User{"admin-code": adminUser, "plain-code": {ID: "u-plain", Name: "Plain"}}},
		Clients: fakeClients{
			testClient:    {ID: testClient, Name: "Claude <b>Code</b>", RedirectURIs: []string{testRedirect, "https://app.example.org/cb"}, Verified: true},
			"n8n-kitchen": {ID: "n8n-kitchen", Name: "n8n-kitchen"},
		},
		Admission: adm, Tokens: agents, Audit: log, Now: clock.Now})
	h.srv = httptest.NewTLSServer(h.server.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// browser is a cookie-keeping client that does not follow redirects.
type browser struct {
	h *harness
	c *http.Client
}

func (h *harness) browser() *browser {
	jar, _ := cookiejar.New(nil)
	c := h.srv.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &browser{h: h, c: c}
}

type response struct {
	status   int
	header   http.Header
	body     string
	location string
}

func (b *browser) do(req *http.Request) response {
	b.h.t.Helper()
	resp, err := b.c.Do(req)
	if err != nil {
		b.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: string(body), location: resp.Header.Get("Location")}
}

func (b *browser) get(path string) response {
	b.h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, b.h.srv.URL+path, nil)
	return b.do(req)
}

func (b *browser) post(path string, form url.Values, header ...string) response {
	b.h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, b.h.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	return b.do(req)
}

var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrfOf(t *testing.T, body string) string {
	t.Helper()
	m := csrfPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no CSRF token in page:\n%s", body)
	}
	return m[1]
}

// pkce returns a verifier and its S256 challenge.
func pkce() (string, string) {
	verifier := newSecret()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func authorizeQuery(challenge string, edit func(url.Values)) string {
	q := url.Values{"response_type": {"code"}, "client_id": {testClient}, "redirect_uri": {testRedirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"agent-state"}, "resource": {testResource}}
	if edit != nil {
		edit(q)
	}
	return AuthorizePath + "?" + q.Encode()
}

// signIn follows the redirect to Home Assistant and comes back with haCode.
func (b *browser) signIn(toHA response, haCode string) response {
	b.h.t.Helper()
	if toHA.status != http.StatusFound || !strings.HasPrefix(toHA.location, "https://ha.test/auth/authorize?") {
		b.h.t.Fatalf("expected the redirect to Home Assistant, got %d %q\n%s", toHA.status, toHA.location, toHA.body)
	}
	u, _ := url.Parse(toHA.location)
	return b.get(CallbackPath + "?" + url.Values{"code": {haCode}, "state": {u.Query().Get("state")}}.Encode())
}

// consentAs signs in with haCode and returns the consent page.
func (b *browser) consentAs(challenge, haCode string) response {
	b.h.t.Helper()
	back := b.signIn(b.get(authorizeQuery(challenge, nil)), haCode)
	if back.status != http.StatusSeeOther || back.location != ConsentPath {
		b.h.t.Fatalf("callback = %d %q", back.status, back.location)
	}
	return b.get(ConsentPath)
}

// approve submits the consent form and returns the code from the redirect.
func (b *browser) approve(consentPage response) string {
	b.h.t.Helper()
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(b.h.t, consentPage.body)}, "action": {"approve"},
		"name": {"Claude"}, "template": {voiceChoice}})
	if res.status != http.StatusSeeOther || !strings.HasPrefix(res.location, testRedirect+"?") {
		b.h.t.Fatalf("consent = %d %q\n%s", res.status, res.location, res.body)
	}
	u, _ := url.Parse(res.location)
	q := u.Query()
	if q.Get("state") != "agent-state" || q.Get("iss") != testPublicURL || q.Get("code") == "" {
		b.h.t.Fatalf("redirect = %s", res.location)
	}
	return q.Get("code")
}

func (h *harness) tokenRequest(form url.Values) (int, map[string]any, http.Header) {
	h.t.Helper()
	resp, err := h.srv.Client().PostForm(h.srv.URL+TokenPath, form)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

func exchangeForm(code, verifier string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {testClient},
		"redirect_uri": {testRedirect}, "code_verifier": {verifier}}
}

func (h *harness) auditLog() string {
	h.t.Helper()
	var buf bytes.Buffer
	if err := h.log.Export(context.Background(), &buf); err != nil {
		h.t.Fatal(err)
	}
	return buf.String()
}

func countAgents(t *testing.T, h *harness) int {
	t.Helper()
	list, err := h.agents.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestMetadata(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	res := b.get(MetadataPath)
	var md map[string]any
	_ = json.Unmarshal([]byte(res.body), &md)
	if res.status != http.StatusOK || md["issuer"] != testPublicURL || md["token_endpoint"] != testPublicURL+TokenPath ||
		md["client_id_metadata_document_supported"] != true || md["registration_endpoint"] != nil ||
		!strings.Contains(res.body, `"code_challenge_methods_supported":["S256"]`) || res.header.Get("Cache-Control") != "no-store" {
		t.Errorf("metadata = %d %s", res.status, res.body)
	}
	for _, path := range []string{ResourceMetadataPath, ResourceMetadataPath + "/mcp"} {
		res := b.get(path)
		if res.status != http.StatusOK || !strings.Contains(res.body, `"resource":"`+testResource+`"`) ||
			!strings.Contains(res.body, `"authorization_servers":["`+testPublicURL+`"]`) {
			t.Errorf("%s = %d %s", path, res.status, res.body)
		}
	}
	if res := b.get(stylePath); res.status != http.StatusOK || !strings.HasPrefix(res.header.Get("Content-Type"), "text/css") {
		t.Errorf("style = %d %v", res.status, res.header)
	}
}

func TestAuthorizationCodeFlow(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	verifier, challenge := pkce()
	page := b.consentAs(challenge, "admin-code")
	if page.status != http.StatusOK {
		t.Fatalf("consent page = %d\n%s", page.status, page.body)
	}
	// The claimed name is shown escaped and marked as the agent's own claim; the page
	// has a strict policy that lets the form lead back to the agent only.
	if !strings.Contains(page.body, "Claude &lt;b&gt;Code&lt;/b&gt;") || strings.Contains(page.body, "<b>Code") ||
		!strings.Contains(page.body, "claude.example.org") || !strings.Contains(page.body, "Markus") {
		t.Errorf("consent page:\n%s", page.body)
	}
	csp := page.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "form-action 'self' http://127.0.0.1:33418") ||
		strings.Contains(csp, "unsafe") || page.header.Get("X-Frame-Options") != "DENY" || page.header.Get("Referrer-Policy") != "same-origin" ||
		!strings.Contains(page.body, `<meta name="referrer" content="same-origin">`) {
		t.Errorf("headers = %v", page.header)
	}
	code := b.approve(page)
	status, tokens, header := h.tokenRequest(exchangeForm(code, verifier))
	if status != http.StatusOK || tokens["token_type"] != "Bearer" || tokens["expires_in"] != float64(600) ||
		header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token = %d %v", status, tokens)
	}
	a, err := h.agents.Authenticate(context.Background(), tokens["access_token"].(string), testResource)
	if err != nil || a.DisplayName != "Claude" || a.OAuthClient != testClient || !a.ClientVerified || a.CreatedBy != "u-admin" {
		t.Errorf("agent = %+v, %v", a, err)
	}
	// The code is single-use.
	if status, out, _ := h.tokenRequest(exchangeForm(code, verifier)); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("second exchange = %d %v", status, out)
	}
	// Refresh with rotation through the endpoint.
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "client_id": {testClient}}
	status, next, _ := h.tokenRequest(refresh)
	if status != http.StatusOK || next["refresh_token"] == tokens["refresh_token"] {
		t.Fatalf("refresh = %d %v", status, next)
	}
	if status, out, _ := h.tokenRequest(refresh); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("reused refresh token = %d %v", status, out)
	}
	if _, err := h.agents.Authenticate(context.Background(), next["access_token"].(string), testResource); err == nil {
		t.Error("token of the reused family still valid")
	}
	out := h.auditLog()
	if !strings.Contains(out, `"event":"agent.registered"`) || !strings.Contains(out, `"id":"u-admin"`) ||
		!strings.Contains(out, `"error":"refresh_token_reused"`) {
		t.Errorf("audit log:\n%s", out)
	}
}

// Negative catalog: PKCE missing or wrong verifier, redirect URI differs, client
// metadata unusable → rejected. Errors about client and redirect URI are never
// redirected.
func TestAuthorizeRejects(t *testing.T) {
	h := newHarness(t)
	_, challenge := pkce()
	pages := map[string]func(url.Values){
		"unknown client":       func(q url.Values) { q.Set("client_id", "https://evil.example.org/c.json") },
		"unverified client":    func(q url.Values) { q.Set("client_id", "n8n-kitchen") },
		"no client":            func(q url.Values) { q.Del("client_id") },
		"other redirect":       func(q url.Values) { q.Set("redirect_uri", "https://evil.example.org/cb") },
		"redirect upper case":  func(q url.Values) { q.Set("redirect_uri", "https://APP.example.org/cb") },
		"redirect longer path": func(q url.Values) { q.Set("redirect_uri", "https://app.example.org/cb/x") },
		"no redirect":          func(q url.Values) { q.Del("redirect_uri") },
		"repeated parameter":   func(q url.Values) { q.Add("state", "again") },
		"state with newline":   func(q url.Values) { q.Set("state", "a\nb") },
		"state too long":       func(q url.Values) { q.Set("state", strings.Repeat("s", 513)) },
	}
	for name, edit := range pages {
		res := h.browser().get(authorizeQuery(challenge, edit))
		if res.status != http.StatusBadRequest || res.location != "" {
			t.Errorf("%s: %d %q", name, res.status, res.location)
		}
	}
	redirects := map[string]struct {
		edit func(url.Values)
		want string
	}{
		"implicit flow":       {func(q url.Values) { q.Set("response_type", "token") }, "unsupported_response_type"},
		"no challenge":        {func(q url.Values) { q.Del("code_challenge") }, "invalid_request"},
		"plain challenge":     {func(q url.Values) { q.Set("code_challenge_method", "plain") }, "invalid_request"},
		"short challenge":     {func(q url.Values) { q.Set("code_challenge", "abc") }, "invalid_request"},
		"no resource":         {func(q url.Values) { q.Del("resource") }, "invalid_target"},
		"other resource":      {func(q url.Values) { q.Set("resource", "https://other.example.org/mcp") }, "invalid_target"},
		"resource with slash": {func(q url.Values) { q.Set("resource", testResource+"/") }, "invalid_target"},
	}
	for name, tc := range redirects {
		res := h.browser().get(authorizeQuery(challenge, tc.edit))
		u, _ := url.Parse(res.location)
		if res.status != http.StatusFound || !strings.HasPrefix(res.location, testRedirect) || u.Query().Get("error") != tc.want ||
			u.Query().Get("state") != "agent-state" || u.Query().Get("iss") != testPublicURL {
			t.Errorf("%s: %d %q", name, res.status, res.location)
		}
	}
}

// Negative catalog: PKCE missing or wrong verifier, redirect URI or client differ, code
// expired → rejected at the token endpoint.
func TestTokenExchangeRejects(t *testing.T) {
	h := newHarness(t)
	consumes := map[string]func(url.Values){
		"wrong verifier": func(f url.Values) { f.Set("code_verifier", strings.Repeat("v", 43)) },
		"short verifier": func(f url.Values) { f.Set("code_verifier", "abc") },
		"other client":   func(f url.Values) { f.Set("client_id", "https://evil.example.org/c.json") },
		"other redirect": func(f url.Values) { f.Set("redirect_uri", "http://127.0.0.1:1/callback") },
		"other resource": func(f url.Values) { f.Set("resource", "https://other.example.org/mcp") },
	}
	for name, edit := range consumes {
		b := h.browser()
		verifier, challenge := pkce()
		code := b.approve(b.consentAs(challenge, "admin-code"))
		form := exchangeForm(code, verifier)
		edit(form)
		if status, out, _ := h.tokenRequest(form); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("%s: %d %v", name, status, out)
		}
		// A failed attempt consumes the code: no second try with the right values.
		if status, _, _ := h.tokenRequest(exchangeForm(code, verifier)); status != http.StatusBadRequest {
			t.Errorf("%s: code still usable", name)
		}
	}
	malformed := map[string]struct {
		edit func(url.Values, string)
		want string
	}{
		"no verifier":        {func(f url.Values, _ string) { f.Del("code_verifier") }, "invalid_request"},
		"repeated parameter": {func(f url.Values, v string) { f.Add("code_verifier", v) }, "invalid_request"},
		"unknown grant type": {func(f url.Values, _ string) { f.Set("grant_type", "password") }, "unsupported_grant_type"},
		"client credentials": {func(f url.Values, _ string) { f.Set("grant_type", "client_credentials") }, "unsupported_grant_type"},
		"unknown code":       {func(f url.Values, _ string) { f.Set("code", "hmc_"+strings.Repeat("A", 43)) }, "invalid_grant"},
	}
	for name, tc := range malformed {
		verifier, _ := pkce()
		form := exchangeForm("hmc_x", verifier)
		tc.edit(form, verifier)
		if status, out, _ := h.tokenRequest(form); status != http.StatusBadRequest || out["error"] != tc.want {
			t.Errorf("%s: %d %v", name, status, out)
		}
	}
	// An expired code.
	b := h.browser()
	verifier, challenge := pkce()
	code := b.approve(b.consentAs(challenge, "admin-code"))
	h.clock.Add(codeTTL)
	if status, out, _ := h.tokenRequest(exchangeForm(code, verifier)); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("expired code: %d %v", status, out)
	}
	// Not a form.
	resp, err := h.srv.Client().Post(h.srv.URL+TokenPath, "application/json", strings.NewReader(`{"grant_type":"refresh_token"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("JSON body: %d", resp.StatusCode)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents admitted", n)
	}
}

func TestRefreshRejects(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	verifier, challenge := pkce()
	_, tokens, _ := h.tokenRequest(exchangeForm(b.approve(b.consentAs(challenge, "admin-code")), verifier))
	rt := tokens["refresh_token"].(string)
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"other client":   {url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {"https://evil.example.org/c.json"}}, "invalid_grant"},
		"no client":      {url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}, "invalid_request"},
		"other resource": {url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {testClient}, "resource": {"https://x.example.org/mcp"}}, "invalid_target"},
		"access token":   {url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["access_token"].(string)}, "client_id": {testClient}}, "invalid_grant"},
	} {
		if status, out, _ := h.tokenRequest(tc.form); status != http.StatusBadRequest || out["error"] != tc.want {
			t.Errorf("%s: %d %v", name, status, out)
		}
	}
	// The refused attempts did not use the token up.
	ok := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {testClient}, "resource": {testResource}}
	if status, out, _ := h.tokenRequest(ok); status != http.StatusOK {
		t.Errorf("valid refresh: %d %v", status, out)
	}
}

// Negative catalog: admission by a non-admin → rejected (E2E scenario 10 at unit level).
func TestNonAdminCannotAdmit(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	_, challenge := pkce()
	res := b.signIn(b.get(authorizeQuery(challenge, nil)), "plain-code")
	if res.status != http.StatusForbidden || !strings.Contains(res.body, "administrators") {
		t.Errorf("non-admin = %d\n%s", res.status, res.body)
	}
	if res := b.get(ConsentPath); res.status != http.StatusBadRequest {
		t.Errorf("consent after refusal = %d", res.status)
	}
	if out := h.auditLog(); !strings.Contains(out, `"event":"auth.rejected"`) || !strings.Contains(out, `"error":"not_admin"`) ||
		!strings.Contains(out, `"id":"u-plain"`) {
		t.Errorf("audit log:\n%s", out)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents", n)
	}
}

func TestCallbackRejects(t *testing.T) {
	h := newHarness(t)
	_, challenge := pkce()

	// No session.
	if res := h.browser().get(CallbackPath + "?code=admin-code&state=x"); res.status != http.StatusBadRequest {
		t.Errorf("no session: %d", res.status)
	}
	// Wrong state: the attempt is used up.
	b := h.browser()
	toHA := b.get(authorizeQuery(challenge, nil))
	if res := b.get(CallbackPath + "?code=admin-code&state=wrong"); res.status != http.StatusBadRequest {
		t.Errorf("wrong state: %d", res.status)
	}
	if res := b.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("right state after a wrong one: %d", res.status)
	}
	// State replayed.
	b = h.browser()
	toHA = b.get(authorizeQuery(challenge, nil))
	b.signIn(toHA, "admin-code")
	if res := b.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("replayed state: %d", res.status)
	}
	// Home Assistant refuses the code.
	b = h.browser()
	if res := b.signIn(b.get(authorizeQuery(challenge, nil)), "bad-code"); res.status != http.StatusBadGateway {
		t.Errorf("sign-in failed: %d", res.status)
	}
	// Repeated parameters.
	b = h.browser()
	toHA = b.get(authorizeQuery(challenge, nil))
	u, _ := url.Parse(toHA.location)
	if res := b.get(CallbackPath + "?code=admin-code&state=" + url.QueryEscape(u.Query().Get("state")) + "&state=x"); res.status != http.StatusBadRequest {
		t.Errorf("repeated state: %d", res.status)
	}
	// An expired session.
	b = h.browser()
	toHA = b.get(authorizeQuery(challenge, nil))
	h.clock.Add(sessionTTL)
	if res := b.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("expired session: %d", res.status)
	}
}

// The session cookie changes at sign-in; a cookie planted before is worth nothing.
func TestSessionFixation(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	_, challenge := pkce()
	toHA := b.get(authorizeQuery(challenge, nil))
	srvURL, _ := url.Parse(h.srv.URL)
	before := b.c.Jar.Cookies(srvURL)
	b.signIn(toHA, "admin-code")
	after := b.c.Jar.Cookies(srvURL)
	if len(before) != 1 || len(after) != 1 || before[0].Value == after[0].Value || before[0].Name != "__Host-hm_session" {
		t.Fatalf("cookies before %v, after %v", before, after)
	}
	attacker := h.browser()
	attacker.c.Jar.SetCookies(srvURL, before)
	if res := attacker.get(ConsentPath); res.status != http.StatusBadRequest {
		t.Errorf("planted cookie: %d", res.status)
	}
}

// Negative catalog (UI): request without CSRF token → rejected.
func TestConsentRejects(t *testing.T) {
	h := newHarness(t)
	_, challenge := pkce()
	form := func(csrf, name, template string, action ...string) url.Values {
		f := url.Values{"name": {name}, "template": {template}}
		if csrf != "" {
			f.Set("csrf", csrf)
		}
		if len(action) > 0 {
			f.Set("action", action[0])
		}
		return f
	}
	for name, tc := range map[string]struct {
		form   func(csrf string) url.Values
		header []string
		status int
	}{
		"no csrf":      {func(string) url.Values { return form("", "x", "voice-assistant", "approve") }, nil, http.StatusForbidden},
		"wrong csrf":   {func(string) url.Values { return form("x", "x", "voice-assistant", "approve") }, nil, http.StatusForbidden},
		"cross origin": {func(c string) url.Values { return form(c, "x", "voice-assistant", "approve") }, []string{"Origin", "https://evil.example.org"}, http.StatusForbidden},
		"null origin":  {func(c string) url.Values { return form(c, "x", voiceChoice, "approve") }, []string{"Origin", "null"}, http.StatusForbidden},
		"bad name":     {func(c string) url.Values { return form(c, "bad\u202ename", "voice-assistant", "approve") }, nil, http.StatusBadRequest},
		"empty name":   {func(c string) url.Values { return form(c, " ", "voice-assistant", "approve") }, nil, http.StatusBadRequest},
		"no template":  {func(c string) url.Values { return form(c, "x", "none", "approve") }, nil, http.StatusBadRequest},
		"no action":    {func(c string) url.Values { return form(c, "x", "voice-assistant") }, nil, http.StatusBadRequest},
		"no digest":    {func(c string) url.Values { return form(c, "x", "voice-assistant", "approve") }, nil, http.StatusBadRequest},
	} {
		b := h.browser()
		page := b.consentAs(challenge, "admin-code")
		res := b.post(ConsentPath, tc.form(csrfOf(t, page.body)), tc.header...)
		if res.status != tc.status || res.location != "" {
			t.Errorf("%s: %d %q", name, res.status, res.location)
		}
	}
	// Deny goes back to the agent with access_denied.
	b := h.browser()
	page := b.consentAs(challenge, "admin-code")
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, page.body)}, "action": {"deny"}})
	if u, _ := url.Parse(res.location); res.status != http.StatusSeeOther || u.Query().Get("error") != "access_denied" {
		t.Errorf("deny = %d %q", res.status, res.location)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents", n)
	}
}

func TestConsentWithoutTemplates(t *testing.T) {
	h := newHarness(t)
	if err := h.adm.RemoveTemplate(context.Background(), "voice-assistant", audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hm-read-only", "hm-light-climate", "hm-voice-cautious"} {
		if err := h.adm.SetHidden(context.Background(), name, true, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
			t.Fatal(err)
		}
	}
	_, challenge := pkce()
	if res := h.browser().consentAs(challenge, "admin-code"); res.status != http.StatusConflict {
		t.Errorf("consent page = %d", res.status)
	}
}

// E2E scenario 7 at unit level: no admission during the emergency stop.
func TestEmergencyStopBlocksAdmission(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	verifier, challenge := pkce()
	code := b.approve(b.consentAs(challenge, "admin-code"))
	if _, err := h.agents.SetEmergencyStop(context.Background(), true, audit.Actor{Kind: audit.ActorUser, ID: "u-admin"}); err != nil {
		t.Fatal(err)
	}
	if status, out, _ := h.tokenRequest(exchangeForm(code, verifier)); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("exchange during stop = %d %v", status, out)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents", n)
	}
}

func TestPagesFollowTheBrowserLanguage(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+ConsentPath, nil)
	req.Header.Set("Accept-Language", "fr-FR, de-DE;q=0.8, en;q=0.5")
	res := h.browser().do(req)
	if !strings.Contains(res.body, `lang="de"`) || !strings.Contains(res.body, "abgelaufen") {
		t.Errorf("page:\n%s", res.body)
	}
	if res := h.browser().get(ConsentPath); !strings.Contains(res.body, `lang="en"`) {
		t.Errorf("default language:\n%s", res.body)
	}
}

// Unauthenticated requests cannot fill the session store: one sender gets at most
// maxSessionsPerSender sessions, all together at most maxSessions.
func TestTooManySignInsInProgress(t *testing.T) {
	h := newHarness(t)
	_, challenge := pkce()
	for range maxSessionsPerSender {
		h.browser().get(authorizeQuery(challenge, nil))
	}
	if res := h.browser().get(PairPath); res.status != http.StatusServiceUnavailable {
		t.Errorf("one sender over its limit: status %d", res.status)
	}
	senders := 0
	h.server.clientAddr = func(*http.Request) string { senders++; return fmt.Sprint("10.0.", senders/250, ".", senders%250) }
	for range maxSessions - maxSessionsPerSender {
		h.browser().get(authorizeQuery(challenge, nil))
	}
	if res := h.browser().get(authorizeQuery(challenge, nil)); res.status != http.StatusServiceUnavailable {
		t.Errorf("status %d", res.status)
	}
	if res := h.browser().get(PairPath); res.status != http.StatusServiceUnavailable {
		t.Errorf("pair: status %d", res.status)
	}
	h.clock.Add(sessionTTL)
	if res := h.browser().get(authorizeQuery(challenge, nil)); res.status != http.StatusFound {
		t.Errorf("after expiry: status %d", res.status)
	}
}

func TestErrorsReachTheAgentWithoutDetails(t *testing.T) {
	h := newHarness(t)
	h.server.cfg.Tokens = failingTokens{}
	status, out, _ := h.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}, "client_id": {testClient}})
	if status != http.StatusInternalServerError || len(out) != 1 || out["error"] != "server_error" {
		t.Errorf("%d %v", status, out)
	}
}

type failingTokens struct{}

func (failingTokens) Refresh(context.Context, string, string, string) (agent.TokenPair, error) {
	return agent.TokenPair{}, errors.New("database is locked")
}

// Two decisions posted at the same time in one session admit one agent.
func TestConcurrentConsentDecidesOnce(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	_, challenge := pkce()
	page := b.consentAs(challenge, "admin-code")
	form := url.Values{"csrf": {csrfOf(t, page.body)}, "action": {"approve"}, "name": {"x"}, "template": {voiceChoice}}
	var wg sync.WaitGroup
	results := make([]response, 8)
	for i := range results {
		wg.Go(func() { results[i] = b.post(ConsentPath, form) })
	}
	wg.Wait()
	codes := 0
	for _, res := range results {
		if strings.Contains(res.location, "code=") {
			codes++
		}
	}
	if codes != 1 {
		t.Errorf("%d codes issued", codes)
	}
}

// The consent page says in plain words what each template allows, offers no hidden base
// template and refuses one named anyway.
func TestConsentShowsTemplatesInPlainWords(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.adm.SetHidden(ctx, "hm-light-climate", true, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce()
	b := h.browser()
	page := b.consentAs(challenge, "admin-code")
	for _, want := range []string{"Voice assistant (cautious)", "Built in", "Asks you first", "Locks: unlock, open",
		"Never", "Cameras: everything", "Everything else is forbidden.", `value="hm-read-only@sha256:`, `aria-describedby="template-0-details" required checked`, `value="` + voiceChoice + `"`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("consent page lacks %q", want)
		}
	}
	if strings.Contains(page.body, `value="hm-light-climate"`) {
		t.Error("a hidden base template is offered")
	}
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, page.body)}, "action": {"approve"}, "name": {"Claude"}, "template": {"hm-light-climate"}})
	if res.status != http.StatusBadRequest {
		t.Errorf("approve with a hidden template = %d", res.status)
	}
}

// Names and device IDs of templates are text, never markup, and a template granting
// critical actions without approval is not offered here (it needs the UI's separate
// confirmation).
func TestConsentEscapesAndLeavesOutCriticalTemplates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	local := audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}
	doc := mustDocument(t, h, "voice-assistant")
	evil := strings.Replace(string(doc), `{"any":true}`, `{"entity_id":"light.<script>x</script>"}`, 1)
	if err := h.adm.PutTemplate(ctx, "evil", []byte(evil), local); err != nil {
		t.Fatal(err)
	}
	critical := strings.Replace(string(doc), `"rules":[`, `"rules":[{"id":"r-door","resource":{"category":"lock"},"actions":["unlock"],"decision":"allow","allow_critical":true},`, 1)
	if err := h.adm.PutTemplate(ctx, "doors", []byte(critical), local); err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce()
	page := h.browser().consentAs(challenge, "admin-code")
	if strings.Contains(page.body, "<script>") || !strings.Contains(page.body, "light.&lt;script&gt;x&lt;/script&gt;") {
		t.Error("an entity ID was not escaped")
	}
	if strings.Contains(page.body, `value="doors@`) {
		t.Error("a template granting critical actions without approval is offered")
	}
}
