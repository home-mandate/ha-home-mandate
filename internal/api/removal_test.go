// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/oauth"
)

// These tests write mandate.removed, agent.removed and agent.reconnected: they need a
// specification with those events (home-mandate/spec v0.1.0-alpha.3).

func (h *harness) events(after int64) []string {
	h.t.Helper()
	var buf bytes.Buffer
	if err := h.log.Export(context.Background(), &buf); err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var e struct {
			Seq   int64  `json:"seq"`
			Event string `json:"event"`
		}
		_ = json.Unmarshal([]byte(line), &e)
		if e.Seq > after {
			out = append(out, e.Event)
		}
	}
	return out
}

func (h *harness) lastSeq() int64 {
	h.t.Helper()
	var seq int64
	if err := h.st.DB().QueryRow(`SELECT coalesce(max(seq), 0) FROM audit_log`).Scan(&seq); err != nil {
		h.t.Fatal(err)
	}
	return seq
}

func TestRemoveAgent(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	if r := h.do(http.MethodPost, "/api/agents/remove", map[string]any{"client_id": voice.ClientID}); r.errCode() != codeConflict {
		t.Errorf("remove active = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": voice.ClientID}, nil)
	seq := h.lastSeq()
	var a wireAgent
	h.ok(http.MethodPost, "/api/agents/remove", map[string]any{"client_id": voice.ClientID, "mandates": true}, &a)
	if a.RemovedAt == nil || a.RemovedByName == nil || *a.RemovedByName != "Markus" || a.Status != "revoked" || a.Mandate == nil ||
		a.Mandate.RemovedAt == nil {
		t.Errorf("removed agent = %+v, mandate %+v", a, a.Mandate)
	}
	if got := strings.Join(h.events(seq), " "); got != "mandate.removed agent.removed" {
		t.Errorf("events = %s", got)
	}
	// Removed agents stay in the list, marked: the UI hides them unless asked.
	var list []wireAgent
	h.ok(http.MethodGet, "/api/agents", nil, &list)
	if len(list) != 1 || list[0].RemovedAt == nil {
		t.Errorf("agents = %+v", list)
	}
	for _, tc := range []struct {
		body any
		code string
	}{
		{map[string]any{"client_id": "hm-client:none"}, codeNotFound},
		{map[string]any{"client_id": ""}, codeInvalidInput},
		{map[string]any{"client_id": voice.ClientID, "all": true}, codeInvalidInput},
	} {
		if r := h.do(http.MethodPost, "/api/agents/remove", tc.body); r.errCode() != tc.code {
			t.Errorf("%v = %d %s", tc.body, r.code, r.body)
		}
	}
}

// Leftover entries after an emergency stop: revoke and remove in one action, recorded
// as two entries; open requests of the agent end.
func TestRevokeAndRemoveAgent(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	req := lightRequest(adminID)
	req.ClientID = voice.ClientID
	_, done := h.ask(req)
	seq := h.lastSeq()
	var a wireAgent
	h.ok(http.MethodPost, "/api/agents/remove", map[string]any{"client_id": voice.ClientID, "revoke": true}, &a)
	if a.Status != "revoked" || a.RemovedAt == nil || a.Mandate == nil || a.Mandate.Status != "revoked" || a.Mandate.RemovedAt != nil {
		t.Errorf("agent = %+v, mandate %+v", a, a.Mandate)
	}
	if got := strings.Join(h.events(seq), " "); !strings.HasPrefix(got, "agent.revoked mandate.revoked agent.removed") {
		t.Errorf("events = %s", got)
	}
	select {
	case res := <-done:
		if res.Outcome != approval.OutcomeCancelled {
			t.Errorf("open request ended with %s", res.Outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open request of the removed agent not ended")
	}
}

func TestRemoveMandate(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	path := "/api/mandates/" + m.ID + "/remove"
	if r := h.do(http.MethodPost, path, nil); r.errCode() != codeConflict {
		t.Errorf("remove active = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/revoke", nil, nil)
	var sum wireMandateSummary
	h.ok(http.MethodPost, path, nil, &sum)
	if sum.RemovedAt == nil || sum.Status != "revoked" {
		t.Errorf("removed = %+v", sum)
	}
	var list []wireMandateSummary
	h.ok(http.MethodGet, "/api/mandates", nil, &list)
	if len(list) != 1 || list[0].RemovedAt == nil {
		t.Errorf("mandates = %+v", list)
	}
	// Read-only, still: its versions open, nothing can be changed.
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, nil)
	if r := h.do(http.MethodPost, "/api/mandates/m-none-0000/remove", nil); r.errCode() != codeNotFound {
		t.Errorf("unknown = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/mandates/x/remove", nil); r.errCode() != codeNotFound {
		t.Errorf("bad id = %d %s", r.code, r.body)
	}
}

func TestRemoveAllRevoked(t *testing.T) {
	h := newHarness(t)
	active := h.admit("Active")
	gone := h.admit("Gone")
	h.ok(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": gone.ClientID}, nil)
	activeMandate := h.mandateOf(active.ClientID)
	h.ok(http.MethodPost, "/api/mandates/"+activeMandate.ID+"/revoke", nil, nil)
	var out wireRemoved
	h.ok(http.MethodPost, "/api/revoked/remove", nil, &out)
	if out.Agents != 1 || out.Mandates != 2 {
		t.Errorf("removed = %+v", out)
	}
	h.ok(http.MethodPost, "/api/revoked/remove", nil, &out)
	if out.Agents != 0 || out.Mandates != 0 {
		t.Errorf("again = %+v", out)
	}
	if r := h.do(http.MethodPost, "/api/revoked/remove", map[string]any{}); r.errCode() != codeInvalidInput {
		t.Errorf("with a body = %d", r.code)
	}
}

// The audit log shows the names of removed mandates; the version stays resolvable while
// the entries refer to it.
func TestAuditShowsNamesOfRemovedMandates(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	h.ok(http.MethodPost, "/api/agents/remove", map[string]any{"client_id": voice.ClientID, "revoke": true, "mandates": true}, nil)
	var page wireAuditPage
	h.ok(http.MethodGet, "/api/audit?event=mandate.removed", nil, &page)
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %+v", page.Entries)
	}
	m, _ := page.Entries[0]["mandate"].(map[string]any)
	if m["name"] != "Voice" || m["version"] != float64(1) {
		t.Errorf("mandate = %+v", m)
	}
	h.ok(http.MethodGet, "/api/audit?event=agent.removed", nil, &page)
	if len(page.Entries) != 1 {
		t.Errorf("agent.removed entries = %+v", page.Entries)
	}
}

// Reconnecting in the UI with the real authorization server: after an emergency stop the
// pairing check offers the agent; reconnecting gives it new tokens, no new agent.
func TestReconnectInThePairing(t *testing.T) {
	h := newHarness(t)
	as := oauth.New(oauth.Config{PublicURL: "https://hm.example.org", Resource: "https://hm.example.org/mcp",
		Clients: oauth.NewCIMDResolver(nil), Admission: h.adm, Tokens: h.agents, Audit: h.log, Now: h.now.Now})
	h.pairing = as
	h.build()
	device := func() (string, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "https://hm.example.org"+oauth.DevicePath, strings.NewReader(url.Values{"client_id": {"n8n-voice"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		as.Handler().ServeHTTP(rec, req)
		var grant struct {
			DeviceCode string `json:"device_code"`
			UserCode   string `json:"user_code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &grant)
		return grant.DeviceCode, grant.UserCode
	}
	voice := h.admit("Voice") // OAuth client n8n-voice, unverified
	_, code := device()
	var c wirePairingCandidate
	h.ok(http.MethodPost, "/api/pairing/check", map[string]any{"code": code}, &c)
	if len(c.Reconnect) != 0 {
		t.Errorf("offered while the agent has tokens: %+v", c.Reconnect)
	}
	h.ok(http.MethodPut, "/api/emergency-stop", map[string]any{"active": true}, nil)
	h.ok(http.MethodPut, "/api/emergency-stop", map[string]any{"active": false}, nil)
	deviceCode, code := device()
	h.ok(http.MethodPost, "/api/pairing/check", map[string]any{"code": code}, &c)
	if len(c.Reconnect) != 1 || c.Reconnect[0].ClientID != voice.ClientID || c.Reconnect[0].DisplayName != "Voice" ||
		c.Reconnect[0].Mandate == nil || c.Reconnect[0].Mandate.Name != "Voice" || c.Reconnect[0].AdmittedAt == "" {
		t.Fatalf("reconnect = %+v", c.Reconnect)
	}
	for _, bad := range []map[string]any{
		{"code": code, "pairing_id": c.PairingID},
		{"code": code, "pairing_id": c.PairingID, "client_id": strings.Repeat("x", 257)},
		{"code": "", "pairing_id": c.PairingID, "client_id": voice.ClientID},
	} {
		if r := h.do(http.MethodPost, "/api/pairing/reconnect", bad); r.errCode() != codeInvalidInput {
			t.Errorf("%v = %d %s", bad, r.code, r.body)
		}
	}
	if r := h.do(http.MethodPost, "/api/pairing/reconnect", map[string]any{"code": code, "pairing_id": c.PairingID,
		"client_id": "hm-client:other"}); r.errCode() != codeConflict {
		t.Errorf("agent not offered = %d %s", r.code, r.body)
	}
	seq := h.lastSeq()
	var a wireAgent
	h.ok(http.MethodPost, "/api/pairing/reconnect", map[string]any{"code": code, "pairing_id": c.PairingID, "client_id": voice.ClientID}, &a)
	if a.ClientID != voice.ClientID || !a.Connected {
		t.Errorf("reconnected = %+v", a)
	}
	if got := strings.Join(h.events(seq), " "); got != "agent.reconnected" {
		t.Errorf("events = %s", got)
	}
	poll := httptest.NewRecorder()
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {deviceCode}, "client_id": {"n8n-voice"}}
	req := httptest.NewRequest(http.MethodPost, "https://hm.example.org"+oauth.TokenPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	as.Handler().ServeHTTP(poll, req)
	if poll.Code != http.StatusOK {
		t.Errorf("poll = %d %s", poll.Code, poll.Body)
	}
	var list []wireAgent
	h.ok(http.MethodGet, "/api/agents", nil, &list)
	if len(list) != 1 {
		t.Errorf("agents = %+v", list)
	}
	_ = audit.EventAgentReconnected
}
