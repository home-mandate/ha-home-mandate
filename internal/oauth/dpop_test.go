// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/dpop"
	"github.com/home-mandate/ha-home-mandate/internal/dpop/dpoptest"
)

// dpopClient is a CIMD client whose metadata ask for bound tokens.
const dpopClient = "https://dpop.example.org/client.json"

func newDPoPHarness(t *testing.T, mode dpop.Mode) *harness {
	t.Helper()
	h := newHarness(t)
	h.server.setDPoPMode(mode)
	return h
}

func newKey(t *testing.T, alg string) *dpoptest.Key {
	t.Helper()
	k, err := dpoptest.NewKey(alg)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// proof is a proof of k for the token endpoint, with a valid nonce unless noNonce.
func (h *harness) proof(k *dpoptest.Key, noNonce bool) string {
	h.t.Helper()
	c := dpoptest.Claims{Method: http.MethodPost, URL: testPublicURL + TokenPath, IssuedAt: h.clock.Now()}
	if !noNonce {
		c.Nonce = h.server.dpop.Nonce()
	}
	p, err := k.Proof(c)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

// tokenRequestWith posts form to the token endpoint with a DPoP header, if proof is set.
func (h *harness) tokenRequestWith(form url.Values, proof string) (int, map[string]any, http.Header) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+TokenPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if proof != "" {
		req.Header.Set(dpop.HeaderName, proof)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

// codeFor runs the browser part of the code flow for client, with edit on the
// authorization request, and returns the code and its verifier.
func (h *harness) codeFor(client string, edit func(url.Values)) (code, verifier string) {
	h.t.Helper()
	b := h.browser()
	verifier, challenge := pkce()
	back := b.signIn(b.get(authorizeQuery(challenge, func(q url.Values) {
		q.Set("client_id", client)
		if edit != nil {
			edit(q)
		}
	})), "admin-code")
	if back.status != http.StatusSeeOther || back.location != ConsentPath {
		h.t.Fatalf("callback = %d %q\n%s", back.status, back.location, back.body)
	}
	return b.approve(b.get(ConsentPath)), verifier
}

func exchangeFor(client, code, verifier string) url.Values {
	f := exchangeForm(code, verifier)
	f.Set("client_id", client)
	return f
}

func refreshForm(token, client string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {client}}
}

func TestMetadataAdvertisesDPoPByMode(t *testing.T) {
	for mode, want := range map[dpop.Mode]struct{ offered, required bool }{
		dpop.ModeOff: {false, false}, dpop.ModeOptional: {true, false}, dpop.ModeRequired: {true, true},
	} {
		h := newDPoPHarness(t, mode)
		b := h.browser()
		for _, path := range []string{MetadataPath, ResourceMetadataPath} {
			var md map[string]any
			_ = json.Unmarshal([]byte(b.get(path).body), &md)
			algs, offered := md["dpop_signing_alg_values_supported"].([]any)
			if offered != want.offered || offered && (len(algs) != 2 || algs[0] != "ES256" || algs[1] != "EdDSA") {
				t.Errorf("%s %s: algorithms %v", mode, path, md["dpop_signing_alg_values_supported"])
			}
			if path == ResourceMetadataPath && (md["dpop_bound_access_tokens_required"] == true) != want.required {
				t.Errorf("%s: required = %v", mode, md["dpop_bound_access_tokens_required"])
			}
		}
	}
}

func TestCodeFlowWithDPoP(t *testing.T) {
	h := newDPoPHarness(t, dpop.ModeOptional)
	ctx := context.Background()
	key, other := newKey(t, dpoptest.ES256), newKey(t, dpoptest.EdDSA)
	code, verifier := h.codeFor(testClient, nil)
	status, tokens, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), h.proof(key, false))
	if status != http.StatusOK || tokens["token_type"] != "DPoP" {
		t.Fatalf("exchange = %d %v", status, tokens)
	}
	a, err := h.agents.Authenticate(ctx, tokens["access_token"].(string), testResource, key.JKT())
	if err != nil || a.Binding != agent.BindingDPoP {
		t.Fatalf("bound token with its key: %+v, %v", a, err)
	}
	if _, err := h.agents.Authenticate(ctx, tokens["access_token"].(string), testResource, ""); err == nil {
		t.Error("bound token accepted as Bearer")
	}
	rt := tokens["refresh_token"].(string)
	for name, proof := range map[string]string{"without a proof": "", "with another key": h.proof(other, false)} {
		if status, out, _ := h.tokenRequestWith(refreshForm(rt, testClient), proof); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
			t.Errorf("refresh %s = %d %v", name, status, out)
		}
	}
	status, next, _ := h.tokenRequestWith(refreshForm(rt, testClient), h.proof(key, false))
	if status != http.StatusOK || next["token_type"] != "DPoP" {
		t.Errorf("refresh with the key = %d %v", status, next)
	}
	if out := h.auditLog(); strings.Count(out, `"error":"invalid_dpop_proof"`) != 2 {
		t.Errorf("audit log:\n%s", out)
	}
}

func TestTokenEndpointAsksForANonce(t *testing.T) {
	h := newDPoPHarness(t, dpop.ModeOptional)
	key := newKey(t, dpoptest.EdDSA)
	code, verifier := h.codeFor(testClient, nil)
	status, out, header := h.tokenRequestWith(exchangeFor(testClient, code, verifier), h.proof(key, true))
	nonce := header.Get(dpop.NonceHeader)
	if status != http.StatusBadRequest || out["error"] != "use_dpop_nonce" || nonce == "" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("without a nonce = %d %v %v", status, out, header)
	}
	// The code survives; the client retries with the nonce it was given.
	p, err := key.Proof(dpoptest.Claims{Method: http.MethodPost, URL: testPublicURL + TokenPath, Nonce: nonce, IssuedAt: h.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), p); status != http.StatusOK || out["token_type"] != "DPoP" {
		t.Errorf("with the nonce = %d %v", status, out)
	}
}

func TestTokenEndpointRefusesInvalidProofs(t *testing.T) {
	h := newDPoPHarness(t, dpop.ModeOptional)
	key := newKey(t, dpoptest.ES256)
	code, verifier := h.codeFor(testClient, nil)
	wrongURL, _ := key.Proof(dpoptest.Claims{Method: http.MethodPost, URL: testPublicURL + "/mcp", Nonce: h.server.dpop.Nonce(), IssuedAt: h.clock.Now()})
	if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), wrongURL); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
		t.Errorf("proof for another URL = %d %v", status, out)
	}
	good := h.proof(key, false)
	if status, _, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), good); status != http.StatusOK {
		t.Fatalf("code did not survive the invalid proof: %d", status)
	}
	// The same proof again is a replay, before the grant is even looked at.
	if status, out, _ := h.tokenRequestWith(refreshForm("hmr_x", testClient), good); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
		t.Errorf("replayed proof = %d %v", status, out)
	}
}

// With DPoP off, new agents get Bearer tokens even if their client sends proofs.
func TestDPoPOffKeepsNewAgentsUnbound(t *testing.T) {
	h := newDPoPHarness(t, dpop.ModeOff)
	key := newKey(t, dpoptest.ES256)
	code, verifier := h.codeFor(testClient, nil)
	status, tokens, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), h.proof(key, false))
	if status != http.StatusOK || tokens["token_type"] != "Bearer" {
		t.Fatalf("exchange = %d %v", status, tokens)
	}
	if _, err := h.agents.Authenticate(context.Background(), tokens["access_token"].(string), testResource, ""); err != nil {
		t.Errorf("Bearer token: %v", err)
	}
}

func TestBearerRefusedWhereBindingIsRequired(t *testing.T) {
	// Mode required: no admission without a proof; the code is used up, nobody admitted.
	h := newDPoPHarness(t, dpop.ModeRequired)
	code, verifier := h.codeFor(testClient, nil)
	if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), ""); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
		t.Errorf("mode required without a proof = %d %v", status, out)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents admitted", n)
	}
	// A client whose metadata ask for bound tokens, in mode optional.
	h = newDPoPHarness(t, dpop.ModeOptional)
	code, verifier = h.codeFor(dpopClient, nil)
	if status, out, _ := h.tokenRequestWith(exchangeFor(dpopClient, code, verifier), ""); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
		t.Errorf("client asking for bound tokens = %d %v", status, out)
	}
	// An agent admitted with Bearer tokens loses them at refresh once binding is required.
	h = newDPoPHarness(t, dpop.ModeOptional)
	code, verifier = h.codeFor(testClient, nil)
	_, tokens, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), "")
	h.server.setDPoPMode(dpop.ModeRequired)
	if status, out, _ := h.tokenRequestWith(refreshForm(tokens["refresh_token"].(string), testClient), ""); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
		t.Errorf("Bearer refresh in mode required = %d %v", status, out)
	}
}

// RFC 9449 section 10: dpop_jkt binds the code to a key.
func TestAuthorizationCodeBoundByDPoPJKT(t *testing.T) {
	h := newDPoPHarness(t, dpop.ModeOptional)
	key, other := newKey(t, dpoptest.ES256), newKey(t, dpoptest.ES256)
	withJKT := func(q url.Values) { q.Set("dpop_jkt", key.JKT()) }
	// A refused exchange uses the code up, like any failed one.
	for name, proof := range map[string]string{"no proof": "", "another key": h.proof(other, false)} {
		code, verifier := h.codeFor(testClient, withJKT)
		if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), proof); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
			t.Errorf("%s = %d %v", name, status, out)
		}
	}
	code, verifier := h.codeFor(testClient, withJKT)
	if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), h.proof(key, false)); status != http.StatusOK || out["token_type"] != "DPoP" {
		t.Errorf("the key = %d %v", status, out)
	}
	b := h.browser()
	_, challenge := pkce()
	res := b.get(authorizeQuery(challenge, func(q url.Values) { q.Set("dpop_jkt", "short") }))
	if res.status != http.StatusFound || !strings.Contains(res.location, "error=invalid_request") {
		t.Errorf("malformed dpop_jkt = %d %q", res.status, res.location)
	}
	// With DPoP off, dpop_jkt is ignored (RFC 9449: a server without DPoP ignores it).
	h = newDPoPHarness(t, dpop.ModeOff)
	code, verifier = h.codeFor(testClient, func(q url.Values) { q.Set("dpop_jkt", "short") })
	if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), ""); status != http.StatusOK || out["token_type"] != "Bearer" {
		t.Errorf("mode off = %d %v", status, out)
	}
}

// A pairing admits the agent at the human's approval; its tokens are bound when the
// agent fetches them with a proof.
func TestDeviceFlowBindsAtThePoll(t *testing.T) {
	for _, tc := range []struct {
		mode      dpop.Mode
		withProof bool
		status    int
		tokenType string
	}{
		{dpop.ModeOptional, true, http.StatusOK, "DPoP"},
		{dpop.ModeOptional, false, http.StatusOK, "Bearer"},
		{dpop.ModeRequired, false, http.StatusBadRequest, ""},
	} {
		h := newDPoPHarness(t, tc.mode)
		key := newKey(t, dpoptest.EdDSA)
		a := h.device("n8n-kitchen")
		b, page := h.pairBrowser("admin-code")
		consent := b.get(b.enterCode(page, a.UserCode).location)
		if res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen"},
			"template": {voiceChoice}}); res.status != http.StatusOK {
			t.Fatalf("approve = %d\n%s", res.status, res.body)
		}
		h.clock.Add(deviceInterval * 3)
		proof := ""
		if tc.withProof {
			proof = h.proof(key, false)
		}
		status, out, _ := h.tokenRequestWith(url.Values{"grant_type": {deviceGrantID}, "device_code": {a.DeviceCode}, "client_id": {"n8n-kitchen"}}, proof)
		if status != tc.status || out["token_type"] != nil && out["token_type"] != tc.tokenType {
			t.Errorf("%s, proof %v: poll = %d %v", tc.mode, tc.withProof, status, out)
		}
		if status != http.StatusOK && (out["error"] != "invalid_dpop_proof" || !strings.Contains(h.auditLog(), `"error":"dpop_required"`)) {
			t.Errorf("%s: refusal %v\n%s", tc.mode, out, h.auditLog())
		}
	}
}

// A code bound by dpop_jkt keeps that binding even if DPoP was switched off between the
// authorization request and the exchange (review of the branch).
func TestCodeBoundByDPoPJKTStaysBoundWhenDPoPIsSwitchedOff(t *testing.T) {
	h := newDPoPHarness(t, dpop.ModeOptional)
	key := newKey(t, dpoptest.EdDSA)
	code, verifier := h.codeFor(testClient, func(q url.Values) { q.Set("dpop_jkt", key.JKT()) })
	h.server.setDPoPMode(dpop.ModeOff)
	if status, out, _ := h.tokenRequestWith(exchangeFor(testClient, code, verifier), h.proof(key, false)); status != http.StatusOK || out["token_type"] != "DPoP" {
		t.Errorf("exchange after switching off = %d %v", status, out)
	}
}

// The token endpoint records a proof's jti before the grant is checked; its request limit
// must keep the replay cache from filling within the time a proof counts (2 × Window).
func TestTokenLimitCannotFillTheReplayCache(t *testing.T) {
	perWindow := tokenGlobal * int((2*dpop.Window+rateWindow-1)/rateWindow)
	if perWindow >= dpop.ReplayEntries {
		t.Errorf("%d token requests fit into the time a proof counts, the replay cache holds %d", perWindow, dpop.ReplayEntries)
	}
}
