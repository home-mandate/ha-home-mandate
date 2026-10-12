// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"errors"
	"fmt"
	"math"
)

// celsius is the unit Home Assistant reports for metric households.
const celsius = "°C"

// errTemperatureUnitUnknown: the household's temperature unit is not known yet (#42), so
// a temperature cannot be checked. Not the agent's fault: the request is unavailable.
var errTemperatureUnitUnknown = errors.New("temperature_unit_unknown")

// evaluationParameter maps a parameter of a Home Assistant service call to the
// parameter of the vocabulary and its unit (SPEC-v0 section 4.5): the value times scale
// must be an integer.
type evaluationParameter struct {
	name  string
	scale float64
	// celsiusOnly: the value is a temperature in the unit of the household. Only degrees
	// Celsius can be expressed in the unit of the vocabulary; a temperature in any other
	// unit is refused, in an unknown unit it is unavailable (#42, #44).
	celsiusOnly bool
}

// evaluationParameterOf: category → action → parameter of the service call.
var evaluationParameterOf = map[string]map[string]map[string]evaluationParameter{
	"light":   {"set": {"brightness_pct": {name: "brightness", scale: 1}}},
	"climate": {"set_temperature": {"temperature": {name: "temperature", scale: 100, celsiusOnly: true}}},
	"cover":   {"set_position": {"position": {name: "position", scale: 1}}},
	"media":   {"set_volume": {"volume_level": {name: "volume", scale: 100}}},
}

const (
	// exactness is how far a scaled value may be from an integer and still count as one;
	// it absorbs binary floating-point noise such as 19.99 * 100, nothing more.
	exactness = 1e-6
	// maxExactInteger is the largest integer a float64 holds exactly.
	maxExactInteger = 1<<53 - 1
)

// requestParameters checks the parameters of a vocabulary action before the decision and
// derives the parameters of the evaluation, in the units of the vocabulary. A value that
// cannot be expressed as an integer in that unit makes the request invalid (SPEC-v0
// section 4.5): an error wrapping errInvalidParams that names the parameter. A
// temperature while the household's unit is unknown gives errTemperatureUnitUnknown. An
// action outside the vocabulary has no parameters to check; the decision refuses it.
func requestParameters(category, action string, params map[string]any, temperatureUnit string) (map[string]int64, error) {
	spec, ok := vocabulary[category][action]
	if !ok || action == "read" {
		return nil, nil
	}
	if err := checkTemperatureUnit(category, action, params, temperatureUnit); err != nil {
		return nil, err
	}
	data, err := callData(category, action, spec, params)
	if err != nil {
		return nil, err
	}
	return evaluationParameters(category, action, data)
}

// checkTemperatureUnit refuses a temperature the vocabulary cannot express: one in a
// household that does not use degrees Celsius, and one while the unit is unknown.
func checkTemperatureUnit(category, action string, params map[string]any, unit string) error {
	for callName, p := range evaluationParameterOf[category][action] {
		if _, present := params[callName]; !present || !p.celsiusOnly || unit == celsius {
			continue
		}
		if unit == "" {
			return errTemperatureUnitUnknown
		}
		return fmt.Errorf("%w: %s cannot be checked in a household that uses %s yet", errInvalidParams, callName, unit)
	}
	return nil
}

// evaluationParameters derives the parameters of the evaluation from the checked data of
// the service call. Every value has been checked to be a whole step of its unit; one that
// is not anyway fails closed with an error, it is never dropped.
func evaluationParameters(category, action string, data map[string]any) (map[string]int64, error) {
	var out map[string]int64
	for callName, p := range evaluationParameterOf[category][action] {
		value, present := data[callName]
		if !present {
			continue
		}
		var f float64
		switch v := value.(type) {
		case int:
			f = float64(v)
		case float64:
			f = v
		default:
			return nil, fmt.Errorf("parameter %s is no number: %T", callName, value)
		}
		scaled := f * p.scale
		rounded := math.Round(scaled)
		if math.IsNaN(scaled) || math.IsInf(scaled, 0) || math.Abs(scaled-rounded) > exactness || math.Abs(rounded) > maxExactInteger {
			return nil, fmt.Errorf("parameter %s is no integer in the unit of the vocabulary: %v", callName, f)
		}
		if out == nil {
			out = map[string]int64{}
		}
		out[p.name] = int64(rounded)
	}
	return out, nil
}
