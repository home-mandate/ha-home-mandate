// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import "math"

// celsius is the unit Home Assistant reports for metric households.
const celsius = "°C"

// evaluationParameter maps a parameter of a Home Assistant service call to the
// parameter of the vocabulary and its unit (SPEC-v0 section 4.5): the value times scale
// must be an integer.
type evaluationParameter struct {
	name  string
	scale float64
	// celsiusOnly: the value is a temperature in the unit of the household. Only degrees
	// Celsius can be expressed exactly in the unit of the vocabulary.
	celsiusOnly bool
}

// evaluationParameterOf: category → action → parameter of the service call.
var evaluationParameterOf = map[string]map[string]map[string]evaluationParameter{
	"light":   {"set": {"brightness_pct": {name: "brightness", scale: 1}}},
	"climate": {"set_temperature": {"temperature": {name: "temperature", scale: 100, celsiusOnly: true}}},
	"cover":   {"set_position": {"position": {name: "position", scale: 1}}},
	"media":   {"set_volume": {"volume_level": {name: "volume", scale: 100}}},
}

// exactness is how far a scaled value may be from an integer and still count as one;
// it absorbs binary floating-point noise such as 19.99 * 100.
const exactness = 1e-6

// evaluationParameters derives the parameters of the evaluation from the parameters of
// the call, in the units of the vocabulary. A value that cannot be expressed as an
// integer in that unit yields no parameter: a rule with a constraint on it then does not
// match, and the request is denied unless a rule without constraint allows it. The
// value that is sent to Home Assistant is always the one the agent gave, unchanged.
func evaluationParameters(category, action string, params map[string]any, temperatureUnit string) map[string]int64 {
	var out map[string]int64
	for callName, p := range evaluationParameterOf[category][action] {
		value, ok := params[callName].(float64)
		if !ok || p.celsiusOnly && temperatureUnit != "" && temperatureUnit != celsius {
			continue
		}
		scaled := value * p.scale
		rounded := math.Round(scaled)
		if math.IsNaN(scaled) || math.Abs(scaled-rounded) > exactness || math.Abs(rounded) > 1<<53-1 {
			continue
		}
		if out == nil {
			out = map[string]int64{}
		}
		out[p.name] = int64(rounded)
	}
	return out
}
