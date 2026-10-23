// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"errors"
	"fmt"

	"github.com/home-mandate/spec/evaluator"
)

// The units Home Assistant reports for temperatures: metric and US customary households.
const (
	celsius    = "°C"
	fahrenheit = "°F"
)

// errTemperatureUnitUnknown: the household's temperature unit is not known yet (#42), so
// a temperature cannot be checked. Not the agent's fault: the request is unavailable.
var errTemperatureUnitUnknown = errors.New("temperature_unit_unknown")

// errTemperatureUnitChanged: a temperature was confirmed in one unit, and the household
// uses another one when it would be executed. Nothing is executed.
const errTemperatureUnitChanged = "temperature_unit_changed"

// unitOf is the unit a temperature among the parameters of an evaluation is in: "" if
// there is none.
func unitOf(parameters map[string]evaluator.Value) string {
	for unit, name := range evaluationParameterOf["climate"]["set_temperature"]["temperature"].perUnit {
		if _, ok := parameters[name]; ok {
			return unit
		}
	}
	return ""
}

// evaluationParameter maps a parameter of a Home Assistant service call to the
// parameter of the vocabulary and its unit (SPEC-v0 section 4.5): the value times scale
// must be an integer.
type evaluationParameter struct {
	name  string
	scale float64
	// perUnit: the value is a temperature in the unit of the household, and the parameter
	// of the vocabulary depends on that unit (spec #21 part B): exactly one of them, never
	// a conversion. A temperature in any other unit is refused, in an unknown unit it is
	// unavailable (#42, #44).
	perUnit map[string]string
	// values maps the values of a string parameter of the service call to the permitted
	// values of the vocabulary (Home Assistant profile); a value without one is invalid.
	values map[string]string
}

// hvacModes maps the HVAC modes of Home Assistant to mode (Home Assistant profile).
var hvacModes = map[string]string{
	"off": "off", "heat": "heat", "cool": "cool", "heat_cool": "auto", "auto": "auto", "dry": "dry", "fan_only": "fan_only",
}

// evaluationParameterOf: category → action → parameter of the service call.
var evaluationParameterOf = map[string]map[string]map[string]evaluationParameter{
	"light": {"set": {"brightness_pct": {name: "brightness", scale: 1}}},
	"climate": {
		"set_temperature": {"temperature": {scale: 100, perUnit: map[string]string{celsius: "temperature", fahrenheit: "temperature_f"}}},
		// The HVAC modes of Home Assistant as modes of the vocabulary (Home Assistant
		// profile): heat_cool and auto both mean Matter's Auto; the requested mode is executed.
		"set_mode": {"hvac_mode": {name: "mode", values: hvacModes}},
	},
	"cover": {"set_position": {"position": {name: "position", scale: 1}}},
	"media": {"set_volume": {"volume_level": {name: "volume", scale: 100}}},
}

// maxExactInteger is the largest magnitude of an integer parameter (SPEC-v0 section 4.5).
const maxExactInteger = 1<<53 - 1

// evaluated is the value of the evaluation for the checked value of a service call: an
// integer in the unit of the vocabulary, by its exact decimal value, or the permitted
// value a string maps to. A value that is neither fails closed with an error.
func (p evaluationParameter) evaluated(callName string, value any, temperatureUnit string) (evaluator.Value, error) {
	if p.values != nil {
		s, _ := value.(string)
		mapped, ok := p.values[s]
		if !ok {
			return evaluator.Value{}, fmt.Errorf("%w: %s has no value of the vocabulary", errInvalidParams, callName)
		}
		return evaluator.StringValue(mapped), nil
	}
	n, ok := exactSteps(value, int64(p.scale))
	if !ok {
		return evaluator.Value{}, fmt.Errorf("parameter %s is no integer in the unit of the vocabulary: %v", callName, value)
	}
	return evaluator.IntegerValue(n), nil
}

// requestParameters checks the parameters of a vocabulary action before the decision and
// derives the parameters of the evaluation, in the units of the vocabulary. A value that
// cannot be expressed as an integer in that unit makes the request invalid (SPEC-v0
// section 4.5): an error wrapping errInvalidParams that names the parameter. A
// temperature while the household's unit is unknown gives errTemperatureUnitUnknown. An
// action outside the vocabulary has no parameters to check; the decision refuses it.
func requestParameters(category, action string, params map[string]any, temperatureUnit string) (map[string]evaluator.Value, error) {
	spec, ok := specFor(category, action, temperatureUnit)
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
	return evaluationParameters(category, action, data, temperatureUnit)
}

// checkTemperatureUnit refuses a temperature the vocabulary cannot express: one in a unit
// it has no parameter for, and one while the unit is unknown.
func checkTemperatureUnit(category, action string, params map[string]any, unit string) error {
	for callName, p := range evaluationParameterOf[category][action] {
		if _, present := params[callName]; !present || p.perUnit == nil || p.perUnit[unit] != "" {
			continue
		}
		if unit == "" {
			return errTemperatureUnitUnknown
		}
		return fmt.Errorf("%w: %s cannot be checked in a household that uses %s", errInvalidParams, callName, unit)
	}
	return nil
}

// evaluationParameters derives the parameters of the evaluation from the checked data of
// the service call. Every value has been checked to be a whole step of its unit; one that
// is not anyway fails closed with an error, it is never dropped.
func evaluationParameters(category, action string, data map[string]any, temperatureUnit string) (map[string]evaluator.Value, error) {
	var out map[string]evaluator.Value
	for callName, p := range evaluationParameterOf[category][action] {
		value, present := data[callName]
		if !present {
			continue
		}
		v, err := p.evaluated(callName, value, temperatureUnit)
		if err != nil {
			return nil, err
		}
		name := p.name
		if p.perUnit != nil {
			if name = p.perUnit[temperatureUnit]; name == "" {
				return nil, fmt.Errorf("parameter %s has no unit of the vocabulary in %q", callName, temperatureUnit)
			}
		}
		if out == nil {
			out = map[string]evaluator.Value{}
		}
		out[name] = v
	}
	return out, nil
}
