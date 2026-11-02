// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Spec #84 / SPEC-v0 section 4 step 0: a request carries exactly the parameters its action
// declares. These scenarios run them against Home Assistant's manual alarm (every arming
// mode, no code) and the demo thermostats (climate.ecobee sets a range, climate.hvac a
// single setpoint).

const e2eAlarm = "alarm_control_panel.e2e_alarm"

// alarmAsk adds an ask rule for arming the alarm.
func alarmAsk(d map[string]any) {
	d["rules"] = append([]any{map[string]any{"id": "r-alarm", "resource": map[string]any{"entity_id": e2eAlarm},
		"actions": []any{"arm"}, "decision": "ask"}}, d["rules"].([]any)...)
}

// noNotification fails if anyone is notified within the next seconds.
func noNotification(t *testing.T, ch <-chan notification) {
	t.Helper()
	select {
	case n := <-ch:
		t.Fatalf("notified although nobody should be: %+v", n)
	case <-time.After(3 * time.Second):
	}
}

// Arming without a mode is refused before anyone is asked; with a mode the human sees it
// and Home Assistant arms exactly that mode, also vacation and custom_bypass.
func TestAlarmArmRequiresAMode(t *testing.T) {
	notes := watchNotifications(t)
	s := session(t, newAgent(t, "Alarm helper", alarmAsk))
	ready(t, s)
	if state := haState(t, e2eAlarm); state != "disarmed" {
		t.Fatalf("%s is %q before the test, want disarmed", e2eAlarm, state)
	}
	_, errText := call(t, s, "perform_action", map[string]any{"entity_id": e2eAlarm, "action": "arm"})
	if errText != "invalid_params: mode is required" {
		t.Fatalf("arm without a mode = %q", errText)
	}
	_, errText = call(t, s, "perform_action", map[string]any{"entity_id": e2eAlarm, "action": "arm", "params": map[string]any{"mode": "panic"}})
	if errText != "invalid_params: mode must be one of home, away, night, vacation, custom_bypass" {
		t.Fatalf("arm with an unknown mode = %q", errText)
	}
	noNotification(t, notes)
	if state := haState(t, e2eAlarm); state != "disarmed" {
		t.Fatalf("%s is %q after the refused requests", e2eAlarm, state)
	}

	for _, mode := range []string{"night", "vacation", "custom_bypass"} {
		result := callAsync(t, s, "perform_action", map[string]any{"entity_id": e2eAlarm, "action": "arm", "params": map[string]any{"mode": mode}})
		n := nextApproval(t, notes)
		env.secrets = append(env.secrets, strings.TrimPrefix(n.Actions[0], "HM_APPROVE_"))
		if !strings.Contains(n.Message, "mode="+mode) {
			t.Errorf("%s: the request does not show the mode: %q", mode, n.Message)
		}
		answer(t, adminApprover, n.Actions[0])
		if r := settled(t, s, awaitResult(t, result)); r.errText != "" || r.out["status"] != "executed" {
			t.Fatalf("%s: perform_action = %v, %q", mode, r.out, r.errText)
		}
		eventually(t, "alarm armed "+mode, 15*time.Second, func() bool { return haState(t, e2eAlarm) == "armed_"+mode })
		if !hasLine(auditLog(t), `"entity_id":"`+e2eAlarm+`"`, `"parameters":{"mode":"`+mode+`"}`, `"status":"executed"`) {
			t.Errorf("%s: no executed decision with the mode in the audit log", mode)
		}
		// Disarmed directly in Home Assistant for the next mode (test set-up).
		haService(t, "alarm_control_panel", "alarm_disarm", e2eAlarm)
		eventually(t, "alarm disarmed", 15*time.Second, func() bool { return haState(t, e2eAlarm) == "disarmed" })
	}
}

// A mandate limits the arming mode; the agent learns the limit in its own terms, and
// list_my_permissions still lists arm, with the limit.
func TestAlarmModeCanBeLimited(t *testing.T) {
	s := session(t, newAgent(t, "Night watch", func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-alarm", "resource": map[string]any{"entity_id": e2eAlarm},
			"actions": []any{"arm"}, "decision": "allow", "constraints": map[string]any{"mode": map[string]any{"one_of": []any{"home"}}}}},
			d["rules"].([]any)...)
	}))
	ready(t, s)
	_, errText := call(t, s, "perform_action", map[string]any{"entity_id": e2eAlarm, "action": "arm", "params": map[string]any{"mode": "away"}})
	if errText != "denied: no_match: allowed only with mode one of home" {
		t.Errorf("arm away under a home-only rule = %q", errText)
	}
	perms, errText := call(t, s, "list_my_permissions", map[string]any{"category": "alarm"})
	if errText != "" || !strings.Contains(string(mustJSON(t, perms)), `"limits":{"arm":[{"mode":{"one_of":["home"]}}]}`) {
		t.Errorf("list_my_permissions = %s, %q", mustJSON(t, perms), errText)
	}
	if state := haState(t, e2eAlarm); state != "disarmed" {
		t.Errorf("%s is %q", e2eAlarm, state)
	}
}

// A range setpoint on a thermostat that supports one: the human sees both ends with the
// unit, Home Assistant sets both, the audit entry has them in hundredths of a degree. A
// range on a thermostat without the feature (the demo heat pump), and a single setpoint
// on one that only takes a range (the demo Ecobee), are not supported, and the agent
// learns the form the thermostat takes; half a range is invalid.
func TestClimateRangeSetpoint(t *testing.T) {
	notes := watchNotifications(t)
	s := session(t, newAgent(t, "Thermostat helper", func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-thermostats", "resource": map[string]any{"category": "climate"},
			"actions": []any{"set_temperature"}, "decision": "ask"}}, d["rules"].([]any)...)
	}))
	ready(t, s)
	rangeArgs := func(entity string) map[string]any {
		return map[string]any{"entity_id": entity, "action": "set_temperature", "params": map[string]any{"target_temp_low": 20.0, "target_temp_high": 25.5}}
	}
	for name, tc := range map[string]struct {
		args    map[string]any
		errText string
	}{
		"half a range":       {map[string]any{"entity_id": "climate.ecobee", "action": "set_temperature", "params": map[string]any{"target_temp_low": 20.0}}, "invalid_params: target_temp_low and target_temp_high go together"},
		"setpoint and range": {map[string]any{"entity_id": "climate.ecobee", "action": "set_temperature", "params": map[string]any{"temperature": 21.0, "target_temp_low": 20.0, "target_temp_high": 25.0}}, "invalid_params: temperature or target_temp_low with target_temp_high, not both"},
		"nothing":            {map[string]any{"entity_id": "climate.ecobee", "action": "set_temperature"}, "invalid_params: temperature, or target_temp_low with target_temp_high, is required"},
		"range on a single-setpoint thermostat": {rangeArgs("climate.heatpump"),
			"not_supported: climate.heatpump cannot set_temperature with target_temp_low and target_temp_high; it can: set_temperature with temperature"},
		"setpoint on a range thermostat": {map[string]any{"entity_id": "climate.ecobee", "action": "set_temperature", "params": map[string]any{"temperature": 21.0}},
			"not_supported: climate.ecobee cannot set_temperature with temperature; it can: set_temperature with target_temp_low and target_temp_high"},
	} {
		if _, errText := call(t, s, "perform_action", tc.args); errText != tc.errText {
			t.Errorf("%s: %q, want %q", name, errText, tc.errText)
		}
	}
	noNotification(t, notes)

	result := callAsync(t, s, "perform_action", rangeArgs("climate.ecobee"))
	n := nextApproval(t, notes)
	env.secrets = append(env.secrets, strings.TrimPrefix(n.Actions[0], "HM_APPROVE_"))
	if !strings.Contains(n.Message, "target_temp_high=25.5 °C") || !strings.Contains(n.Message, "target_temp_low=20 °C") {
		t.Errorf("the request does not show both ends with the unit: %q", n.Message)
	}
	answer(t, adminApprover, n.Actions[0])
	if r := settled(t, s, awaitResult(t, result)); r.errText != "" || r.out["status"] != "executed" {
		t.Fatalf("range = %v, %q", r.out, r.errText)
	}
	eventually(t, "range set in Home Assistant", 15*time.Second, func() bool {
		attrs := haAttributes(t, "climate.ecobee")
		return attrs["target_temp_low"] == 20.0 && attrs["target_temp_high"] == 25.5
	})
	if !hasLine(auditLog(t), `"entity_id":"climate.ecobee"`, `"temperature_high":2550`, `"temperature_low":2000`, `"status":"executed"`) {
		t.Error("no executed decision with both ends in the audit log")
	}

	// A single setpoint keeps working on a thermostat that takes one.
	result = callAsync(t, s, "perform_action", map[string]any{"entity_id": "climate.hvac", "action": "set_temperature", "params": map[string]any{"temperature": 23.5}})
	n = nextApproval(t, notes)
	env.secrets = append(env.secrets, strings.TrimPrefix(n.Actions[0], "HM_APPROVE_"))
	if !strings.Contains(n.Message, "temperature=23.5 °C") {
		t.Errorf("the request does not show the setpoint with the unit: %q", n.Message)
	}
	answer(t, adminApprover, n.Actions[0])
	if r := settled(t, s, awaitResult(t, result)); r.errText != "" || r.out["status"] != "executed" {
		t.Fatalf("setpoint = %v, %q", r.out, r.errText)
	}
	eventually(t, "setpoint set in Home Assistant", 15*time.Second, func() bool { return haAttributes(t, "climate.hvac")["temperature"] == 23.5 })

	// Both actions stay listed for an agent allowed to use them, with the range ends as limits.
	perms, errText := call(t, s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" || !strings.Contains(string(mustJSON(t, perms)), `"set_temperature":"ask"`) {
		t.Errorf("list_my_permissions = %s, %q", mustJSON(t, perms), errText)
	}
}

// haAttributes reads an entity's attributes through the REST API of Home Assistant.
func haAttributes(t *testing.T, entityID string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.haURL+"/api/states/"+entityID, nil)
	req.Header.Set("Authorization", "Bearer "+env.haToken)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s struct {
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("attributes of %s: %v", entityID, err)
	}
	return s.Attributes
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
