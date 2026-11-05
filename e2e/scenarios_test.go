// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: env.roots, MinVersion: tls.VersionTLS13}}
	return transport.RoundTrip(r)
}

func session(t *testing.T, token string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "e2e-agent", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: env.mcpURL, HTTPClient: &http.Client{Transport: bearer{token}, Timeout: 30 * time.Second},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// call returns the structured output or the error text of a tool call.
func call(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
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

// ready waits until the gateway is connected to Home Assistant and has its catalog.
func ready(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	eventually(t, "gateway ready", 2*time.Minute, func() bool {
		_, errText := call(t, s, "list_devices", nil)
		return errText == ""
	})
}

// auditLog verifies the chain and returns the exported log.
func auditLog(t *testing.T) string {
	t.Helper()
	if out := cli(t, "", "audit", "verify"); !strings.Contains(out, "audit log valid") {
		t.Errorf("audit verify: %s", out)
	}
	return cli(t, "", "audit", "export")
}

func hasLine(log string, parts ...string) bool {
	for _, line := range strings.Split(log, "\n") {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(line, p)
		}
		if all {
			return true
		}
	}
	return false
}

// Scenario 1 (TESTING.md §3): an agent is paired via code with the voice assistant
// mandate and switches a light; it is executed and logged.
func TestScenario01VoiceAssistantSwitchesALight(t *testing.T) {
	s := session(t, newAgent(t, "Voice assistant", nil))
	ready(t, s)
	if state := haState(t, "light.kitchen_lights"); state != "on" {
		t.Fatalf("light.kitchen_lights is %q before the test, want on (demo default)", state)
	}

	out, errText := call(t, s, "perform_action", map[string]any{"entity_id": "light.kitchen_lights", "action": "turn_off"})
	if errText != "" || out["status"] != "executed" {
		t.Fatalf("perform_action = %v, %q", out, errText)
	}
	eventually(t, "light off in Home Assistant", 10*time.Second, func() bool { return haState(t, "light.kitchen_lights") == "off" })
	if !hasLine(auditLog(t), `"entity_id":"light.kitchen_lights"`, `"status":"executed"`, `"action":"turn_off"`) {
		t.Error("no executed decision for light.kitchen_lights in the audit log")
	}
}

// Scenario 5: the camera is denied and does not appear in list_devices.
func TestScenario05CameraIsDeniedAndHidden(t *testing.T) {
	s := session(t, newAgent(t, "Camera test", nil))
	ready(t, s)
	_, camera := call(t, s, "get_state", map[string]any{"entity_id": "camera.demo_camera"})
	_, missing := call(t, s, "get_state", map[string]any{"entity_id": "camera.does_not_exist"})
	if camera != "not_found" || missing != camera {
		t.Errorf("camera %q, missing camera %q; want identical not_found", camera, missing)
	}
	out, errText := call(t, s, "list_devices", nil)
	if errText != "" {
		t.Fatal(errText)
	}
	listed, _ := json.Marshal(out)
	if strings.Contains(string(listed), "camera.") || !strings.Contains(string(listed), "light.kitchen_lights") {
		t.Errorf("list_devices = %s", listed)
	}
}

// Scenario 8: above the rate limit, requests are refused from request n+1 and logged.
func TestScenario08RateLimit(t *testing.T) {
	// Readiness is checked with another agent: list calls count against the limit too.
	ready(t, session(t, newAgent(t, "Probe", nil)))
	s := session(t, newAgent(t, "Limited", func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 3} }))
	for i := range 3 {
		if _, errText := call(t, s, "get_state", map[string]any{"entity_id": "light.bed_light"}); errText != "" {
			t.Fatalf("request %d: %q", i+1, errText)
		}
	}
	if _, errText := call(t, s, "get_state", map[string]any{"entity_id": "light.bed_light"}); errText != "rate_limited" {
		t.Errorf("request 4: %q, want rate_limited", errText)
	}
	if !strings.Contains(auditLog(t), `"denied_by":"rate_limit"`) {
		t.Error("rate limit refusal not logged")
	}
}

// startHAAgain starts the stopped Home Assistant, which publishes a new port.
func startHAAgain(t *testing.T) {
	t.Helper()
	if _, err := run("start", env.ha); err != nil {
		t.Fatal(err)
	}
	addr, err := hostPort(env.ha, "8123")
	if err != nil {
		t.Fatal(err)
	}
	env.haURL = "https://" + addr
	if err := waitHTTP(env.haURL+"/api/onboarding", 3*time.Minute); err != nil {
		t.Fatal(err)
	}
}

// Issue #34: Home Assistant goes away while a request waits, so an answer could get lost.
// When it is back, the request asks again, and the answer counts. The phone's notify
// service writes to a file: a watcher could connect only after the gateway did.
func TestReconnectAsksAgain(t *testing.T) {
	// Only this test switches it: a restart of Home Assistant resets the demo lights.
	const light = "light.entrance_color_white_lights"
	notes := watchNotifications(t)
	token := newAgent(t, "Asked again", func(d map[string]any) {
		askRule(light)(d)
		d["rules"].([]any)[0].(map[string]any)["approval"] = map[string]any{"timeout": "PT170S", "approvers": []any{env.users[adminApprover].id}}
	})
	s := session(t, token)
	ready(t, s)
	before := haState(t, light)
	id, n := pendingRequest(t, s, notes, light, toggle(before))

	if _, err := run("stop", "-t", "10", env.ha); err != nil {
		t.Fatal(err)
	}
	startHAAgain(t)
	const note = "may not have arrived"
	eventually(t, "the request asks again", time.Minute, func() bool {
		out, err := run("exec", env.ha, "cat", "/config/notifications.log")
		return err == nil && strings.Contains(out, note)
	})
	answer(t, adminApprover, n.Actions[0])
	if r := settled(t, s, toolResult{map[string]any{"status": "pending", "approval_id": id}, ""}); r.errText != "" || r.out["status"] != "executed" {
		t.Fatalf("after the answer: %v, %q", r.out, r.errText)
	}
}

// Scenario 12: Home Assistant unreachable → requests refused with a clear error; nothing
// is queued and executed later. Runs last because it restarts Home Assistant.
func TestScenario12UnreachableHomeAssistant(t *testing.T) {
	s := session(t, newAgent(t, "Outage", nil))
	ready(t, s)
	if state := haState(t, "light.bed_light"); state != "off" {
		t.Fatalf("light.bed_light is %q before the test, want off (demo default)", state)
	}

	if _, err := run("stop", "-t", "10", env.ha); err != nil {
		t.Fatal(err)
	}
	eventually(t, "gateway notices the outage", time.Minute, func() bool {
		_, errText := call(t, s, "perform_action", map[string]any{"entity_id": "light.bed_light", "action": "turn_on"})
		return errText == "unavailable"
	})
	if _, errText := call(t, s, "perform_action", map[string]any{"entity_id": "light.bed_light", "action": "turn_on"}); errText != "unavailable" {
		t.Errorf("during the outage: %q, want unavailable", errText)
	}

	startHAAgain(t)
	ready(t, s)
	time.Sleep(5 * time.Second) // time for anything queued to surface (there must be nothing)
	if state := haState(t, "light.bed_light"); state != "off" {
		t.Errorf("light.bed_light is %q after the outage: a refused request was executed later", state)
	}
	if !strings.Contains(auditLog(t), `"error":"ha_unavailable"`) {
		t.Error("outage refusals not logged")
	}
}
