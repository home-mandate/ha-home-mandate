// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/home-mandate/home-mandate/internal/ha"
)

var (
	errInvalidParams = errors.New("invalid_params")
	errNotSupported  = errors.New("not_supported")
)

// param describes one allowed parameter of an action.
type param struct {
	kind     string // "int", "number" or "enum"
	min, max float64
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
		"read":            {},
		"set_temperature": {service: "set_temperature", params: map[string]param{"temperature": {kind: "number", min: -50, max: 100, required: true}}},
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
		"set_volume": {service: "volume_set", params: map[string]param{"volume_level": {kind: "number", min: 0, max: 1, required: true}}},
	},
	"sensor": {"read": {}},
	"scene":  {"read": {}, "activate": {service: "turn_on"}},
	"script": {"read": {}, "run": {service: "turn_on"}},
	"other":  {"read": {}, "set": {}}, // arbitrary entities have no safe mapping
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

// buildCall turns an allowed action into exactly one service call on the entity.
func buildCall(entityID, category, action string, params map[string]any) (ha.ServiceCall, error) {
	spec, ok := vocabulary[category][action]
	if !ok || action == "read" {
		return ha.ServiceCall{}, fmt.Errorf("%w: action %q", errInvalidParams, action)
	}
	data, err := checkParams(spec.params, params)
	if err != nil {
		return ha.ServiceCall{}, err
	}
	if category == "light" && action == "set" && len(data) == 0 {
		// Without a parameter "set" would be a plain turn_on, which the mandate may not allow.
		return ha.ServiceCall{}, fmt.Errorf("%w: set needs brightness_pct or color_temp_kelvin", errInvalidParams)
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
		f, ok := value.(float64)
		if !ok || math.IsNaN(f) || f < p.min || f > p.max || p.kind == "int" && f != math.Trunc(f) {
			return nil, fmt.Errorf("must be a %s between %g and %g", p.kind, p.min, p.max)
		}
		if p.kind == "int" {
			return int(f), nil
		}
		return f, nil
	}
}
