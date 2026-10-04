// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/oauth"
)

func decisionAt(clientID, name string, withEvaluation bool) audit.Entry {
	e := audit.Entry{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: clientID, DisplayName: name},
		Request: &audit.Request{Time: testStart, Resource: audit.Resource{EntityID: "light.kitchen", Area: "kitchen"}, Action: "turn_on"},
		Result:  &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByRateLimit}}
	if withEvaluation {
		e.Evaluation = &audit.Evaluation{Decision: "deny", Reason: "no_match"}
		e.Result = &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}
	}
	return e
}

func TestAgents(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	voice := h.admit("Voice")
	// Activity: the household day starts at midnight in Europe/Berlin (22:00 UTC the day
	// before); the hour counts only requests that went through the rate limit.
	h.log.SetClock(func() time.Time { return time.Date(2026, 10, 2, 21, 30, 0, 0, time.UTC) }) // yesterday, Berlin
	_, _ = h.log.Append(ctx, decisionAt(voice.ClientID, "Voice", true))
	h.log.SetClock(func() time.Time { return time.Date(2026, 10, 2, 22, 30, 0, 0, time.UTC) }) // today 00:30, Berlin
	_, _ = h.log.Append(ctx, decisionAt(voice.ClientID, "Voice", true))
	h.log.SetClock(func() time.Time { return testStart.Add(-10 * time.Minute) })
	_, _ = h.log.Append(ctx, decisionAt(voice.ClientID, "Voice", true))
	_, _ = h.log.Append(ctx, decisionAt(voice.ClientID, "Voice", false))
	var list []wireAgent
	h.ok(http.MethodGet, "/api/agents", nil, &list)
	if len(list) != 1 {
		t.Fatalf("agents = %+v", list)
	}
	a := list[0]
	if a.ClientID != voice.ClientID || a.Status != "active" || a.CreatedByName == nil || *a.CreatedByName != "Markus" ||
		a.OAuthClient != "n8n-voice" || a.ClientVerified || a.RedirectURIs == nil || len(a.RedirectURIs) != 0 ||
		a.RequestsToday != 3 || a.ActionsLastHour != 1 || a.LastActiveAt == nil || *a.LastActiveAt != "2026-10-03T09:50:00.000Z" ||
		a.RevokedAt != nil || a.Mandate == nil || a.Mandate.Status != "active" || a.Mandate.Name != "voice-assistant" ||
		*a.Mandate.MaxActionsPerHour != 60 || !strings.HasPrefix(a.Mandate.Digest, "sha256:") {
		t.Errorf("agent = %+v, mandate %+v", a, a.Mandate)
	}
	// In UTC (no time zone yet) the day starts at 00:00 UTC: only two requests today.
	h.status.TimeZone = ""
	h.ok(http.MethodGet, "/api/agents", nil, &list)
	if list[0].RequestsToday != 2 {
		t.Errorf("requests today in UTC = %d", list[0].RequestsToday)
	}
	h.status.TimeZone = "Not/AZone"
	h.ok(http.MethodGet, "/api/agents", nil, &list)
	if list[0].RequestsToday != 2 {
		t.Errorf("requests today with an unknown zone = %d", list[0].RequestsToday)
	}
}

// Negative catalog, UI: agent, tokens, mandate and pending approvals end in one
// transaction; a repeated revoke answers like the first.
func TestRevokeAgent(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	other := h.admit("Other")
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	req := lightRequest(adminID)
	req.ClientID = voice.ClientID
	_, done := h.ask(req)
	var a wireAgent
	h.ok(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": voice.ClientID}, &a)
	if a.Status != "revoked" || a.RevokedAt == nil || a.RevokedByName == nil || *a.RevokedByName != "Markus" ||
		a.Mandate == nil || a.Mandate.Status != "revoked" {
		t.Errorf("revoked agent = %+v", a)
	}
	select {
	case res := <-done:
		if res.Outcome != approval.OutcomeCancelled {
			t.Errorf("open request ended with %s", res.Outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open request of the revoked agent not ended")
	}
	if m := h.mandateOf(other.ClientID); m.Status != mandate.StatusActive {
		t.Error("another agent's mandate was revoked")
	}
	var again wireAgent
	h.ok(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": voice.ClientID}, &again)
	if again.Status != "revoked" || *again.RevokedAt != *a.RevokedAt {
		t.Errorf("second revoke = %+v", again)
	}
	if r := h.do(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": "hm-client:none"}); r.errCode() != codeNotFound {
		t.Errorf("unknown agent = %d", r.code)
	}
	for _, bad := range []string{"", strings.Repeat("x", 257)} {
		if r := h.do(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": bad}); r.field() != "/client_id" {
			t.Errorf("client_id %q = %d %s", bad, r.code, r.body)
		}
	}
	// A failing transaction revokes nothing: the agent stays active.
	if _, err := h.st.DB().Exec(`CREATE TRIGGER no_revoke BEFORE UPDATE ON mandates BEGIN SELECT RAISE(ABORT, 'x'); END`); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": other.ClientID}); r.code != http.StatusInternalServerError {
		t.Errorf("failed revoke = %d", r.code)
	}
	if got, _ := h.agents.Get(context.Background(), other.ClientID); got.Status != agent.StatusActive {
		t.Error("agent revoked although its mandate could not be")
	}
}

func TestDevices(t *testing.T) {
	h := newHarness(t)
	var c wireDeviceCatalog
	h.ok(http.MethodGet, "/api/devices", nil, &c)
	if len(c.Areas) != 2 || c.Areas[0] != (wireArea{ID: "hall", Name: "Flur"}) || len(c.Devices) != 4 {
		t.Fatalf("catalog = %+v", c)
	}
	byID := map[string]wireDevice{}
	for _, d := range c.Devices {
		byID[d.EntityID] = d
	}
	if d := byID["light.kitchen"]; d.Name != "Küchenlicht" || d.Category != "light" || *d.Area != "kitchen" ||
		strings.Join(d.Actions, ",") != "read,set,turn_off,turn_on" {
		t.Errorf("light = %+v", d)
	}
	if d := byID["sensor.outside"]; d.Name != "sensor.outside" || d.Area != nil || strings.Join(d.Actions, ",") != "read" {
		t.Errorf("sensor = %+v", d)
	}
	if d := byID["number.wallbox"]; d.Category != "other" || strings.Join(d.Actions, ",") != "read,set" {
		t.Errorf("other = %+v", d)
	}
	h.cat.devices = append(h.cat.devices, h.cat.devices[0])
	h.cat.devices[len(h.cat.devices)-1].Category = "paperless:document"
	h.ok(http.MethodGet, "/api/devices", nil, &c)
	if last := c.Devices[len(c.Devices)-1]; last.Actions == nil || len(last.Actions) != 0 {
		t.Errorf("unknown category actions = %#v", last.Actions)
	}
}

// fakePairing returns err for every call, or a candidate and an agent.
type fakePairing struct {
	err      error
	calls    []string
	approved oauth.PairingApproval
	agent    agent.Agent
}

func (f *fakePairing) Check(_ context.Context, session, code string) (oauth.PairingCandidate, error) {
	f.calls = append(f.calls, "check:"+session+":"+code)
	return oauth.PairingCandidate{PairingID: "p1", ClaimedName: "Küchen-Tablet", Client: "kitchen-tablet", RequestedAt: testStart,
		ExpiresAt: testStart.Add(10 * time.Minute), RequestedFrom: "192.168.1.42"}, f.err
}

func (f *fakePairing) Approve(_ context.Context, session string, a oauth.PairingApproval) (agent.Agent, error) {
	f.calls = append(f.calls, "approve:"+session)
	f.approved = a
	return f.agent, f.err
}

func (f *fakePairing) Deny(_ context.Context, session, code, id string) error {
	f.calls = append(f.calls, "deny:"+session+":"+code+":"+id)
	return f.err
}

func TestPairingWithoutOAuth(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/pairing/check", "/api/pairing/approve", "/api/pairing/deny"} {
		if r := h.do(http.MethodPost, path, map[string]any{"code": "BCDF-GHJK"}); r.errCode() != codeUnavailable {
			t.Errorf("%s = %d", path, r.code)
		}
	}
}

func TestPairingHandlers(t *testing.T) {
	h := newHarness(t)
	f := &fakePairing{agent: h.admit("Tablet")}
	h.pairing = f
	h.build()
	var c wirePairingCandidate
	h.ok(http.MethodPost, "/api/pairing/check", map[string]any{"code": "bcdf ghjk"}, &c)
	if c.PairingID != "p1" || c.RequestedFrom != "192.168.1.42" || c.ExpiresAt != "2026-10-03T10:10:00.000Z" || f.calls[0] != "check:"+adminID+":bcdf ghjk" {
		t.Errorf("candidate = %+v, calls %v", c, f.calls)
	}
	var a wireAgent
	h.ok(http.MethodPost, "/api/pairing/approve", map[string]any{"code": "BCDF-GHJK", "pairing_id": "p1", "display_name": "  Tablet  ",
		"template": "voice-assistant", "mandate_name": "Küche", "confirm_critical": true}, &a)
	if a.DisplayName != "Tablet" || f.approved.DisplayName != "Tablet" || f.approved.MandateName != "Küche" || !f.approved.ConfirmCritical ||
		f.approved.By != adminID || f.approved.PairingID != "p1" {
		t.Errorf("approve = %+v, %+v", a, f.approved)
	}
	h.ok(http.MethodPost, "/api/pairing/approve", map[string]any{"code": "BCDF-GHJK", "pairing_id": "p1", "display_name": "Tablet",
		"template": "voice-assistant", "mandate_name": "  "}, nil)
	if f.approved.MandateName != "" {
		t.Errorf("blank mandate name = %q", f.approved.MandateName)
	}
	h.ok(http.MethodPost, "/api/pairing/deny", map[string]any{"code": "BCDF-GHJK", "pairing_id": "p1"}, nil)
	for _, tc := range []struct {
		path  string
		body  map[string]any
		field string
	}{
		{"/api/pairing/check", map[string]any{"code": ""}, "/code"},
		{"/api/pairing/check", map[string]any{"code": strings.Repeat("B", 33)}, "/code"},
		{"/api/pairing/deny", map[string]any{"code": "x", "pairing_id": ""}, "/pairing_id"},
		{"/api/pairing/deny", map[string]any{"code": "", "pairing_id": "p1"}, "/code"},
		{"/api/pairing/approve", map[string]any{"code": "x", "pairing_id": strings.Repeat("p", 65), "display_name": "A", "template": "t"}, "/pairing_id"},
		{"/api/pairing/approve", map[string]any{"code": "x", "pairing_id": "p1", "display_name": "\u3164", "template": "t"}, "/display_name"},
		{"/api/pairing/approve", map[string]any{"code": "x", "pairing_id": "p1", "display_name": strings.Repeat("a", 81), "template": "t"}, "/display_name"},
		{"/api/pairing/approve", map[string]any{"code": "x", "pairing_id": "p1", "display_name": "A", "template": ""}, "/template"},
		{"/api/pairing/approve", map[string]any{"code": "x", "pairing_id": "p1", "display_name": "A", "template": "t", "mandate_name": "a\u202eb"}, "/mandate_name"},
	} {
		if r := h.do(http.MethodPost, tc.path, tc.body); r.errCode() != codeInvalidInput || r.field() != tc.field {
			t.Errorf("%s %v = %d %s", tc.path, tc.body, r.code, r.body)
		}
	}
	// Errors of internal/oauth and the admission, as the UI knows them.
	for _, tc := range []struct {
		err         error
		status      int
		code, field string
		retryAfter  string
	}{
		{&oauth.PairingLockedError{RetryAfter: 90500 * time.Millisecond}, http.StatusTooManyRequests, codePairingLocked, "", "91"},
		{oauth.ErrPairingInvalid, http.StatusBadRequest, codePairingInvalid, "", ""},
		{oauth.ErrPairingExpired, http.StatusGone, codePairingExpired, "", ""},
		{oauth.ErrPairingConflict, http.StatusConflict, codeConflict, "", ""},
		{fmt.Errorf("%w: %w", oauth.ErrPairingAdmission, mandate.ErrCriticalConfirmation), http.StatusUnprocessableEntity, codeCriticalConfirm, "", ""},
		{fmt.Errorf("%w: %w", oauth.ErrPairingAdmission, admission.ErrTemplateNotFound), http.StatusBadRequest, codeInvalidInput, "/template", ""},
		{fmt.Errorf("%w: %w", oauth.ErrPairingAdmission, agent.ErrInvalidName), http.StatusBadRequest, codeInvalidInput, "/display_name", ""},
		{fmt.Errorf("%w: %w", oauth.ErrPairingAdmission, agent.ErrEmergencyStop), http.StatusConflict, codeConflict, "", ""},
		{fmt.Errorf("%w: %w", oauth.ErrPairingAdmission, mandate.ErrConflict), http.StatusConflict, codeConflict, "", ""},
		{fmt.Errorf("%w: other", oauth.ErrPairingAdmission), http.StatusBadRequest, codeInvalidInput, "", ""},
		{fmt.Errorf("%w: %w", oauth.ErrPairingAdmission, mandate.ErrInvalid), http.StatusUnprocessableEntity, codeInvalidMandate, "/template", ""},
		{fmt.Errorf("%w: x", oauth.ErrPairingUnavailable), http.StatusServiceUnavailable, codeUnavailable, "", ""},
		{errors.New("boom"), http.StatusInternalServerError, codeInternal, "", ""},
	} {
		f.err = tc.err
		r := h.do(http.MethodPost, "/api/pairing/approve", map[string]any{"code": "BCDF-GHJK", "pairing_id": "p1", "display_name": "A", "template": "t"})
		if r.code != tc.status || r.errCode() != tc.code || r.field() != tc.field || r.header.Get("Retry-After") != tc.retryAfter {
			t.Errorf("%v = %d %s %v", tc.err, r.code, r.body, r.header.Get("Retry-After"))
		}
		if r := h.do(http.MethodPost, "/api/pairing/check", map[string]any{"code": "BCDF-GHJK"}); r.code != tc.status {
			t.Errorf("check with %v = %d", tc.err, r.code)
		}
		if r := h.do(http.MethodPost, "/api/pairing/deny", map[string]any{"code": "BCDF-GHJK", "pairing_id": "p1"}); r.code != tc.status {
			t.Errorf("deny with %v = %d", tc.err, r.code)
		}
	}
}

// The whole pairing with the real authorization server: an agent asks for a code, the
// administrator checks and approves it in the UI, the agent's poll gets its tokens.
func TestPairingWithTheAuthorizationServer(t *testing.T) {
	h := newHarness(t)
	as := oauth.New(oauth.Config{PublicURL: "https://hm.example.org", Resource: "https://hm.example.org/mcp",
		Clients: oauth.NewCIMDResolver(nil), Admission: h.adm, Tokens: h.agents, Audit: h.log, Now: h.now.Now})
	h.pairing = as
	h.build()
	device := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://hm.example.org"+oauth.DevicePath, strings.NewReader(url.Values{"client_id": {"kitchen-tablet"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.168.1.42:51000"
	as.Handler().ServeHTTP(device, req)
	var grant struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.Unmarshal(device.Body.Bytes(), &grant); err != nil || grant.UserCode == "" {
		t.Fatalf("device authorization = %d %s", device.Code, device.Body)
	}
	var c wirePairingCandidate
	h.ok(http.MethodPost, "/api/pairing/check", map[string]any{"code": grant.UserCode}, &c)
	if c.Client != "kitchen-tablet" || c.RequestedFrom != "192.168.1.42" || c.ClientVerified {
		t.Errorf("candidate = %+v", c)
	}
	var a wireAgent
	h.ok(http.MethodPost, "/api/pairing/approve", map[string]any{"code": grant.UserCode, "pairing_id": c.PairingID,
		"display_name": "Küchen-Tablet", "template": "voice-assistant", "mandate_name": "Küche"}, &a)
	if a.Status != "active" || a.Mandate == nil || a.Mandate.Name != "Küche" || a.CreatedBy != adminID {
		t.Errorf("admitted = %+v", a)
	}
	poll := httptest.NewRecorder()
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {grant.DeviceCode}, "client_id": {"kitchen-tablet"}}
	req = httptest.NewRequest(http.MethodPost, "https://hm.example.org"+oauth.TokenPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	as.Handler().ServeHTTP(poll, req)
	if poll.Code != http.StatusOK || !strings.Contains(poll.Body.String(), "access_token") {
		t.Errorf("poll = %d %s", poll.Code, poll.Body)
	}
	// Wrong codes count for this user: five lock the session, with the real remaining time.
	for range 5 {
		_ = h.do(http.MethodPost, "/api/pairing/check", map[string]any{"code": "BBBB-BBBB"})
	}
	r := h.do(http.MethodPost, "/api/pairing/check", map[string]any{"code": "BBBB-BBBB"})
	if r.errCode() != codePairingLocked || r.header.Get("Retry-After") != "600" {
		t.Errorf("locked = %d %s %v", r.code, r.body, r.header)
	}
}

// fakeMarks records what the API marks as critical.
type fakeMarks struct {
	set map[string]string // entity → user
	err error
}

func (m *fakeMarks) Set(_ context.Context, entityID string, critical bool, by string) error {
	if m.err != nil {
		return m.err
	}
	if critical {
		m.set[entityID] = by
	} else {
		delete(m.set, entityID)
	}
	return nil
}

// An administrator marks a device as critical: every action on it except read then
// needs a confirmation or allow_critical (SPEC-v0 section 4, step 5).
func TestMarkDeviceCritical(t *testing.T) {
	h := newHarness(t)
	h.ok(http.MethodPut, "/api/devices/critical", map[string]any{"entity_id": "light.kitchen", "critical": true}, nil)
	if h.marks.set["light.kitchen"] == "" {
		t.Fatalf("marks = %v", h.marks.set)
	}
	h.cat.devices[slices.IndexFunc(h.cat.devices, func(d catalog.Device) bool { return d.EntityID == "light.kitchen" })].Critical = true
	h.cat.devices = append(h.cat.devices,
		catalog.Device{EntityID: "switch.garage", Category: "switch", Attributes: map[string]any{"friendly_name": "Garage"}},
		catalog.Device{EntityID: "switch.door", Category: "switch", Critical: true, Attributes: map[string]any{"friendly_name": "Tür"}})
	var c wireDeviceCatalog
	h.ok(http.MethodGet, "/api/devices", nil, &c)
	for _, d := range c.Devices {
		if d.Critical != (d.EntityID == "light.kitchen" || d.EntityID == "switch.door") {
			t.Errorf("%s: critical = %v", d.EntityID, d.Critical)
		}
		// Only for devices not yet marked.
		if d.SuggestCritical != (d.EntityID == "switch.garage") {
			t.Errorf("%s: suggest_critical = %v", d.EntityID, d.SuggestCritical)
		}
	}
	h.ok(http.MethodPut, "/api/devices/critical", map[string]any{"entity_id": "light.kitchen", "critical": false}, nil)
	if len(h.marks.set) != 0 {
		t.Errorf("marks after removing = %v", h.marks.set)
	}
	for name, tt := range map[string]struct {
		body   map[string]any
		status int
	}{
		"unknown device": {map[string]any{"entity_id": "light.nowhere", "critical": true}, http.StatusNotFound},
		"no entity":      {map[string]any{"critical": true}, http.StatusBadRequest},
		"no value":       {map[string]any{"entity_id": "light.kitchen"}, http.StatusBadRequest},
		"wrong type":     {map[string]any{"entity_id": "light.kitchen", "critical": "yes"}, http.StatusBadRequest},
		"unknown member": {map[string]any{"entity_id": "light.kitchen", "critical": true, "all": true}, http.StatusBadRequest},
	} {
		if r := h.do(http.MethodPut, "/api/devices/critical", tt.body); r.code != tt.status {
			t.Errorf("%s: status %d, want %d", name, r.code, tt.status)
		}
	}
	h.marks.err = errors.New("database gone")
	if r := h.do(http.MethodPut, "/api/devices/critical", map[string]any{"entity_id": "light.kitchen", "critical": true}); r.code != http.StatusInternalServerError {
		t.Errorf("store error: status %d", r.code)
	}
}
