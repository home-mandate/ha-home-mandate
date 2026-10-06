// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/audit"
)

type deviceAnswer struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
}

func (h *harness) requestDevice(form url.Values) (int, deviceAnswer) {
	h.t.Helper()
	resp, err := h.srv.Client().PostForm(h.srv.URL+DevicePath, form)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var a deviceAnswer
	_ = json.NewDecoder(resp.Body).Decode(&a)
	return resp.StatusCode, a
}

func (h *harness) device(clientID string) deviceAnswer {
	h.t.Helper()
	status, a := h.requestDevice(url.Values{"client_id": {clientID}})
	if status != http.StatusOK {
		h.t.Fatalf("device authorization = %d %+v", status, a)
	}
	return a
}

func (h *harness) poll(a deviceAnswer, clientID string) (int, map[string]any) {
	h.t.Helper()
	status, out, _ := h.tokenRequest(url.Values{"grant_type": {deviceGrantID}, "device_code": {a.DeviceCode}, "client_id": {clientID}})
	return status, out
}

// pairBrowser signs in for pairing and returns the browser on the code entry page.
func (h *harness) pairBrowser(haCode string) (*browser, response) {
	h.t.Helper()
	b := h.browser()
	back := b.signIn(b.get(PairPath), haCode)
	if back.status != http.StatusSeeOther || back.location != PairPath {
		h.t.Fatalf("callback = %d %q", back.status, back.location)
	}
	return b, b.get(PairPath)
}

func (b *browser) enterCode(page response, code string) response {
	b.h.t.Helper()
	return b.post(PairPath, url.Values{"csrf": {csrfOf(b.h.t, page.body)}, "code": {code}})
}

// E2E scenario 1 at unit level: pair an agent via code.
func TestDeviceFlow(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	if !strings.HasPrefix(a.DeviceCode, "hmd_") || len(a.UserCode) != 9 || a.UserCode[4] != '-' ||
		a.VerificationURI != testPublicURL+PairPath || a.ExpiresIn != 600 || a.Interval != 5 {
		t.Fatalf("answer = %+v", a)
	}
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "authorization_pending" {
		t.Errorf("first poll = %d %v", status, out)
	}
	if status, out := h.poll(a, "n8n-kitchen"); out["error"] != "slow_down" || status != http.StatusBadRequest {
		t.Errorf("fast poll = %d %v", status, out)
	}

	b, page := h.pairBrowser("admin-code")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Markus") {
		t.Fatalf("pair page = %d\n%s", page.status, page.body)
	}
	// Typed in lower case with a space instead of the dash.
	res := b.enterCode(page, strings.ToLower(strings.Replace(a.UserCode, "-", " ", 1)))
	if res.status != http.StatusSeeOther || res.location != ConsentPath {
		t.Fatalf("code entry = %d %q\n%s", res.status, res.location, res.body)
	}
	consent := b.get(ConsentPath)
	// A free identifier is shown as unverified; there is no redirect.
	if consent.status != http.StatusOK || !strings.Contains(consent.body, "not checked: n8n-kitchen") ||
		strings.Contains(consent.header.Get("Content-Security-Policy"), "http") {
		t.Fatalf("consent = %d %v\n%s", consent.status, consent.header, consent.body)
	}
	res = b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen n8n"},
		"template": {voiceChoice}})
	if res.status != http.StatusOK || !strings.Contains(res.body, "admitted") {
		t.Fatalf("approve = %d\n%s", res.status, res.body)
	}
	h.clock.Add(deviceInterval * 3)
	status, tokens := h.poll(a, "n8n-kitchen")
	if status != http.StatusOK || tokens["access_token"] == nil {
		t.Fatalf("poll after approval = %d %v", status, tokens)
	}
	got, err := h.agents.Authenticate(context.Background(), tokens["access_token"].(string), testResource)
	if err != nil || got.DisplayName != "Kitchen n8n" || got.OAuthClient != "n8n-kitchen" || got.ClientVerified {
		t.Errorf("agent = %+v, %v", got, err)
	}
	// The device code is used up, and so is the user code.
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("poll after tokens = %d %v", status, out)
	}
	b2, page2 := h.pairBrowser("admin-code")
	if res := b2.enterCode(page2, a.UserCode); res.status != http.StatusBadRequest {
		t.Errorf("used user code = %d", res.status)
	}
}

func TestDeviceFlowDenied(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	b.enterCode(page, a.UserCode)
	consent := b.get(ConsentPath)
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"deny"}})
	if res.status != http.StatusOK || !strings.Contains(res.body, "not admitted") {
		t.Errorf("deny = %d\n%s", res.status, res.body)
	}
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "access_denied" {
		t.Errorf("poll = %d %v", status, out)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents", n)
	}
}

// Negative catalog: pairing code expired.
func TestDeviceCodeExpires(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	res := b.enterCode(page, a.UserCode)
	if res.status != http.StatusSeeOther {
		t.Fatalf("code entry = %d", res.status)
	}
	h.clock.Add(deviceTTL)
	// Approval after expiry does not count.
	if res := b.get(ConsentPath); res.status != http.StatusBadRequest {
		t.Errorf("consent after expiry = %d", res.status)
	}
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "expired_token" {
		t.Errorf("poll = %d %v", status, out)
	}
}

func TestApprovalAfterExpiryIsRefused(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	b.enterCode(page, a.UserCode)
	consent := b.get(ConsentPath)
	csrf := csrfOf(t, consent.body)
	// The grant expires while the human looks at the consent page; the session is
	// younger and still valid.
	h.server.mu.Lock()
	for _, g := range h.server.grants {
		g.expires = h.clock.Now()
	}
	h.server.mu.Unlock()
	res := b.post(ConsentPath, url.Values{"csrf": {csrf}, "action": {"approve"}, "name": {"x"}, "template": {voiceChoice}})
	if res.status == http.StatusOK {
		t.Errorf("approve after expiry = %d", res.status)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents", n)
	}
}

// Negative catalog: pairing code wrong, used more than once, brute force → locked after
// n attempts, per session and across sessions.
func TestPairingBruteForce(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	for i := range pairSessionMax {
		res := b.enterCode(page, "BBBB-BBBB")
		want := http.StatusBadRequest
		if i == pairSessionMax-1 {
			want = http.StatusTooManyRequests
		}
		if res.status != want {
			t.Fatalf("attempt %d = %d", i+1, res.status)
		}
	}
	// The session is locked even for the right code.
	if res := b.enterCode(page, a.UserCode); res.status != http.StatusTooManyRequests {
		t.Errorf("right code in a locked session = %d", res.status)
	}
	if res := b.get(PairPath); res.status != http.StatusTooManyRequests {
		t.Errorf("pair page of a locked session = %d", res.status)
	}
	if out := h.auditLog(); strings.Count(out, `"error":"pairing_code_invalid"`) != pairSessionMax {
		t.Errorf("audit entries = %d", strings.Count(out, `"error":"pairing_code_invalid"`))
	}
	// Across sessions: after pairGlobalMax failures nobody can pair for a while.
	for range pairGlobalMax/pairSessionMax - 1 {
		b, page := h.pairBrowser("admin-code")
		for range pairSessionMax {
			b.enterCode(page, "not a code")
		}
	}
	if _, page := h.pairBrowser("admin-code"); page.status != http.StatusTooManyRequests {
		t.Errorf("fresh session during the global lock = %d", page.status)
	}
	h.clock.Add(pairWindow)
	b3, page3 := h.pairBrowser("admin-code")
	if res := b3.enterCode(page3, a.UserCode); res.status == http.StatusTooManyRequests {
		t.Error("still locked after the window")
	}
}

func TestPairRejects(t *testing.T) {
	h := newHarness(t)
	h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	if res := b.post(PairPath, url.Values{"code": {"BBBB-BBBB"}}); res.status != http.StatusForbidden {
		t.Errorf("no csrf = %d", res.status)
	}
	if res := b.post(PairPath, url.Values{"csrf": {csrfOf(t, page.body)}, "code": {"x"}}, "Origin", "https://evil.example.org"); res.status != http.StatusForbidden {
		t.Errorf("cross origin = %d", res.status)
	}
	// A session started for an authorization request cannot enter codes.
	other := h.browser()
	_, challenge := pkce()
	other.consentAs(challenge, "admin-code")
	if res := other.post(PairPath, url.Values{"csrf": {"x"}, "code": {"BBBB-BBBB"}}); res.status != http.StatusForbidden {
		t.Errorf("authorize session = %d", res.status)
	}
	// A non-admin never gets to the code entry.
	plain := h.browser()
	if res := plain.signIn(plain.get(PairPath), "plain-code"); res.status != http.StatusForbidden {
		t.Errorf("non-admin = %d", res.status)
	}
}

func TestDeviceAuthorizationRejects(t *testing.T) {
	h := newHarness(t)
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"unknown client": {url.Values{"client_id": {"https://evil.example.org/c.json"}}, "invalid_client"},
		"no client":      {url.Values{}, "invalid_client"},
		"other resource": {url.Values{"client_id": {"n8n-kitchen"}, "resource": {"https://x.example.org/mcp"}}, "invalid_target"},
		"repeated":       {url.Values{"client_id": {"n8n-kitchen", "n8n-kitchen"}}, "invalid_request"},
	} {
		if status, a := h.requestDevice(tc.form); status != http.StatusBadRequest || a.Error != tc.want {
			t.Errorf("%s: %d %+v", name, status, a)
		}
	}
	a := h.device(testClient)
	for name, form := range map[string]url.Values{
		"other client":   {"grant_type": {deviceGrantID}, "device_code": {a.DeviceCode}, "client_id": {"n8n-kitchen"}},
		"unknown code":   {"grant_type": {deviceGrantID}, "device_code": {"hmd_x"}, "client_id": {testClient}},
		"no device code": {"grant_type": {deviceGrantID}, "client_id": {testClient}},
	} {
		if status, out, _ := h.tokenRequest(form); status != http.StatusBadRequest || out["error"] == "authorization_pending" {
			t.Errorf("%s: %d %v", name, status, out)
		}
	}
	// One sender may hold maxGrantsPerHost pending pairings.
	for range maxGrantsPerHost - 1 {
		h.device("n8n-kitchen")
	}
	if status, a := h.requestDevice(url.Values{"client_id": {"n8n-kitchen"}}); status != http.StatusServiceUnavailable || a.Error != "temporarily_unavailable" {
		t.Errorf("over the limit of one sender: %d %+v", status, a)
	}
	// Bounded number of pending pairings overall.
	senders := 0
	h.server.clientAddr = func(*http.Request) string { senders++; return fmt.Sprint("10.0.0.", senders) }
	for range maxGrants - maxGrantsPerHost {
		h.device("n8n-kitchen")
	}
	if status, a := h.requestDevice(url.Values{"client_id": {"n8n-kitchen"}}); status != http.StatusServiceUnavailable || a.Error != "temporarily_unavailable" {
		t.Errorf("over the limit: %d %+v", status, a)
	}
	h.clock.Add(deviceTTL)
	if status, _ := h.requestDevice(url.Values{"client_id": {"n8n-kitchen"}}); status != http.StatusOK {
		t.Errorf("after expiry: %d", status)
	}
}

func TestUserCodes(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c := newUserCode()
		if len(c) != userCodeLen || strings.Trim(c, userCodeAlphabet) != "" {
			t.Fatalf("user code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 199 {
		t.Errorf("only %d distinct codes", len(seen))
	}
	for in, want := range map[string]string{
		"BCDF-GHJK": "BCDFGHJK", "bcdf ghjk": "BCDFGHJK", "BCDFGHJK": "BCDFGHJK",
		"ABCD-EFGH": "", "BCDF-GHJ": "", "BCDF-GHJKL": "", "BCDF_GHJK": "", "": "",
	} {
		if got := normalizeUserCode(in); got != want {
			t.Errorf("normalizeUserCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// The approval admits the agent at once. A server error during it leaves the pairing
// pending: nothing was admitted, the human approves again with the same code, and the
// agent's next poll gets the tokens.
func TestDeviceApprovalAfterAServerError(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	approve := func() response {
		b, page := h.pairBrowser("admin-code")
		b.enterCode(page, a.UserCode)
		consent := b.get(ConsentPath)
		return b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen"}, "template": {voiceChoice}})
	}
	h.server.cfg.Admission = brokenAdmission{Admitter: h.adm, admitErr: errors.New("database is locked")}
	if res := approve(); res.status != http.StatusServiceUnavailable {
		t.Fatalf("approval with a broken database = %d", res.status)
	}
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "authorization_pending" {
		t.Fatalf("poll after the failed approval = %d %v", status, out)
	}
	if n := countAgents(t, h); n != 0 {
		t.Fatalf("%d agents", n)
	}
	h.server.cfg.Admission = h.adm
	if res := approve(); res.status != http.StatusOK {
		t.Fatalf("second approval = %d\n%s", res.status, res.body)
	}
	h.clock.Add(deviceInterval * 3)
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusOK {
		t.Errorf("poll after recovery = %d %v", status, out)
	}
}

// A refused admission (here: the template was deleted after the page was shown) is shown
// to the human; the pairing stays pending.
func TestDeviceApprovalRefused(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	b.enterCode(page, a.UserCode)
	consent := b.get(ConsentPath)
	h.server.cfg.Admission = brokenAdmission{Admitter: h.adm, admitErr: admission.ErrTemplateNotFound}
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen"}, "template": {voiceChoice}})
	if res.status != http.StatusBadRequest {
		t.Errorf("refused admission = %d", res.status)
	}
	if status, out := h.poll(a, "n8n-kitchen"); out["error"] != "authorization_pending" {
		t.Errorf("poll = %d %v", status, out)
	}
}

// The human approves what the page showed: a template changed after the page was shown
// admits nobody, and the pairing stays pending.
func TestApprovalOfATemplateChangedSinceItWasShown(t *testing.T) {
	h := newHarness(t)
	a := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	b.enterCode(page, a.UserCode)
	consent := b.get(ConsentPath)
	_, info, err := h.adm.TemplateDocument(context.Background(), "voice-assistant")
	if err != nil {
		t.Fatal(err)
	}
	broader := []byte(strings.Replace(string(mustDocument(t, h, "voice-assistant")), `"decision":"deny"`, `"decision":"allow"`, 1))
	if err := h.adm.UpdateTemplate(context.Background(), "voice-assistant", broader, info.Digest, true, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
		t.Fatal(err)
	}
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen"}, "template": {voiceChoice}})
	if res.status != http.StatusBadRequest {
		t.Errorf("approval of a changed template = %d", res.status)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents admitted", n)
	}
	// A choice without the digest of what was shown is refused as well (or the session is
	// used up already): never an admission.
	res = b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen"}, "template": {"voice-assistant"}})
	if res.status == http.StatusOK || countAgents(t, h) != 0 {
		t.Errorf("approval without digest = %d", res.status)
	}
}

func mustDocument(t *testing.T, h *harness, name string) []byte {
	t.Helper()
	doc, _, err := h.adm.TemplateDocument(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, doc); err != nil {
		t.Fatal(err)
	}
	return compact.Bytes()
}
