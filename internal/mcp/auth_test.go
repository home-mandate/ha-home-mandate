// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/home-mandate/internal/agent"
)

func (h *harness) post(authorization string) *http.Response {
	h.t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// requestFor is a tool request authenticated as a.
func requestFor(a agent.Agent) *sdk.CallToolRequest {
	return &sdk.CallToolRequest{Extra: &sdk.RequestExtra{TokenInfo: &sdkauth.TokenInfo{UserID: a.ClientID, Extra: map[string]any{"agent": a}}}}
}

// Negative catalog: a token issued for another resource is refused like any other
// invalid token, and the 401 tells the client where the resource metadata is.
func TestTokenForAnotherResourceIsRejected(t *testing.T) {
	h := newHarness(t, nil)
	p, err := h.agents.IssueTokens(context.Background(), h.agent.ClientID, "https://other.example.org/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp := h.post("Bearer " + p.AccessToken)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `resource_metadata="`+testMetadataURL+`"`) {
		t.Errorf("WWW-Authenticate = %q", got)
	}
	if resp := h.post("Bearer " + h.token); resp.StatusCode != http.StatusOK {
		t.Errorf("valid token: status %d", resp.StatusCode)
	}
}

// Invalid tokens are logged as auth.rejected, at most once per minute.
func TestRejectedTokensAreLoggedThrottled(t *testing.T) {
	h := newHarness(t, nil)
	for range 5 {
		h.post("Bearer hma_" + strings.Repeat("A", 43))
	}
	out := h.auditLog()
	if n := strings.Count(out, `"event":"auth.rejected"`); n != 1 {
		t.Errorf("auth.rejected entries = %d, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, `"error":"invalid_token"`) || strings.Contains(out, strings.Repeat("A", 43)) {
		t.Errorf("audit log:\n%s", out)
	}
}

// A request authenticated a moment before the emergency stop is still refused by the
// PEP and logged; nothing reaches Home Assistant. A new request is refused at the token.
func TestEmergencyStopIsEnforcedByThePEP(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	if _, err := h.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('emergency_stop', 'on', '2026-10-13T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	g := New(Config{Agents: h.agents, PDP: h.pdp, Catalog: h.catalog, HA: h.ha, Audit: h.log})
	if _, err := g.enforce(context.Background(), h.agent, "light.kitchen", "turn_on"); err == nil || err.Error() != "denied: emergency_stop" {
		t.Errorf("enforce = %v", err)
	}
	if e := h.lastEntry(); path(e, "result", "denied_by") != "emergency_stop" || e["evaluation"] != nil {
		t.Errorf("audit entry = %v", e)
	}
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText == "" {
		t.Error("perform_action served during the stop")
	}
	if calls := h.ha.recorded(); len(calls) != 0 {
		t.Errorf("Home Assistant called: %v", calls)
	}
}

// Lists are refused during the stop, and an unreadable stop counts as active.
func TestEmergencyStopRefusesLists(t *testing.T) {
	h := newHarness(t, nil)
	g := New(Config{Agents: unreadableStop{h.agents}, PDP: h.pdp, Catalog: h.catalog, HA: h.ha, Audit: h.log})
	if _, err := g.listSnapshot(context.Background(), requestFor(h.agent)); err == nil || err.Error() != "denied: emergency_stop" {
		t.Errorf("listSnapshot = %v", err)
	}
}

type unreadableStop struct{ Authenticator }

func (unreadableStop) EmergencyStopActive(context.Context) (bool, error) {
	return false, context.DeadlineExceeded
}
