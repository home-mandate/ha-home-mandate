// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/ha-home-mandate/internal/dpop/dpoptest"
)

// dpopTokenRequest posts form to the token endpoint with a proof of key and follows one
// use_dpop_nonce with the nonce it was given, as a DPoP client does.
func dpopTokenRequest(t *testing.T, key *dpoptest.Key, form url.Values) (int, map[string]any) {
	t.Helper()
	nonce := ""
	for {
		proof, err := key.Proof(dpoptest.Claims{Method: http.MethodPost, URL: env.public + "/oauth/token", Nonce: nonce, IssuedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, env.public+"/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("DPoP", proof)
		resp, err := oauthClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode == http.StatusBadRequest && out["error"] == "use_dpop_nonce" && nonce == "" {
			if nonce = resp.Header.Get("DPoP-Nonce"); nonce == "" {
				t.Fatal("use_dpop_nonce without a DPoP-Nonce header")
			}
			continue
		}
		return resp.StatusCode, out
	}
}

// dpopSession is an MCP session whose every request carries the token in the DPoP scheme
// and a fresh proof of key.
func dpopSession(t *testing.T, key *dpoptest.Key, token string) *sdk.ClientSession {
	t.Helper()
	base := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: env.roots, MinVersion: tls.VersionTLS13}}
	client := sdk.NewClient(&sdk.Implementation{Name: "e2e-dpop-agent", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: env.mcpURL,
		HTTPClient: &http.Client{Transport: dpoptest.Transport{Key: key, Token: token, Base: base}, Timeout: 30 * time.Second}}, nil)
	if err != nil {
		t.Fatalf("connect with DPoP: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// tokensOf takes a successful token response apart and keeps its secrets out of the logs.
func tokensOf(t *testing.T, status int, out map[string]any, tokenType string) (access, refresh string) {
	t.Helper()
	access, _ = out["access_token"].(string)
	refresh, _ = out["refresh_token"].(string)
	env.secrets = append(env.secrets, access, refresh)
	if status != http.StatusOK || out["token_type"] != tokenType || access == "" || refresh == "" {
		t.Fatalf("token response = %d %v, want token_type %s", status, out["error"], tokenType)
	}
	return access, refresh
}

// dpopTokens asks for bound tokens with a proof of key and returns them.
func dpopTokens(t *testing.T, key *dpoptest.Key, form url.Values) (access, refresh string) {
	t.Helper()
	status, out := dpopTokenRequest(t, key, form)
	return tokensOf(t, status, out, "DPoP")
}

func pollForm(p pairing) url.Values {
	return url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {p.DeviceCode}, "client_id": {e2eClient}}
}

func refreshFormOf(token string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {e2eClient}}
}

func bindingOf(t *testing.T, ui *uiClient, name string) string {
	t.Helper()
	var agents []struct {
		DisplayName string `json:"display_name"`
		Binding     string `json:"binding"`
	}
	ui.ok(http.MethodGet, "api/agents", nil, &agents)
	for _, a := range agents {
		if a.DisplayName == name {
			return a.Binding
		}
	}
	t.Fatalf("no agent %q in the UI", name)
	return ""
}

// reconnectAs gives the agent clientID new tokens through the pairing p, chosen by
// admin-approver on the consent page.
func reconnectAs(t *testing.T, p pairing, clientID string) {
	t.Helper()
	b := newBrowser(t)
	if back := b.signIn(b.get("/pair"), adminApprover); back.status != http.StatusSeeOther {
		t.Fatalf("sign-in = %d", back.status)
	}
	entry := b.get("/pair")
	if res := b.post("/pair", url.Values{"csrf": {b.csrf(entry)}, "code": {p.UserCode}}); res.status != http.StatusSeeOther {
		t.Fatalf("code entry = %d\n%s", res.status, res.body)
	}
	consent := b.get("/oauth/consent")
	if !strings.Contains(consent.body, clientID) {
		t.Fatalf("the consent page does not offer to reconnect %s", clientID)
	}
	if res := b.post("/oauth/consent", url.Values{"csrf": {b.csrf(consent)}, "action": {"reconnect"}, "agent": {clientID}}); res.status != http.StatusOK {
		t.Fatalf("reconnect = %d\n%s", res.status, res.body)
	}
}

// Issue #67, DPoP part (#62–#65 against the release image and a real Home Assistant): a
// pairing with DPoP gives bound tokens, every MCP request carries a proof, and the bound
// agent is refused Bearer at /mcp, at refresh and at reconnect; a Bearer agent next to it
// keeps working and shows as unbound.
func TestDPoPBoundAgent(t *testing.T) {
	key, err := dpoptest.NewKey(dpoptest.ES256)
	if err != nil {
		t.Fatal(err)
	}
	template := voiceTemplate(t, nil)
	p := requestPairing(t)
	if res := pairAs(t, p, adminApprover, "DPoP agent", template); res.status != http.StatusOK {
		t.Fatalf("admission = %d\n%s", res.status, res.body)
	}
	access, refresh := dpopTokens(t, key, pollForm(p))
	s := dpopSession(t, key, access)
	ready(t, s)
	if _, msg := call(t, s, "list_devices", map[string]any{}); msg != "" {
		t.Fatalf("list_devices with DPoP: %q", msg)
	}

	// No Bearer for the bound agent: at /mcp and at refresh.
	if status := statusWith(t, access); status != http.StatusUnauthorized {
		t.Errorf("bound token as Bearer at /mcp: %d", status)
	}
	resp, err := oauthClient().PostForm(env.public+"/oauth/token", refreshFormOf(refresh))
	if err != nil {
		t.Fatal(err)
	}
	var refused map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&refused)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || refused["error"] != "invalid_dpop_proof" {
		t.Errorf("refresh without a proof = %d %v", resp.StatusCode, refused)
	}
	access, _ = dpopTokens(t, key, refreshFormOf(refresh))
	if _, msg := call(t, dpopSession(t, key, access), "list_devices", map[string]any{}); msg != "" {
		t.Errorf("after the refresh: %q", msg)
	}

	// A Bearer agent next to it keeps working and shows as unbound.
	bearerToken := newAgent(t, "Bearer next to DPoP", nil)
	if _, msg := call(t, session(t, bearerToken), "list_devices", map[string]any{}); msg != "" {
		t.Errorf("Bearer agent: %q", msg)
	}
	ui := uiLogin(t, adminApprover)
	if got := bindingOf(t, ui, "DPoP agent"); got != "dpop" {
		t.Errorf("DPoP agent shown as %q", got)
	}
	if got := bindingOf(t, ui, "Bearer next to DPoP"); got != "none" {
		t.Errorf("Bearer agent shown as %q", got)
	}

	// The client lost its key: after all tokens are withdrawn, a reconnect without a proof
	// is refused, and one with a new key binds anew.
	clientID := clientIDOf(t, "DPoP agent")
	cli(t, "", "emergency-stop", "on", "--revoke-tokens")
	cli(t, "", "emergency-stop", "off")
	p = requestPairing(t)
	reconnectAs(t, p, clientID)
	if status, out := pollTokens(t, p); status != http.StatusBadRequest || out["error"] != "invalid_dpop_proof" {
		t.Errorf("reconnect without a proof = %d %v", status, out["error"])
	}
	newKey, err := dpoptest.NewKey(dpoptest.EdDSA)
	if err != nil {
		t.Fatal(err)
	}
	p = requestPairing(t)
	reconnectAs(t, p, clientID)
	access, _ = dpopTokens(t, newKey, pollForm(p))
	if _, msg := call(t, dpopSession(t, newKey, access), "list_devices", map[string]any{}); msg != "" {
		t.Errorf("after the reconnect with a new key: %q", msg)
	}
	log := auditLog(t)
	if !strings.Contains(log, `"error":"invalid_dpop_proof"`) || !strings.Contains(log, `"error":"dpop_required"`) {
		t.Error("DPoP refusals not in the audit log")
	}
}
