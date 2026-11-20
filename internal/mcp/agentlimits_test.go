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

// The limit is listed also when the probe's sample value happens to be permitted: a
// home-only rule on arming lists arm with its limit, not as unlimited.
func TestLimitIsListedWhenTheSampleValueIsPermitted(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-arm", "resource": map[string]any{"category": "alarm"}, "actions": []any{"arm"}, "decision": "allow",
				"constraints": map[string]any{"mode": map[string]any{"one_of": []any{"home"}}}},
		}
	})
	s := h.session()
	perms, errText := h.call(s, "list_my_permissions", map[string]any{"category": "alarm"})
	if errText != "" {
		t.Fatal(errText)
	}
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"arm":"allow","read":"allow"},"category":"alarm","entity_ids":["alarm_control_panel.home"],`+
		`"limits":{"arm":[{"mode":{"one_of":["home"]}}]}}]` {
		t.Errorf("groups = %s", got)
	}
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "alarm_control_panel.home", "action": "arm",
		"params": map[string]any{"mode": "away"}}); errText != "denied: no_match: allowed only with mode one of home" {
		t.Errorf("arm away = %q", errText)
	}
}

// A list of permitted modes is told in the agent's terms: auto stands for heat_cool and
// auto of Home Assistant (Home Assistant profile); a refused mode names the allowed ones.
func TestAgentLearnsItsModes(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-modes", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_mode"}, "decision": "allow",
				"constraints": map[string]any{"mode": map[string]any{"one_of": []any{"heat", "auto", "sleep"}}}},
		}
	})
	s := h.session()
	perms, errText := h.call(s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" {
		t.Fatal(errText)
	}
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"read":"allow","set_mode":"allow"},"category":"climate","entity_ids":["climate.living_room"],`+
		`"limits":{"set_mode":[{"hvac_mode":{"one_of":["auto","heat","heat_cool"]}}]}}]` {
		t.Errorf("groups = %s", got)
	}
	for mode, want := range map[string]string{"heat_cool": "", "heat": "", "off": "denied: no_match: allowed only with hvac_mode one of auto, heat, heat_cool"} {
		if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_mode",
			"params": map[string]any{"hvac_mode": mode}}); errText != want {
			t.Errorf("%s: %q, want %q", mode, errText, want)
		}
	}
}

// Excluded values (none_of, spec v0.2) are told as such: in list_my_permissions and in a
// refusal, never as a parameter without a limit.
func TestAgentLearnsItsExcludedValues(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-heating", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"}, "decision": "allow",
				"constraints": map[string]any{"temperature": map[string]any{"none_of": []any{2500, 2600}}}},
			map[string]any{"id": "r-modes", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_mode"}, "decision": "allow",
				"constraints": map[string]any{"mode": map[string]any{"none_of": []any{"off"}}}},
		}
	})
	s := h.session()
	perms, errText := h.call(s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" {
		t.Fatal(errText)
	}
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"read":"allow","set_mode":"allow","set_temperature":"allow"},"category":"climate","entity_ids":["climate.living_room"],`+
		`"limits":{"set_mode":[{"hvac_mode":{"none_of":["off"]}}],"set_temperature":[{"temperature":{"none_of":[25,26],"unit":"°C"}}]}}]` {
		t.Errorf("groups = %s", got)
	}
	for _, c := range []struct {
		action string
		params map[string]any
		want   string
	}{
		{"set_temperature", map[string]any{"temperature": 25.0}, "denied: no_match: allowed only with temperature except 25, 26 °C"},
		{"set_temperature", map[string]any{"temperature": 21.0}, ""},
		{"set_mode", map[string]any{"hvac_mode": "off"}, "denied: no_match: allowed only with hvac_mode except off"},
		{"set_mode", map[string]any{"hvac_mode": "heat"}, ""},
	} {
		if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "climate.living_room", "action": c.action, "params": c.params}); errText != c.want {
			t.Errorf("%s %v: %q, want %q", c.action, c.params, errText, c.want)
		}
	}
}

// Excluding only values the agent cannot give (no mode of Home Assistant maps to sleep)
// leaves the parameter without a limit for the agent.
func TestExcludedValuesTheAgentCannotGiveLimitNothing(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-modes", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_mode"}, "decision": "allow",
				"constraints": map[string]any{"mode": map[string]any{"none_of": []any{"sleep"}}}},
		}
	})
	s := h.session()
	perms, errText := h.call(s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" {
		t.Fatal(errText)
	}
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"read":"allow","set_mode":"allow"},"category":"climate","entity_ids":["climate.living_room"]}]` {
		t.Errorf("groups = %s", got)
	}
}
