// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

var (
	errInvalidParams = errors.New("invalid_params")
	errNotSupported  = errors.New("not_supported")
)

// param describes one allowed parameter of an action.
type param struct {
	kind     string // "int", "number" or "enum"
	min, max float64
	// steps is how many steps of the vocabulary's unit make one unit of the service call
	// (100 for a volume level in percent); a number must be a whole step (SPEC-v0
	// section 4.5). An int is a whole step of 1.
	steps    float64
	rule     string // what the agent is told about a wrong number; empty: the default
	values   []string
	required bool
}

// actionSpec maps a vocabulary action to a Home Assistant service. An empty service
// means the action is evaluated and logged but cannot be executed in v0.1.
type actionSpec struct {
	service string
	params  map[string]param
}

// vocabulary is SPEC-v0 section 5 with the services of the reference implementation.
// "read" is answered by get_state and needs no service.
var vocabulary = map[string]map[string]actionSpec{
	"light": {
		"read": {}, "turn_on": {service: "turn_on"}, "turn_off": {service: "turn_off"},
		"set": {service: "turn_on", params: map[string]param{
			"brightness_pct":    {kind: "int", min: 0, max: 100},
			"color_temp_kelvin": {kind: "int", min: 1000, max: 12000},
		}},
	},
	"switch": {"read": {}, "turn_on": {service: "turn_on"}, "turn_off": {service: "turn_off"}},
	"climate": {
		"read": {},
		"set_temperature": {service: "set_temperature", params: map[string]param{"temperature": {kind: "number", min: -50, max: 100, steps: 100, required: true,
			rule: "must be a number between -50 and 100 in steps of 0.01 degrees"}}},
		"set_mode": {service: "set_hvac_mode", params: map[string]param{"hvac_mode": {kind: "enum", required: true,
			values: []string{"off", "heat", "cool", "heat_cool", "auto", "dry", "fan_only"}}}},
	},
	"cover": {
		"read": {}, "open": {service: "open_cover"}, "close": {service: "close_cover"}, "stop": {service: "stop_cover"},
		"set_position": {service: "set_cover_position", params: map[string]param{"position": {kind: "int", min: 0, max: 100, required: true}}},
	},
	"gate": {"read": {}, "open": {service: "open_cover"}, "close": {service: "close_cover"}},
	"lock": {"read": {}, "lock": {service: "lock"}, "unlock": {service: "unlock"}, "open": {service: "open"}},
	"alarm": {
		"read":   {},
		"arm":    {service: "alarm_arm_", params: map[string]param{"mode": {kind: "enum", values: []string{"home", "away", "night"}}}},
		"disarm": {service: "alarm_disarm"},
	},
	"camera": {"read": {}, "snapshot": {}}, // the image is only available over the HTTP API
	"media": {
		"read": {}, "turn_on": {service: "turn_on"}, "turn_off": {service: "turn_off"},
		"play": {service: "media_play"}, "pause": {service: "media_pause"},
		"set_volume": {service: "volume_set", params: map[string]param{"volume_level": {kind: "number", min: 0, max: 1, steps: 100, required: true,
			rule: "must be a whole percent, 0 to 1 in steps of 0.01 (e.g. 0.33)"}}},
	},
	"sensor": {"read": {}},
	"scene":  {"read": {}, "activate": {service: "turn_on"}},
	"script": {"read": {}, "run": {service: "turn_on"}},
	"other":  {"read": {}, "set": {}}, // arbitrary entities have no safe mapping
}

// Actions returns the vocabulary actions of a category (SPEC-v0 section 5), sorted; the
// UI's device catalog offers exactly these. Unknown categories have none.
func Actions(category string) []string {
	return actionsOf(category)
}

// DeviceActions returns the actions of a category that an entity with the attributes of
// its state can carry out (issue #89), sorted; the UI's device catalog offers these.
func DeviceActions(category string, attrs map[string]any) []string {
	return actionsItCanDo(category, attrs)
}

// actionsOf returns the vocabulary of a category in a fixed order.
func actionsOf(category string) []string {
	var actions []string
	for a := range vocabulary[category] {
		actions = append(actions, a)
	}
	slices.Sort(actions)
	return actions
}

// fahrenheitTemperature is the temperature of set_temperature in a household that uses
// degrees Fahrenheit: the same range as in degrees Celsius, in its own unit.
var fahrenheitTemperature = param{kind: "number", min: -58, max: 212, steps: 100, required: true,
	rule: "must be a number between -58 and 212 in steps of 0.01 degrees"}

// specFor is the vocabulary entry of an action with its parameters in the household's
// temperature unit (spec #21 part B).
func specFor(category, action, temperatureUnit string) (actionSpec, bool) {
	spec, ok := vocabulary[category][action]
	if !ok || temperatureUnit != fahrenheit {
		return spec, ok
	}
	if _, has := spec.params["temperature"]; !has {
		return spec, ok
	}
	params := maps.Clone(spec.params)
	params["temperature"] = fahrenheitTemperature
	spec.params = params
	return spec, ok
}

// buildCall turns an allowed action into exactly one service call on the entity; a
// temperature is in the household's unit (temperatureUnit).
func buildCall(entityID, category, action string, params map[string]any, temperatureUnit string) (ha.ServiceCall, error) {
	spec, ok := specFor(category, action, temperatureUnit)
	if !ok || action == "read" {
		return ha.ServiceCall{}, fmt.Errorf("%w: action %q", errInvalidParams, action)
	}
	data, err := callData(category, action, spec, params)
	if err != nil {
		return ha.ServiceCall{}, err
	}
	if spec.service == "" {
		return ha.ServiceCall{}, errNotSupported
	}
	domain, _, _ := strings.Cut(entityID, ".")
	service := spec.service
	if category == "alarm" && action == "arm" {
		mode, _ := data["mode"].(string)
		if mode == "" {
			mode = "away"
		}
		service += mode
		data = nil
	}
	return ha.ServiceCall{Domain: domain, Service: service, EntityID: entityID, Data: data}, nil
}

// callData checks the parameters of a vocabulary action and returns the data of its
// service call, every number as the canonical value of its step.
func callData(category, action string, spec actionSpec, params map[string]any) (map[string]any, error) {
	data, err := checkParams(spec.params, params)
	if err != nil {
		return nil, err
	}
	if category == "light" && action == "set" && len(data) == 0 {
		// Without a parameter "set" would be a plain turn_on, which the mandate may not allow.
		return nil, fmt.Errorf("%w: set needs brightness_pct or color_temp_kelvin", errInvalidParams)
	}
	return data, nil
}

// checkParams accepts only the declared parameters, with the declared types and ranges.
func checkParams(spec map[string]param, params map[string]any) (map[string]any, error) {
	data := map[string]any{}
	for name, value := range params {
		p, ok := spec[name]
		if !ok {
			return nil, fmt.Errorf("%w: unknown parameter %q", errInvalidParams, name)
		}
		v, err := p.check(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %w", errInvalidParams, name, err)
		}
		data[name] = v
	}
	for name, p := range spec {
		if _, ok := params[name]; p.required && !ok {
			return nil, fmt.Errorf("%w: %s is required", errInvalidParams, name)
		}
	}
	return data, nil
}

func (p param) check(value any) (any, error) {
	switch p.kind {
	case "enum":
		s, ok := value.(string)
		if !ok || !slices.Contains(p.values, s) {
			return nil, fmt.Errorf("must be one of %s", strings.Join(p.values, ", "))
		}
		return s, nil
	default:
		steps := int64(p.steps)
		if p.kind == "int" || steps == 0 {
			steps = 1
		}
		n, ok := exactSteps(value, steps)
		v := float64(n) / float64(steps)
		if !ok || v < p.min || v > p.max {
			return nil, errors.New(p.message())
		}
		if p.kind == "int" {
			return int(n), nil
		}
		return v, nil
	}
}

// message tells the agent which numbers the parameter takes.
func (p param) message() string {
	if p.rule != "" {
		return p.rule
	}
	article := "a"
	if p.kind == "int" {
		article, p.kind = "an", "integer"
	}
	return fmt.Sprintf("must be %s %s between %g and %g", article, p.kind, p.min, p.max)
}

// jsonNumber is a JSON number (RFC 8259) whose exponent has at most two digits: far
// beyond any value of the vocabulary, and small enough to compute with exactly.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,30})(\.[0-9]{1,30})?([eE][+-]?[0-9]{1,2})?$`)

// exactSteps returns value in steps of the vocabulary's unit (steps per unit of the
// service call) and whether it is a whole number of them by its exact decimal value
// (SPEC-v0 section 4.5): 19.99 degrees are 1999 hundredths, 50.00000000000000000001
// percent is no whole percent. Numbers arrive as json.Number from the agent's arguments;
// a float64 counts by its shortest decimal form. The magnitude is at most 2^53 − 1 steps.
func exactSteps(value any, steps int64) (int64, bool) {
	var text string
	switch v := value.(type) {
	case json.Number:
		text = string(v)
	case float64:
		text = strconv.FormatFloat(v, 'g', -1, 64)
	case int:
		text = strconv.Itoa(v)
	default:
		return 0, false
	}
	if !jsonNumber.MatchString(text) {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, false
	}
	r.Mul(r, new(big.Rat).SetInt64(steps))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	n := r.Num().Int64()
	if n > maxExactInteger || n < -maxExactInteger {
		return 0, false
	}
	return n, true
}
