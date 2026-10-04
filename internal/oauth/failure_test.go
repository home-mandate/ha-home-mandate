// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
)

// brokenAdmission fails the way a broken database would.
type brokenAdmission struct {
	Admitter
	admitErr, listErr error
}

func (b brokenAdmission) Admit(ctx context.Context, req admission.Request) (agent.Agent, agent.TokenPair, error) {
	if b.admitErr != nil {
		return agent.Agent{}, agent.TokenPair{}, b.admitErr
	}
	return b.Admitter.Admit(ctx, req)
}

func (b brokenAdmission) Templates(ctx context.Context) ([]admission.Template, error) {
	if b.listErr != nil {
		return nil, b.listErr
	}
	return b.Admitter.Templates(ctx)
}

type brokenAudit struct{}

func (brokenAudit) Append(context.Context, audit.Entry) (int64, error) {
	return 0, errors.New("disk full")
}

func TestAdmissionFailureIsAServerError(t *testing.T) {
	h := newHarness(t)
	h.server.cfg.Admission = brokenAdmission{Admitter: h.adm, admitErr: errors.New("database is locked")}
	b := h.browser()
	verifier, challenge := pkce()
	code := b.approve(b.consentAs(challenge, "admin-code"))
	if status, out, _ := h.tokenRequest(exchangeForm(code, verifier)); status != http.StatusInternalServerError || out["error"] != "server_error" {
		t.Errorf("exchange = %d %v", status, out)
	}
}

func TestTemplatesUnavailable(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	_, challenge := pkce()
	page := b.consentAs(challenge, "admin-code")
	h.server.cfg.Admission = brokenAdmission{Admitter: h.adm, listErr: errors.New("database is locked")}
	if res := b.get(ConsentPath); res.status != http.StatusServiceUnavailable {
		t.Errorf("consent page = %d", res.status)
	}
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, page.body)}, "action": {"approve"}, "name": {"x"}, "template": {"voice-assistant"}})
	if res.status != http.StatusServiceUnavailable {
		t.Errorf("approve = %d", res.status)
	}
}

func TestTooManyOpenCodes(t *testing.T) {
	h := newHarness(t)
	authz := authzRequest{client: Client{ID: testClient}, redirectURI: testRedirect}
	for range maxCodes {
		if _, err := h.server.issueCode(authz, decision{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.server.issueCode(authz, decision{}); err == nil {
		t.Error("code issued over the limit")
	}
	// The consent sends the agent back with temporarily_unavailable.
	b := h.browser()
	_, challenge := pkce()
	page := b.consentAs(challenge, "admin-code")
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, page.body)}, "action": {"approve"}, "name": {"x"}, "template": {"voice-assistant"}})
	if u, _ := url.Parse(res.location); res.status != http.StatusSeeOther || u.Query().Get("error") != "temporarily_unavailable" {
		t.Errorf("approve = %d %q", res.status, res.location)
	}
	h.clock.Add(codeTTL)
	if _, err := h.server.issueCode(authz, decision{}); err != nil {
		t.Errorf("after expiry: %v", err)
	}
}

func TestOversizedFormIsRejected(t *testing.T) {
	h := newHarness(t)
	status, out, _ := h.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "pad": {strings.Repeat("x", maxFormBytes)}})
	if status != http.StatusBadRequest || out["error"] != "invalid_request" {
		t.Errorf("%d %v", status, out)
	}
}

func TestAuditFailureIsLoggedNotHidden(t *testing.T) {
	h := newHarness(t)
	h.server.cfg.Audit = brokenAudit{}
	b := h.browser()
	_, challenge := pkce()
	// The refusal still happens when the audit log cannot be written.
	if res := b.signIn(b.get(authorizeQuery(challenge, nil)), "plain-code"); res.status != http.StatusForbidden {
		t.Errorf("non-admin = %d", res.status)
	}
}

func TestNewDefaults(t *testing.T) {
	s := New(Config{PublicURL: "http://localhost:8765"})
	if s.cfg.Logger == nil || s.cfg.Now == nil || s.secure || s.cookie != "hm_session" {
		t.Errorf("server = %+v", s)
	}
}

func TestSessionRotationOfAnExpiredSession(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	s := newSessions(clock.Now)
	id, _, err := s.create("10.0.0.1", purposePair, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(sessionTTL)
	if _, ok := s.rotate(id); ok {
		t.Error("expired session rotated")
	}
	if _, ok := s.rotate("unknown"); ok {
		t.Error("unknown session rotated")
	}
	if equalSecret("", "") || equalSecret("a", "") || !equalSecret("a", "a") {
		t.Error("equalSecret")
	}
}

func TestCIMDCacheIsBounded(t *testing.T) {
	r := NewCIMDResolver(nil)
	base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for i := range cimdCacheSize + 5 {
		r.now = func() time.Time { return base.Add(time.Duration(i) * time.Second) }
		r.remember("https://c.example.org/"+strings.Repeat("x", i+1), Client{})
	}
	if len(r.cache) != cimdCacheSize {
		t.Errorf("cache size %d", len(r.cache))
	}
	if _, ok := r.cache["https://c.example.org/x"]; ok {
		t.Error("oldest entry kept")
	}
}

func TestCIMDInputLimits(t *testing.T) {
	r := NewCIMDResolver(nil)
	if _, ok := r.metadataURL("https://c.example.org/" + strings.Repeat("x", maxClientIDLen)); ok {
		t.Error("overlong client ID accepted")
	}
	if err := r.control("tcp", "no-port", nil); err == nil {
		t.Error("address without port accepted")
	}
	for _, doc := range []string{`{"a":}`, `{"a":1,`, `{1:2}`, `{"a":1 "b":2}`} {
		if err := uniqueTopLevelKeys([]byte(doc)); err == nil {
			t.Errorf("%s accepted", doc)
		}
	}
}

func TestSignInWithUnreachableHomeAssistant(t *testing.T) {
	f := newFakeHAAuth(t)
	s := newSignIn(t, f, nil)
	f.srv.Close()
	if _, err := s.SignIn(context.Background(), "code"); !errors.Is(err, ErrSignInFailed) {
		t.Errorf("SignIn = %v", err)
	}
}

// Metadata fetches are bounded; more at the same time are refused, not queued.
func TestCIMDFetchesAreBounded(t *testing.T) {
	c, r := newCIMDServer(t)
	id := c.json("/client.json", goodDoc)
	for range cimdParallel {
		r.fetching <- struct{}{}
	}
	if _, err := r.Resolve(context.Background(), id); !errors.Is(err, ErrInvalidClient) {
		t.Errorf("Resolve while busy = %v", err)
	}
	for range cimdParallel {
		<-r.fetching
	}
	if _, err := r.Resolve(context.Background(), id); err != nil {
		t.Errorf("Resolve after = %v", err)
	}
}
