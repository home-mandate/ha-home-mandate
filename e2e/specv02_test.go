// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// Members of spec v0.2 in a template imported with the CLI, enforced against Home
// Assistant: a value excluded with none_of is refused and named to the agent, every other
// value is carried out; a rule whose own validity has ended allows nothing, nor does a
// rule whose range of dates lies in the past. Limits of a rule of its own are not enforced
// yet (issue #205), so a template with them is refused.

const (
	v02HeatPump = "climate.heatpump"
	v02Hvac     = "climate.hvac"
)

// v02Rules allows reading everything, the heat pump at any setpoint but 25 °C, and the
// HVAC only under a rule that expired and one for dates that have passed.
func v02Rules(d map[string]any) {
	d["rules"] = []any{
		map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
		map[string]any{"id": "r-heatpump", "resource": map[string]any{"entity_id": v02HeatPump},
			"actions": []any{"set_temperature"}, "decision": "allow",
			"constraints": map[string]any{"temperature": map[string]any{"none_of": []any{2500}}}},
		map[string]any{"id": "r-hvac-expired", "resource": map[string]any{"entity_id": v02Hvac},
			"actions": []any{"set_temperature"}, "decision": "allow", "expires": "2026-01-01T00:00:00Z"},
		map[string]any{"id": "r-hvac-holidays", "resource": map[string]any{"entity_id": v02Hvac},
			"actions": []any{"set_temperature"}, "decision": "allow",
			"conditions": map[string]any{"dates": []any{map[string]any{"from": "2025-12-24", "to": "2025-12-26"}}}},
	}
}

func TestMembersOfSpecV02AgainstHomeAssistant(t *testing.T) {
	s := session(t, newAgent(t, "Spec v0.2 helper", v02Rules))
	ready(t, s)

	const limits = `"limits":{"set_temperature":[{"temperature":{"none_of":[25],"unit":"°C"}}]}`
	perms, errText := call(t, s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" || !strings.Contains(string(mustJSON(t, perms)), limits) {
		t.Fatalf("list_my_permissions = %s, %q", mustJSON(t, perms), errText)
	}
	setpoint := func(entity string, celsius float64) map[string]any {
		return map[string]any{"entity_id": entity, "action": "set_temperature", "params": map[string]any{"temperature": celsius}}
	}
	const refusal = "denied: no_match: allowed only with temperature except 25 °C"
	if _, errText := call(t, s, "perform_action", setpoint(v02HeatPump, 25)); errText != refusal {
		t.Fatalf("25 °C = %q, want %q", errText, refusal)
	}
	if out, errText := call(t, s, "perform_action", setpoint(v02HeatPump, 21)); errText != "" || out["status"] != "executed" {
		t.Fatalf("21 °C = %v, %q", out, errText)
	}
	eventually(t, "heat pump at 21 °C", 15*time.Second, func() bool { return haAttributes(t, v02HeatPump)["temperature"] == 21.0 })

	before := haAttributes(t, v02Hvac)["temperature"]
	if _, errText := call(t, s, "perform_action", setpoint(v02Hvac, 22)); errText != "denied: no_match" {
		t.Fatalf("HVAC under an expired rule and past dates = %q", errText)
	}
	if after := haAttributes(t, v02Hvac)["temperature"]; after != before {
		t.Errorf("HVAC changed from %v to %v although it was refused", before, after)
	}
	log := auditLog(t)
	if !hasLine(log, `"entity_id":"`+v02HeatPump+`"`, `"parameters":{"temperature":2100}`, `"status":"executed"`) ||
		!hasLine(log, `"entity_id":"`+v02Hvac+`"`, `"reason":"no_match"`, `"status":"denied"`) {
		t.Error("the decisions are missing from the audit log")
	}
	if out := cli(t, "", "audit", "verify"); !strings.Contains(out, "entries=") {
		t.Errorf("audit verify = %q", out)
	}
}

// A template whose rule has limits of its own is refused by the CLI and stored nowhere.
func TestRuleLimitsAreRefusedUntilEnforced(t *testing.T) {
	doc := voiceDocument(t, func(d map[string]any) {
		d["rules"] = []any{map[string]any{"id": "r-lights", "resource": map[string]any{"category": "light"},
			"actions": []any{"turn_on"}, "decision": "allow", "limits": map[string]any{"max_actions_per_day": 3}}}
	})
	out, err := runInput(string(doc), "exec", "-i", env.hm, "/home-mandate", "mandate", "template", "import", "rule-limits", "-")
	if err == nil || !strings.Contains(err.Error(), "limits of its own") {
		t.Fatalf("import = %q, %v; want a refusal that names the limits", out, err)
	}
	if list := cli(t, "", "mandate", "template", "list"); strings.Contains(list, "rule-limits") {
		t.Errorf("the template was stored: %s", list)
	}
}
