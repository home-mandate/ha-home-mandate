// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"math"
	"slices"
	"strings"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// What an entity can do (issue #89, SPEC-v0 section 11.1 item 11): nobody is asked for an
// action the entity cannot carry out. Home Assistant reports it in the entity's state,
// as supported_features (a bit mask per domain) and attributes (profile Home Assistant,
// "What an entity can do"). An entity that reports nothing tells nothing and is asked as
// before; the current state counts, since some integrations change their features while
// they run.

// featureBits are the bits of supported_features an action needs, per category; the
// values of Home Assistant's LockEntityFeature, CoverEntityFeature, ClimateEntityFeature,
// MediaPlayerEntityFeature and AlarmControlPanelEntityFeature.
var featureBits = map[string]map[string]uint64{
	"lock":  {"open": 1},
	"cover": {"open": 1, "close": 2, "set_position": 4, "stop": 8},
	"gate":  {"open": 1, "close": 2},
	"media": {"pause": 1, "set_volume": 4, "turn_on": 128, "turn_off": 256, "play": 16384},
}

// armBits are the features of the alarm's arm modes, by the suffix of the service
// (AlarmControlPanelEntityFeature; 8 is TRIGGER).
var armBits = map[string]uint64{"home": 1, "away": 2, "night": 4, "custom_bypass": 16, "vacation": 32}

// ClimateEntityFeature: a single target temperature, and a range of two.
const (
	targetTemperature      uint64 = 1
	targetTemperatureRange uint64 = 2
)

// brightnessModes are the color modes of a light that have a brightness.
var brightnessModes = []string{"brightness", "color_temp", "hs", "xy", "rgb", "rgbw", "rgbww", "white"}

// canDo tells whether the entity with attrs can carry out the call of action; true when
// it reports nothing that says otherwise.
func canDo(category, action string, call ha.ServiceCall, attrs map[string]any) bool {
	switch {
	case category == "light" && action == "set":
		modes, known := stringList(attrs["supported_color_modes"])
		if !known {
			return true
		}
		if _, ok := call.Data["color_temp_kelvin"]; ok && !slices.Contains(modes, "color_temp") {
			return false
		}
		if _, ok := call.Data["brightness_pct"]; ok && !slices.ContainsFunc(modes, func(m string) bool { return slices.Contains(brightnessModes, m) }) {
			return false
		}
		return true
	case category == "climate" && action == "set_mode":
		modes, known := stringList(attrs["hvac_modes"])
		mode, _ := call.Data["hvac_mode"].(string)
		return !known || slices.Contains(modes, mode)
	case category == "alarm" && action == "arm":
		bit, ok := armBits[strings.TrimPrefix(call.Service, "alarm_arm_")]
		return !ok || has(attrs, bit)
	case category == "climate" && action == "set_temperature":
		// A range setpoint needs the range feature, a single one the other (spec #84).
		if isRange(call) {
			return has(attrs, targetTemperatureRange)
		}
		return has(attrs, targetTemperature)
	}
	bit, ok := featureBits[category][action]
	return !ok || has(attrs, bit)
}

// actionsItCanDo is the vocabulary of the category less the actions the entity cannot
// carry out with any parameters, in the vocabulary's order.
func actionsItCanDo(category string, attrs map[string]any) []string {
	return slices.DeleteFunc(actionsOf(category), func(action string) bool { return !canDoSome(category, action, attrs) })
}

// canDoSome tells whether the entity can carry out action with some parameters.
func canDoSome(category, action string, attrs map[string]any) bool {
	switch {
	case category == "light" && action == "set":
		return canDo(category, action, ha.ServiceCall{Data: map[string]any{"brightness_pct": 0}}, attrs) ||
			canDo(category, action, ha.ServiceCall{Data: map[string]any{"color_temp_kelvin": 0}}, attrs)
	case category == "climate" && action == "set_mode":
		modes, known := stringList(attrs["hvac_modes"])
		return !known || len(modes) > 0
	case category == "climate" && action == "set_temperature":
		return has(attrs, targetTemperature) || has(attrs, targetTemperatureRange)
	case category == "alarm" && action == "arm":
		for mode := range armBits {
			if canDo(category, action, ha.ServiceCall{Service: "alarm_arm_" + mode}, attrs) {
				return true
			}
		}
		return false
	}
	return canDo(category, action, ha.ServiceCall{}, attrs)
}

// isRange tells whether a set_temperature call sets a range of two temperatures.
func isRange(call ha.ServiceCall) bool {
	_, ok := call.Data["target_temp_low"]
	return ok
}

// has tells whether supported_features has bit; true when it is missing or no whole
// number, which tells nothing.
func has(attrs map[string]any, bit uint64) bool {
	n, ok := number(attrs["supported_features"])
	if !ok || n < 0 || n > math.MaxUint32 || n != math.Trunc(n) {
		return true
	}
	return uint64(n)&bit != 0
}

// stringList reads a list of strings from an attribute; known when it is a list, also an
// empty one (a climate entity without modes has none to set).
func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}
