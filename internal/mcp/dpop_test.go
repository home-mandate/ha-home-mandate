// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/dpop"
	"github.com/home-mandate/ha-home-mandate/internal/dpop/dpoptest"
)

func dpopHarness(t *testing.T, mode dpop.Mode) *harness {
	t.Helper()
	h := newHarness(t, nil)
	h.dpopMode = mode
	h.url = h.serve(h.log)
	return h
}

func newKey(t *testing.T) *dpoptest.Key {
	t.Helper()
	k, err := dpoptest.NewKey(dpoptest.ES256)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// boundToken issues a pair bound to key and returns its access token.
func (h *harness) boundToken(key *dpoptest.Key) string {
	h.t.Helper()
	ctx := context.Background()
	p, err := h.agents.IssueTokens(ctx, h.agent.ClientID, testResource)
	if err != nil {
		h.t.Fatal(err)
	}
	if p, err = h.agents.Bind(ctx, p, agent.Holder{JKT: key.JKT(), Bind: true}); err != nil {
		h.t.Fatal(err)
	}
	return p.AccessToken
}

// proof is a proof of key for a POST to the endpoint with ath of token; at the public URL,
// never the address the test server listens on.
func (h *harness) proof(key *dpoptest.Key, token string) string {
	h.t.Helper()
	p, err := key.Proof(dpoptest.Claims{Method: http.MethodPost, URL: testResource, AccessToken: token, IssuedAt: h.now.Now()})
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

// postWith posts tools/list with an Authorization header and, if set, a DPoP header and
// an MCP-Protocol-Version.
func (h *harness) postWith(authorization, proof, version string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if proof != "" {
		req.Header.Set(dpop.HeaderName, proof)
	}
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// challenges checks the two challenges of a 401: Bearer with the resource metadata, and
// DPoP with the algorithms and, if set, an error.
func challenges(t *testing.T, name string, resp *http.Response, dpopError string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		return
	}
	all := strings.Join(resp.Header.Values("WWW-Authenticate"), "\n")
	want := `DPoP algs="ES256 EdDSA"`
	if dpopError != "" {
		want = `DPoP error="` + dpopError + `", algs="ES256 EdDSA"`
	}
	if !strings.Contains(all, `Bearer resource_metadata="`+testMetadataURL+`"`) || !strings.Contains(all, want) {
		t.Errorf("%s: WWW-Authenticate = %q, want %s", name, all, want)
	}
}

func TestBoundTokensNeedAProofOfTheirKey(t *testing.T) {
	h := dpopHarness(t, dpop.ModeOptional)
	key, other := newKey(t), newKey(t)
	token := h.boundToken(key)
	good := h.proof(key, token)
	if resp := h.postWith("DPoP "+token, good, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("DPoP with a valid proof: %d", resp.StatusCode)
	}
	challenges(t, "replayed proof", h.postWith("DPoP "+token, good, ""), "invalid_dpop_proof")
	noATH, _ := key.Proof(dpoptest.Claims{Method: http.MethodPost, URL: testResource, IssuedAt: h.now.Now()})
	otherHost, _ := key.Proof(dpoptest.Claims{Method: http.MethodPost, URL: h.url, AccessToken: token, IssuedAt: h.now.Now()})
	get, _ := key.Proof(dpoptest.Claims{Method: http.MethodGet, URL: testResource, AccessToken: token, IssuedAt: h.now.Now()})
	for name, tc := range map[string]struct{ authorization, proof, dpopError string }{
		"bound token as Bearer":       {"Bearer " + token, "", ""},
		"bound token as Bearer+proof": {"Bearer " + token, h.proof(key, token), ""},
		"no proof":                    {"DPoP " + token, "", "invalid_dpop_proof"},
		"proof without ath":           {"DPoP " + token, noATH, "invalid_dpop_proof"},
		"proof of another key":        {"DPoP " + token, h.proof(other, token), "invalid_token"},
		"proof for another URL":       {"DPoP " + token, otherHost, "invalid_dpop_proof"},
		"proof for another method":    {"DPoP " + token, get, "invalid_dpop_proof"},
		"ath of another token":        {"DPoP " + token, h.proof(key, "hma_other"), "invalid_dpop_proof"},
		"scheme without token":        {"DPoP", h.proof(key, ""), ""},
	} {
		challenges(t, name, h.postWith(tc.authorization, tc.proof, ""), tc.dpopError)
	}
	if out := h.auditLog(); !strings.Contains(out, `"error":"invalid_dpop_proof"`) {
		t.Errorf("audit log:\n%s", out)
	}
}

func TestUnboundTokensStayBearer(t *testing.T) {
	h := dpopHarness(t, dpop.ModeOptional)
	key := newKey(t)
	if resp := h.postWith("Bearer "+h.token, "", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("Bearer: %d", resp.StatusCode)
	}
	// An unbound token presented with the DPoP scheme is refused (RFC 9449 section 7.1).
	challenges(t, "unbound token as DPoP", h.postWith("DPoP "+h.token, h.proof(key, h.token), ""), "invalid_token")
	if resp := h.postWith("bearer "+h.token, "", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("lower-case scheme: %d", resp.StatusCode)
	}
}

// The challenge a TypeScript client refreshes on (issue #61): an expired bound token.
func TestExpiredBoundTokenGetsTheDPoPChallenge(t *testing.T) {
	h := dpopHarness(t, dpop.ModeOptional)
	key := newKey(t)
	token := h.boundToken(key)
	h.agents.SetClock(func() time.Time { return time.Now().Add(agent.AccessTokenTTL + time.Minute) })
	challenges(t, "expired", h.postWith("DPoP "+token, h.proof(key, token), ""), "invalid_token")
}

func TestModeRequiredRefusesBearerAtTheEndpoint(t *testing.T) {
	h := dpopHarness(t, dpop.ModeRequired)
	challenges(t, "Bearer in mode required", h.postWith("Bearer "+h.token, "", ""), "")
	key := newKey(t)
	token := h.boundToken(key)
	if resp := h.postWith("DPoP "+token, h.proof(key, token), ""); resp.StatusCode != http.StatusOK {
		t.Errorf("DPoP in mode required: %d", resp.StatusCode)
	}
}

func TestChallengesFollowTheMode(t *testing.T) {
	h := dpopHarness(t, dpop.ModeOff)
	resp := h.postWith("", "", "")
	if all := strings.Join(resp.Header.Values("WWW-Authenticate"), "\n"); resp.StatusCode != http.StatusUnauthorized ||
		strings.Contains(all, "DPoP") || !strings.Contains(all, "Bearer") {
		t.Errorf("mode off: %d %q", resp.StatusCode, all)
	}
	// A bound agent is still answered in DPoP while DPoP is off.
	key := newKey(t)
	token := h.boundToken(key)
	if resp := h.postWith("DPoP "+token, h.proof(key, token), ""); resp.StatusCode != http.StatusOK {
		t.Errorf("bound token in mode off: %d", resp.StatusCode)
	}
	challenges(t, "bound token in mode off without proof", h.postWith("DPoP "+token, "", ""), "invalid_dpop_proof")
	challenges(t, "mode optional", dpopHarness(t, dpop.ModeOptional).postWith("", "", ""), "")
}

// Issue #64: once MCP makes DPoP mandatory for a protocol version, a release lists it,
// and clients speaking it get no Bearer access.
func TestProtocolVersionsThatRequireDPoP(t *testing.T) {
	if len(dpopRequiredProtocols) != 0 {
		t.Fatalf("protocol versions requiring DPoP: %v", dpopRequiredProtocols)
	}
	dpopRequiredProtocols["2099-01-01"] = true
	t.Cleanup(func() { delete(dpopRequiredProtocols, "2099-01-01") })
	h := dpopHarness(t, dpop.ModeOptional)
	challenges(t, "Bearer with that version", h.postWith("Bearer "+h.token, "", "2099-01-01"), "")
	if resp := h.postWith("Bearer "+h.token, "", "2025-11-25"); resp.StatusCode != http.StatusOK {
		t.Errorf("Bearer with another version: %d", resp.StatusCode)
	}
}

// Refusals are logged at most once a minute per kind: invalid tokens cannot hide a proof
// that was refused (review of the branch).
func TestRejectionsAreThrottledPerKind(t *testing.T) {
	h := dpopHarness(t, dpop.ModeOptional)
	key := newKey(t)
	token := h.boundToken(key)
	for range 3 {
		h.postWith("Bearer hma_"+strings.Repeat("A", 43), "", "")
	}
	h.postWith("DPoP "+token, "", "")
	out := h.auditLog()
	if strings.Count(out, `"error":"invalid_token"`) != 1 || strings.Count(out, `"error":"invalid_dpop_proof"`) != 1 {
		t.Errorf("audit log:\n%s", out)
	}
}

func TestChallengeWriterPassesFlushesOn(t *testing.T) {
	rec := httptest.NewRecorder()
	(&challengeWriter{ResponseWriter: rec}).Flush()
	if !rec.Flushed {
		t.Error("flush not passed on")
	}
}
