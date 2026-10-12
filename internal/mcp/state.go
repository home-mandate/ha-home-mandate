// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"math"
	"slices"
	"strings"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// The state that was shown (SPEC-v0 section 11.1 item 10, issue #27 layer 3). A request
// records the device's state when it is made and shows it to the approvers ("Garage
// door (closed): open"). After the confirmation the action is executed only if that
// state still holds: the state the action leads to already reached → not executed,
// failed already_in_state; any other change → not executed, failed state_changed. This
// keeps two confirmations, for example of two agents or of a request repeated after a
// client gave up, from acting twice on a device that toggles (a garage door on a pulse
// relay opens or closes on every open_cover).

// RECOMMENDED error codes of SPEC-v0 section 11.1 item 10.
const (
	errAlreadyInState = "already_in_state"
	errStateChanged   = "state_changed"
)

// A stateRule says when an action has reached its target: by the state (targets) or by
// an attribute equal to the requested value (attr, param). Actions without a rule have
// no state check: they act on every call (script, scene, lock open, cover stop), or
// their value cannot be compared reliably (a light's brightness and colour temperature
// are converted and rounded by Home Assistant).
type stateRule struct {
	targets []string
	attr    string // attribute holding the value set
	param   string // parameter with the requested value
}

// targetStates is the table of ARCHITECTURE section 7, per category and action.
var targetStates = map[string]map[string]stateRule{
	"light":   {"turn_on": {targets: []string{"on"}}, "turn_off": {targets: []string{"off"}}},
	"switch":  {"turn_on": {targets: []string{"on"}}, "turn_off": {targets: []string{"off"}}},
	"climate": {"set_mode": {param: "hvac_mode"}, "set_temperature": {attr: "temperature", param: "temperature"}},
	"cover": {"open": {targets: []string{"open", "opening"}}, "close": {targets: []string{"closed", "closing"}},
		"set_position": {attr: "current_position", param: "position"}},
	"gate":  {"open": {targets: []string{"open", "opening"}}, "close": {targets: []string{"closed", "closing"}}},
	"lock":  {"lock": {targets: []string{"locked", "locking"}}, "unlock": {targets: []string{"unlocked", "unlocking"}}},
	"alarm": {"arm": {}, "disarm": {targets: []string{"disarmed"}}},
	"media": {"turn_on": {targets: []string{"on", "idle", "playing", "paused", "buffering"}}, "turn_off": {targets: []string{"off"}},
		"play": {targets: []string{"playing"}}, "pause": {targets: []string{"paused"}},
		"set_volume": {attr: "volume_level", param: "volume_level"}},
}

// Agent-facing code of a refusal by the state check for an agent that may not read the
// device: it learns nothing about the state; the audit entry has the precise code.
const errNotExecuted = "not_executed"

// agentCode is the code of a state refusal as the agent gets it.
func agentCode(code string, canRead bool) string {
	if !canRead && (code == errAlreadyInState || code == errStateChanged) {
		return errNotExecuted
	}
	return code
}

// valueTolerance is how close a numeric attribute must be to the requested value.
const valueTolerance = 1e-6

// knownStates are the states of each category that are recorded and shown with a
// request; anything else (unknown, unavailable, a timestamp, text from an integration) is
// neither shown nor checked.
var knownStates = map[string][]string{
	"light":   {"on", "off"},
	"switch":  {"on", "off"},
	"climate": {"off", "heat", "cool", "heat_cool", "auto", "dry", "fan_only"},
	"cover":   {"open", "opening", "closed", "closing", "stopped"},
	"gate":    {"open", "opening", "closed", "closing", "stopped"},
	"lock":    {"locked", "unlocked", "locking", "unlocking", "jammed", "open", "opening"},
	"alarm": {"disarmed", "armed_home", "armed_away", "armed_night", "armed_vacation", "armed_custom_bypass", "arming",
		"disarming", "pending", "triggered"},
	"media": {"off", "on", "idle", "standby", "playing", "paused", "buffering"},
}

// shownState is the state recorded and shown with a request for a device of category;
// empty when it is not one of the known states, and then the request has no state check.
func shownState(category, state string) string {
	if slices.Contains(knownStates[category], state) {
		return state
	}
	return ""
}

// stateCheck compares the device now (exists: found in the directory) with the state
// recorded when the request was made; empty if the action may be executed.
func stateCheck(category, action string, call ha.ServiceCall, recorded string, now catalog.Device, exists bool) string {
	rule, ok := targetStates[category][action]
	if !ok || recorded == "" || !applies(rule, call) {
		return ""
	}
	if !exists {
		return errStateChanged
	}
	if reached(category, action, rule, call, now) {
		return errAlreadyInState
	}
	if now.State != recorded {
		return errStateChanged
	}
	return ""
}

// reached tells whether the device is in the state the action leads to.
func reached(category, action string, rule stateRule, call ha.ServiceCall, dev catalog.Device) bool {
	switch {
	case category == "alarm" && action == "arm":
		return dev.State == "armed_"+strings.TrimPrefix(call.Service, vocabulary["alarm"]["arm"].service)
	case rule.attr != "":
		want, ok1 := number(call.Data[rule.param])
		have, ok2 := number(dev.Attributes[rule.attr])
		return ok1 && ok2 && math.Abs(want-have) < valueTolerance
	case rule.param != "":
		want, _ := call.Data[rule.param].(string)
		return want != "" && dev.State == want
	case category == "cover" && action == "open":
		// A cover stopped half way is "open" too; open_cover would open it fully.
		if pos, ok := number(dev.Attributes["current_position"]); ok && pos < 100 {
			return dev.State == "opening"
		}
	}
	return slices.Contains(rule.targets, dev.State)
}

// hasStateCheck tells whether the action has a state check (a row of targetStates).
func hasStateCheck(category, action string, call ha.ServiceCall) bool {
	rule, ok := targetStates[category][action]
	return ok && applies(rule, call)
}

// reachedNow tells whether the device is in the state the call leads to now; false for
// actions without a state check.
func reachedNow(category, action string, call ha.ServiceCall, dev catalog.Device) bool {
	rule, ok := targetStates[category][action]
	return ok && applies(rule, call) && shownState(category, dev.State) != "" && reached(category, action, rule, call, dev)
}

// applies tells whether a rule says anything about call: a rule by the state alone does
// not for a call with data, whose effect goes beyond the state (a light turned on at
// another brightness).
func applies(rule stateRule, call ha.ServiceCall) bool {
	return rule.attr != "" || rule.param != "" || len(call.Data) == 0
}

// number reads a numeric value as JSON or Home Assistant delivers it.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
