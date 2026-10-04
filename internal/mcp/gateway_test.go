// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mandatespec "github.com/mandate-spec/mandate-spec"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/pdp"
	"github.com/home-mandate/home-mandate/internal/store"
	"github.com/mandate-spec/mandate-spec/ratelimit"
)

const (
	household       = "household:hm-0123456789ab"
	testResource    = "https://hm.test/mcp"
	testMetadataURL = "https://hm.test/.well-known/oauth-protected-resource/mcp"
)

var admin = audit.Actor{Kind: audit.ActorUser, ID: "user-1"}

type fakeCatalog struct {
	mu      sync.Mutex
	devices map[string]catalog.Device
	ready   bool
}

func (f *fakeCatalog) Lookup(id string) (catalog.Device, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[id]
	if ok {
		attrs := map[string]any{}
		for k, v := range d.Attributes {
			attrs[k] = v
		}
		d.Attributes = attrs
	}
	return d, ok
}

func (f *fakeCatalog) All() []catalog.Device {
	var out []catalog.Device
	for _, id := range []string{"alarm_control_panel.home", "camera.porch", "climate.living_room", "light.kitchen", "lock.front_door", "number.wallbox"} {
		if d, ok := f.Lookup(id); ok {
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeCatalog) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeCatalog) setReady(ready bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ready = ready
}

type fakeHA struct {
	mu        sync.Mutex
	connected bool
	err       error
	calls     []ha.ServiceCall
}

func (f *fakeHA) CallService(_ context.Context, call ha.ServiceCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeHA) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeHA) set(fn func(*fakeHA)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeHA) recorded() []ha.ServiceCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ha.ServiceCall(nil), f.calls...)
}

type harness struct {
	t        *testing.T
	url      string
	token    string
	agent    agent.Agent
	agents   *agent.Store
	ha       *fakeHA
	catalog  *fakeCatalog
	log      *audit.Log
	db       *sql.DB
	pdp      *pdp.PDP
	mandates *mandate.Store
	approver Approver
	decider  Decider // replaces pdp when set

	mu sync.Mutex
	tz string
}

func (h *harness) timeZone() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tz
}

// newHarness runs the gateway on real stores with the voice assistant mandate of
// mandate-spec, edited by edit.
func newHarness(t *testing.T, edit func(map[string]any)) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), household)
	agents := agent.New(st.DB(), log)
	mandates := mandate.New(st.DB(), log, household)

	a, err := agents.Register(ctx, "Voice assistant", admin)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := agents.IssueTokens(ctx, a.ClientID, testResource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mandates.Put(ctx, mandateDoc(t, a.ClientID, edit), admin); err != nil {
		t.Fatal(err)
	}

	h := &harness{t: t, token: tokens.AccessToken, agent: a, agents: agents, log: log, db: st.DB(), mandates: mandates, tz: "Europe/Berlin",
		ha: &fakeHA{connected: true},
		catalog: &fakeCatalog{ready: true, devices: map[string]catalog.Device{
			"light.kitchen":            {EntityID: "light.kitchen", Category: "light", Area: "kitchen", State: "off", Attributes: map[string]any{"friendly_name": "Kitchen"}},
			"lock.front_door":          {EntityID: "lock.front_door", Category: "lock", Area: "hallway", State: "locked"},
			"camera.porch":             {EntityID: "camera.porch", Category: "camera", Area: "porch", State: "idle", Attributes: map[string]any{"entity_picture": "/api/camera_proxy/camera.porch?token=secret"}},
			"alarm_control_panel.home": {EntityID: "alarm_control_panel.home", Category: "alarm", State: "armed_away"},
			"climate.living_room":      {EntityID: "climate.living_room", Category: "climate", State: "heat"},
			"number.wallbox":           {EntityID: "number.wallbox", Category: "other", State: "16"},
		}}}
	h.pdp = pdp.New(pdp.Config{Principal: household, Mandates: mandates, Catalog: h.catalog, TimeZone: h.timeZone})
	h.url = h.serve(log)
	return h
}

// serve starts a gateway on the harness with auditor and returns its URL.
func (h *harness) serve(auditor Auditor) string {
	var decider Decider = h.pdp
	if h.decider != nil {
		decider = h.decider
	}
	g := New(Config{Resource: testResource, ResourceMetadataURL: testMetadataURL, Agents: h.agents, PDP: decider, Approvals: h.approver, Catalog: h.catalog, HA: h.ha, Limiter: ratelimit.New(nil), Audit: auditor, Version: "test"})
	srv := httptest.NewServer(g.Handler())
	h.t.Cleanup(srv.Close)
	return srv.URL + Path
}

// issue returns a new access token of clientID for the test resource.
func (h *harness) issue(clientID string) string {
	h.t.Helper()
	p, err := h.agents.IssueTokens(context.Background(), clientID, testResource)
	if err != nil {
		h.t.Fatal(err)
	}
	return p.AccessToken
}

func mandateDoc(t *testing.T, clientID string, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	doc["principal"] = household
	doc["agent"] = map[string]any{"client_id": clientID, "display_name": "Voice assistant"}
	if edit != nil {
		edit(doc)
	}
	out, _ := json.Marshal(doc)
	return out
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (h *harness) session() *sdk.ClientSession {
	h.t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: h.url, HTTPClient: &http.Client{Transport: bearer{h.token}},
	}, nil)
	if err != nil {
		h.t.Fatalf("connect: %v", err)
	}
	h.t.Cleanup(func() { _ = s.Close() })
	return s
}

// call returns the structured output or the error text of a tool call.
func (h *harness) call(s *sdk.ClientSession, tool string, args map[string]any) (map[string]any, string) {
	h.t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, "protocol: " + err.Error()
	}
	if res.IsError {
		var texts []string
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		return nil, strings.Join(texts, " ")
	}
	var out map[string]any
	data, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(data, &out)
	return out, ""
}

func (h *harness) auditLog() string {
	h.t.Helper()
	var buf bytes.Buffer
	if err := h.log.Export(context.Background(), &buf); err != nil {
		h.t.Fatal(err)
	}
	return buf.String()
}

func (h *harness) lastEntry() map[string]any {
	h.t.Helper()
	lines := strings.Split(strings.TrimSpace(h.auditLog()), "\n")
	var e map[string]any
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &e)
	return e
}

func path(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestAllowedActionIsExecutedAndLogged(t *testing.T) {
	h := newHarness(t, nil)
	out, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"})
	if errText != "" || out["status"] != "executed" {
		t.Fatalf("perform_action = %v, %q", out, errText)
	}
	calls := h.ha.recorded()
	if len(calls) != 1 || calls[0].Domain != "light" || calls[0].Service != "turn_on" || calls[0].EntityID != "light.kitchen" {
		t.Errorf("HA calls = %+v", calls)
	}
	e := h.lastEntry()
	if e["event"] != "decision" || path(e, "result", "status") != "executed" || path(e, "evaluation", "decision") != "allow" ||
		path(e, "request", "resource", "category") != "light" || path(e, "request", "timezone") != "Europe/Berlin" ||
		path(e, "agent", "client_id") != h.agent.ClientID || path(e, "mandate", "id") != "m-voice-assistant" {
		t.Errorf("audit entry = %v", e)
	}
	if strings.Contains(h.auditLog(), h.token) {
		t.Error("token in the audit log")
	}
	if r, err := h.log.Verify(context.Background()); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

func TestAskIsRefusedUntilApprovalsExist(t *testing.T) {
	h := newHarness(t, nil)
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
	if !strings.HasPrefix(errText, "approval_required") || len(h.ha.recorded()) != 0 {
		t.Errorf("unlock: %q, HA calls %d", errText, len(h.ha.recorded()))
	}
	e := h.lastEntry()
	if path(e, "result", "denied_by") != "approval" || path(e, "evaluation", "decision") != "ask" || path(e, "evaluation", "approval_timeout") != "PT2M" {
		t.Errorf("audit entry = %v", e)
	}
}

func TestDeniedActionOnAReadableDevice(t *testing.T) {
	h := newHarness(t, nil)
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "alarm_control_panel.home", "action": "disarm"})
	if errText != "denied: rule" || len(h.ha.recorded()) != 0 {
		t.Errorf("disarm: %q", errText)
	}
	if e := h.lastEntry(); path(e, "result", "denied_by") != "mandate" || path(e, "evaluation", "rule_id") != "r-no-alarm" {
		t.Errorf("audit entry = %v", e)
	}
}

// TESTING.md section 4: an entity outside the mandate gets the same answer as one that
// does not exist; E2E scenario 5: the camera is not listed.
func TestUnreadableDevicesDoNotExistForTheAgent(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	_, camera := h.call(s, "get_state", map[string]any{"entity_id": "camera.porch"})
	_, missing := h.call(s, "get_state", map[string]any{"entity_id": "camera.nowhere"})
	if camera != "not_found" || missing != camera {
		t.Errorf("camera %q, missing %q; want identical not_found", camera, missing)
	}
	_, cameraAction := h.call(s, "perform_action", map[string]any{"entity_id": "camera.porch", "action": "snapshot"})
	_, missingAction := h.call(s, "perform_action", map[string]any{"entity_id": "camera.nowhere", "action": "snapshot"})
	if cameraAction != "not_found" || missingAction != cameraAction {
		t.Errorf("actions: camera %q, missing %q", cameraAction, missingAction)
	}
	out, errText := h.call(s, "list_devices", nil)
	if errText != "" {
		t.Fatal(errText)
	}
	if listed := mustJSON(out); strings.Contains(listed, "camera") || !strings.Contains(listed, "light.kitchen") {
		t.Errorf("list_devices = %s", listed)
	}
}

func TestGetStateHidesTokenAttributes(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"}}
	})
	out, errText := h.call(h.session(), "get_state", map[string]any{"entity_id": "camera.porch"})
	if errText != "" || out["state"] != "idle" {
		t.Fatalf("get_state = %v, %q", out, errText)
	}
	if s := mustJSON(out); strings.Contains(s, "token=secret") || strings.Contains(s, "entity_picture") {
		t.Errorf("attributes leak the camera token: %s", s)
	}
	if e := h.lastEntry(); path(e, "result", "status") != "executed" || path(e, "request", "action") != "read" {
		t.Errorf("audit entry = %v", e)
	}
}

func TestListMyPermissions(t *testing.T) {
	h := newHarness(t, nil)
	out, errText := h.call(h.session(), "list_my_permissions", nil)
	if errText != "" {
		t.Fatal(errText)
	}
	got := map[string]map[string]any{}
	devices, _ := out["devices"].([]any)
	for _, d := range devices {
		m := d.(map[string]any)
		got[m["entity_id"].(string)] = m["actions"].(map[string]any)
	}
	if got["light.kitchen"]["turn_on"] != "allow" || got["lock.front_door"]["unlock"] != "ask" || got["lock.front_door"]["read"] != "allow" {
		t.Errorf("permissions = %v", got)
	}
	if _, ok := got["camera.porch"]; ok {
		t.Error("camera listed in permissions")
	}
	if _, ok := got["alarm_control_panel.home"]["disarm"]; ok {
		t.Error("denied action listed")
	}
}

func TestRateLimit(t *testing.T) {
	h := newHarness(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 2} })
	s := h.session()
	for i := range 2 {
		if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "" {
			t.Fatalf("request %d: %q", i+1, errText)
		}
	}
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "rate_limited" {
		t.Errorf("request 3: %q, want rate_limited", errText)
	}
	if n := len(h.ha.recorded()); n != 2 {
		t.Errorf("HA calls = %d", n)
	}
	if e := h.lastEntry(); path(e, "result", "denied_by") != "rate_limit" || e["evaluation"] != nil {
		t.Errorf("audit entry = %v", e)
	}
}

// E2E scenario 12, unit level: without Home Assistant every request is refused and
// logged; nothing is queued.
func TestUnavailableHomeAssistant(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	h.ha.set(func(f *fakeHA) { f.connected = false })
	calls := map[string]map[string]any{
		"perform_action":      {"entity_id": "light.kitchen", "action": "turn_on"},
		"get_state":           {"entity_id": "light.kitchen"},
		"list_devices":        nil,
		"list_my_permissions": nil,
	}
	for tool, args := range calls {
		if _, errText := h.call(s, tool, args); errText != "unavailable" {
			t.Errorf("%s: %q, want unavailable", tool, errText)
		}
	}
	if !strings.Contains(h.auditLog(), `"error":"ha_unavailable"`) {
		t.Error("refusal not logged")
	}
	h.ha.set(func(f *fakeHA) { f.connected = true })
	if len(h.ha.recorded()) != 0 {
		t.Error("a refused request was executed later")
	}
}

func TestCatalogNotLoadedOrTimeZoneUnknown(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	h.catalog.setReady(false)
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "unavailable" {
		t.Errorf("catalog not ready: %q", errText)
	}
	h.catalog.setReady(true)
	h.mu.Lock()
	h.tz = ""
	h.mu.Unlock()
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "unavailable" {
		t.Errorf("time zone unknown: %q", errText)
	}
	if e := h.lastEntry(); path(e, "result", "error") != "timezone_unknown" {
		t.Errorf("audit entry = %v", e)
	}
}

func TestExecutionFailures(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	for code, err := range map[string]error{
		"ha_unavailable": ha.ErrDisconnected,
		"ha_error":       &ha.CommandError{Code: "home_assistant_error", Message: "secret detail"},
	} {
		h.ha.set(func(f *fakeHA) { f.err = err })
		_, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_off"})
		if errText != "failed" {
			t.Errorf("%s: %q, want failed without details", code, errText)
		}
		if e := h.lastEntry(); path(e, "result", "error") != code {
			t.Errorf("%s: audit entry = %v", code, e)
		}
	}
}

func TestParametersAreChecked(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-all", "resource": map[string]any{"any": true}, "actions": []any{"set", "set_temperature", "set_mode", "arm", "snapshot"},
				"decision": "allow", "allow_critical": true},
		}
	})
	s := h.session()
	tests := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"entity_id": "light.kitchen", "action": "set", "params": map[string]any{"brightness_pct": 40}}, ""},
		{map[string]any{"entity_id": "light.kitchen", "action": "set", "params": map[string]any{"brightness_pct": 150}}, "invalid_params"},
		{map[string]any{"entity_id": "light.kitchen", "action": "set", "params": map[string]any{"brightness_pct": 40.5}}, "invalid_params"},
		{map[string]any{"entity_id": "light.kitchen", "action": "set", "params": map[string]any{"entity_id": "lock.front_door"}}, "invalid_params"},
		{map[string]any{"entity_id": "light.kitchen", "action": "set"}, "invalid_params"},
		{map[string]any{"entity_id": "climate.living_room", "action": "set_temperature"}, "invalid_params"},
		{map[string]any{"entity_id": "climate.living_room", "action": "set_mode", "params": map[string]any{"hvac_mode": "sauna"}}, "invalid_params"},
		{map[string]any{"entity_id": "climate.living_room", "action": "set_temperature", "params": map[string]any{"temperature": 21.5}}, ""},
		{map[string]any{"entity_id": "alarm_control_panel.home", "action": "arm", "params": map[string]any{"mode": "night"}}, ""},
		{map[string]any{"entity_id": "camera.porch", "action": "snapshot"}, "not_supported"},
		{map[string]any{"entity_id": "number.wallbox", "action": "set"}, "not_supported"},
	}
	for _, tt := range tests {
		_, errText := h.call(s, "perform_action", tt.args)
		if tt.want == "" && errText != "" || tt.want != "" && !strings.HasPrefix(errText, tt.want) {
			t.Errorf("%v: %q, want %q", tt.args, errText, tt.want)
		}
	}
	calls := h.ha.recorded()
	if len(calls) != 3 || calls[0].Data["brightness_pct"] != 40 || calls[2].Service != "alarm_arm_night" {
		t.Errorf("HA calls = %+v", calls)
	}
}

// TESTING.md section 4, MCP interface.
func TestMalformedToolCalls(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	tests := []struct {
		tool string
		args map[string]any
	}{
		{"unlock_everything", nil},
		{"get_state", map[string]any{}},
		{"get_state", map[string]any{"entity_id": 42}},
		{"get_state", map[string]any{"entity_id": "light.kitchen", "extra": true}},
		{"get_state", map[string]any{"entity_id": "LIGHT.Kitchen"}},
		{"get_state", map[string]any{"entity_id": "light.kitchen; lock.front_door"}},
		{"get_state", map[string]any{"entity_id": strings.Repeat("a", 300) + ".x"}},
		{"perform_action", map[string]any{"entity_id": "light.kitchen"}},
		{"perform_action", map[string]any{"entity_id": "light.kitchen", "action": "read"}},
		{"perform_action", map[string]any{"entity_id": "light.kitchen", "action": "TurnOn"}},
		{"perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on", "params": "x"}},
		{"create_mandate", map[string]any{"rules": []any{}}},
		{"revoke_agent", map[string]any{"client_id": "x"}},
	}
	for _, tt := range tests {
		out, errText := h.call(s, tt.tool, tt.args)
		if errText == "" {
			t.Errorf("%s %v succeeded: %v", tt.tool, tt.args, out)
		}
		if strings.Contains(errText, "goroutine") || strings.Contains(errText, ".go:") || strings.Contains(errText, "sql") {
			t.Errorf("%s: internal details in %q", tt.tool, errText)
		}
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("a malformed call reached Home Assistant")
	}
}

func TestOnlyTheFourToolsExist(t *testing.T) {
	h := newHarness(t, nil)
	res, err := h.session().ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "get_state,list_devices,list_my_permissions,perform_action" {
		t.Errorf("tools = %v", names)
	}
}

func TestAuthentication(t *testing.T) {
	h := newHarness(t, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	for name, header := range map[string]string{
		"no token":     "",
		"wrong scheme": "Basic " + h.token,
		"unknown":      "Bearer hma_" + strings.Repeat("A", 43),
		"garbage":      "Bearer x",
	} {
		req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
	// Revocation takes effect for the next request.
	s := h.session()
	if err := h.agents.Revoke(context.Background(), h.agent.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, errText := h.call(s, "list_devices", nil); errText == "" {
		t.Error("revoked agent still served")
	}
}

func TestOversizedRequestIsRejected(t *testing.T) {
	h := newHarness(t, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_state","arguments":{"entity_id":"` + strings.Repeat("a", 100000) + `"}}}`
	req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("status %d for an oversized request", resp.StatusCode)
	}
}

func TestMissingMandateIsNotFound(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	other, err := h.agents.Register(ctx, "No mandate", admin)
	if err != nil {
		t.Fatal(err)
	}
	h.token = h.issue(other.ClientID)
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"})
	if errText != "not_found" {
		t.Errorf("agent without mandate: %q", errText)
	}
	if e := h.lastEntry(); path(e, "evaluation", "reason") != "no_mandate" || e["mandate"] != nil {
		t.Errorf("audit entry = %v", e)
	}
}

// failingAuditor cannot write anything.
type failingAuditor struct{}

func (failingAuditor) Append(context.Context, audit.Entry) (int64, error) {
	return 0, errors.New("disk full")
}
func (failingAuditor) WithEntry(context.Context, audit.Entry, func() error) error {
	return errors.New("disk full")
}

func TestNothingIsExecutedOrReadWithoutAnAuditEntry(t *testing.T) {
	h := newHarness(t, nil)
	h.url = h.serve(failingAuditor{})
	s := h.session()
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "unavailable" {
		t.Errorf("perform_action: %q, want unavailable", errText)
	}
	if _, errText := h.call(s, "get_state", map[string]any{"entity_id": "light.kitchen"}); errText != "unavailable" {
		t.Errorf("get_state: %q, want unavailable", errText)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("Home Assistant was called without an audit entry")
	}
}

func TestFailedActionLeavesOnlyAFailedEntry(t *testing.T) {
	h := newHarness(t, nil)
	h.ha.set(func(f *fakeHA) { f.err = ha.ErrDisconnected })
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "failed" {
		t.Fatalf("perform_action: %q", errText)
	}
	log := h.auditLog()
	if strings.Contains(log, `"status":"executed"`) || !strings.Contains(log, `"error":"ha_unavailable"`) {
		t.Errorf("audit log:\n%s", log)
	}
}

func TestAskReadIsNotListedAndAskOnUnreadableIsNotFound(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-lock-read", "resource": map[string]any{"category": "lock"}, "actions": []any{"read"}, "decision": "ask"},
			map[string]any{"id": "r-no-camera-read", "resource": map[string]any{"category": "camera"}, "actions": []any{"read"}, "decision": "deny"},
			map[string]any{"id": "r-camera", "resource": map[string]any{"category": "camera"}, "actions": []any{"snapshot"}, "decision": "ask"},
		}
	})
	s := h.session()
	out, errText := h.call(s, "list_devices", nil)
	if errText != "" {
		t.Fatal(errText)
	}
	if listed := mustJSON(out); strings.Contains(listed, "lock.front_door") || strings.Contains(listed, "camera.porch") || !strings.Contains(listed, "light.kitchen") {
		t.Errorf("list_devices = %s", listed)
	}
	if _, errText := h.call(s, "get_state", map[string]any{"entity_id": "lock.front_door"}); !strings.HasPrefix(errText, "approval_required") {
		t.Errorf("get_state on ask-read lock: %q", errText)
	}
	// The camera may not be read; asking to snapshot it must not reveal that it exists.
	_, camera := h.call(s, "perform_action", map[string]any{"entity_id": "camera.porch", "action": "snapshot"})
	_, missing := h.call(s, "perform_action", map[string]any{"entity_id": "camera.nowhere", "action": "snapshot"})
	if camera != "not_found" || missing != camera {
		t.Errorf("ask on an unreadable camera: %q, missing %q", camera, missing)
	}
}

func TestRateLimitRefusalsAreLoggedOncePerInterval(t *testing.T) {
	h := newHarness(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 1} })
	s := h.session()
	for range 5 {
		h.call(s, "get_state", map[string]any{"entity_id": "light.kitchen"})
	}
	if n := strings.Count(h.auditLog(), `"denied_by":"rate_limit"`); n != 1 {
		t.Errorf("%d rate-limit entries for 4 refusals within a minute, want 1", n)
	}
	if _, errText := h.call(s, "list_devices", nil); errText != "rate_limited" {
		t.Errorf("list_devices beyond the limit: %q", errText)
	}
}

func TestAgentsWithoutMandateAreRateLimited(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	other, err := h.agents.Register(ctx, "No mandate", admin)
	if err != nil {
		t.Fatal(err)
	}
	h.token = h.issue(other.ClientID)
	s := h.session()
	last := ""
	for range noMandateLimit + 1 {
		_, last = h.call(s, "get_state", map[string]any{"entity_id": "light.kitchen"})
	}
	if last != "rate_limited" {
		t.Errorf("request %d without a mandate: %q, want rate_limited", noMandateLimit+1, last)
	}
}

func TestSanitizeRemovesTokenCarryingAttributes(t *testing.T) {
	got := sanitize(map[string]any{
		"friendly_name":        "TV",
		"entity_picture":       "/api/media_player_proxy/media_player.tv?token=abc",
		"entity_picture_local": "/api/media_player_proxy/media_player.tv?token=abc",
		"access_token":         "abc",
		"stream_url":           "rtsp://host/x?Token=abc",
		"volume_level":         0.4,
	})
	if len(got) != 2 || got["friendly_name"] != "TV" || got["volume_level"] != 0.4 {
		t.Errorf("sanitize = %v", got)
	}
}

// The UI offers per category exactly the actions of the vocabulary the PEP enforces.
func TestActionsOfTheVocabulary(t *testing.T) {
	if got := Actions("light"); !slices.Equal(got, []string{"read", "set", "turn_off", "turn_on"}) {
		t.Errorf("Actions(light) = %v", got)
	}
	if got := Actions("lock"); !slices.Equal(got, []string{"lock", "open", "read", "unlock"}) {
		t.Errorf("Actions(lock) = %v", got)
	}
	if got := Actions("paperless:document"); len(got) != 0 {
		t.Errorf("unknown category = %v", got)
	}
}

// A constraint of the mandate limits what the agent may set; the evaluated value is the
// one in the audit log and the one sent to Home Assistant (SPEC-v0 section 4.5).
func TestConstraintsLimitParameters(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-heating", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"},
				"decision": "allow", "constraints": map[string]any{"temperature": map[string]any{"min": 1600, "max": 2300}}},
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
		}
	})
	s := h.session()
	set := func(value any) string {
		_, errText := h.call(s, "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_temperature",
			"params": map[string]any{"temperature": value}})
		return errText
	}
	if errText := set(21.5); errText != "" {
		t.Fatalf("21.5 degrees within the limits: %q", errText)
	}
	e := h.lastEntry()
	if path(e, "request", "parameters", "temperature") != float64(2150) || path(e, "evaluation", "rule_id") != "r-heating" {
		t.Errorf("audit entry = %v", e)
	}
	if calls := h.ha.recorded(); len(calls) != 1 || calls[0].Data["temperature"] != 21.5 {
		t.Errorf("HA calls = %+v", calls)
	}
	for _, value := range []any{23.5, 15.0, 21.555} {
		if errText := set(value); !strings.HasPrefix(errText, "denied") {
			t.Errorf("%v degrees: %q, want denied", value, errText)
		}
		if e := h.lastEntry(); path(e, "evaluation", "reason") != "no_match" {
			t.Errorf("%v degrees: audit entry = %v", value, e)
		}
	}
	if calls := h.ha.recorded(); len(calls) != 1 {
		t.Errorf("a denied value reached Home Assistant: %+v", calls)
	}
}
