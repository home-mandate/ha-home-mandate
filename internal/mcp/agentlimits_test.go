// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"testing"
)

// limitsMandate allows reading everything and heating within 16 to 23 °C.
func limitsMandate(d map[string]any) {
	d["rules"] = []any{
		map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
		map[string]any{"id": "r-heating", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"}, "decision": "allow",
			"constraints": map[string]any{"temperature": map[string]any{"min": 1600, "max": 2300}}},
	}
}

// The agent learns its limits in its own units: in list_my_permissions, where an action
// with limits used to be missing altogether, and with a refusal by them.
func TestAgentLearnsItsLimits(t *testing.T) {
	h := newHarness(t, limitsMandate)
	s := h.session()
	perms, errText := h.call(s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" {
		t.Fatal(errText)
	}
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"read":"allow","set_temperature":"allow"},"category":"climate","entity_ids":["climate.living_room"],`+
		`"limits":{"set_temperature":[{"temperature":{"max":23,"min":16,"unit":"°C"}}]}}]` {
		t.Errorf("groups = %s", got)
	}
	_, errText = h.call(s, "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_temperature",
		"params": map[string]any{"temperature": 25.0}})
	if errText != "denied: no_match: allowed only with temperature 16 to 23 °C" {
		t.Errorf("refusal = %q", errText)
	}
	// In a °F household the limit in °C never applies: the action is not offered.
	h.setUnit("°F")
	perms, _ = h.call(s, "list_my_permissions", map[string]any{"category": "climate"})
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"read":"allow"},"category":"climate","entity_ids":["climate.living_room"]}]` {
		t.Errorf("groups in °F = %s", got)
	}
}

// get_state names the unit of a climate device's temperatures; Home Assistant gives the
// household's unit nowhere in the attributes.
func TestGetStateNamesTheTemperatureUnit(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	climate, _ := h.call(s, "get_state", map[string]any{"entity_id": "climate.living_room"})
	light, _ := h.call(s, "get_state", map[string]any{"entity_id": "light.kitchen"})
	if climate["temperature_unit"] != "°C" || light["temperature_unit"] != nil {
		t.Errorf("climate %v, light %v", climate, light)
	}
}
