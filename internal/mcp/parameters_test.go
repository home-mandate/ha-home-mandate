// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"errors"
	"maps"
	"math"
	"testing"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
)

// SPEC-v0 section 4.5: a value that cannot be expressed as an integer in the unit of the
// vocabulary makes the request invalid; it is never dropped or rounded (#44).
func TestRequestParameters(t *testing.T) {
	const (
		volumeRule       = "invalid_params: volume_level must be a whole percent, 0 to 1 in steps of 0.01 (e.g. 0.33)"
		temperatureRule  = "invalid_params: temperature must be a number between -50 and 100 in steps of 0.01 degrees"
		fahrenheitRule   = "invalid_params: temperature must be a number between -58 and 212 in steps of 0.01 degrees"
		unexpectedUnitIn = "invalid_params: temperature cannot be checked in a household that uses K"
	)
	tests := []struct {
		name             string
		category, action string
		params           map[string]any
		unit             string
		want             map[string]int64
		wantErr          string // the exact error text; empty: no error
		unitUnknown      bool   // the error is errTemperatureUnitUnknown
	}{
		{name: "brightness in percent", category: "light", action: "set", params: map[string]any{"brightness_pct": 40.0}, unit: "°C", want: map[string]int64{"brightness": 40}},
		{name: "brightness finer than a percent", category: "light", action: "set", params: map[string]any{"brightness_pct": 40.5}, unit: "°C",
			wantErr: "invalid_params: brightness_pct must be an integer between 0 and 100"},
		{name: "colour only: no vocabulary parameter", category: "light", action: "set", params: map[string]any{"color_temp_kelvin": 3000.0}, unit: "°C"},
		{name: "temperature in hundredths of a degree", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.5}, unit: "°C", want: map[string]int64{"temperature": 2150}},
		{name: "whole degrees", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.0}, unit: "°C", want: map[string]int64{"temperature": 2100}},
		{name: "negative temperature", category: "climate", action: "set_temperature", params: map[string]any{"temperature": -5.25}, unit: "°C", want: map[string]int64{"temperature": -525}},
		{name: "float noise is not a fraction", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 19.99}, unit: "°C", want: map[string]int64{"temperature": 1999}},
		{name: "temperature finer than the unit", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.555}, unit: "°C", wantErr: temperatureRule},
		{name: "temperature out of range", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 150.0}, unit: "°C", wantErr: temperatureRule},
		// Spec #21 part B: a °F household gives temperature_f, never a converted temperature.
		{name: "Fahrenheit household", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 70.0}, unit: "°F", want: map[string]int64{"temperature_f": 7000}},
		{name: "Fahrenheit above the Celsius range", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 101.5}, unit: "°F", want: map[string]int64{"temperature_f": 10150}},
		{name: "Fahrenheit finer than the unit", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 70.005}, unit: "°F", wantErr: fahrenheitRule},
		{name: "Fahrenheit out of range", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 250.0}, unit: "°F", wantErr: fahrenheitRule},
		{name: "unexpected unit: refused", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.0}, unit: "K", wantErr: unexpectedUnitIn},
		{name: "unknown unit: not the agent's fault", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.0}, unit: "", unitUnknown: true},
		{name: "unknown unit, temperature missing", category: "climate", action: "set_temperature", params: nil, unit: "", wantErr: "invalid_params: temperature is required"},
		{name: "position", category: "cover", action: "set_position", params: map[string]any{"position": 30.0}, unit: "°C", want: map[string]int64{"position": 30}},
		{name: "position as text", category: "cover", action: "set_position", params: map[string]any{"position": "30"}, unit: "°C",
			wantErr: "invalid_params: position must be an integer between 0 and 100"},
		{name: "volume level to percent", category: "media", action: "set_volume", params: map[string]any{"volume_level": 0.37}, unit: "°C", want: map[string]int64{"volume": 37}},
		{name: "volume with float noise", category: "media", action: "set_volume", params: map[string]any{"volume_level": 0.29}, unit: "°C", want: map[string]int64{"volume": 29}},
		{name: "volume finer than a percent", category: "media", action: "set_volume", params: map[string]any{"volume_level": 0.335}, unit: "°C", wantErr: volumeRule},
		{name: "volume above full", category: "media", action: "set_volume", params: map[string]any{"volume_level": 1.01}, unit: "°C", wantErr: volumeRule},
		{name: "full volume", category: "media", action: "set_volume", params: map[string]any{"volume_level": 1.0}, unit: "°C", want: map[string]int64{"volume": 100}},
		{name: "silence", category: "media", action: "set_volume", params: map[string]any{"volume_level": 0.0}, unit: "°C", want: map[string]int64{"volume": 0}},
		{name: "volume in a Fahrenheit household", category: "media", action: "set_volume", params: map[string]any{"volume_level": 0.5}, unit: "°F", want: map[string]int64{"volume": 50}},
		{name: "action without parameters", category: "light", action: "turn_on", unit: "°C"},
		{name: "action outside the vocabulary: left to the decision", category: "light", action: "explode", params: map[string]any{"x": 1.0}, unit: "°C"},
		{name: "unknown category: left to the decision", category: "toaster", action: "set", params: map[string]any{"brightness_pct": 40.0}, unit: "°C"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requestParameters(tt.category, tt.action, tt.params, tt.unit)
			switch {
			case tt.unitUnknown:
				if !errors.Is(err, errTemperatureUnitUnknown) || errors.Is(err, errInvalidParams) {
					t.Fatalf("error = %v, want only errTemperatureUnitUnknown", err)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr || !errors.Is(err, errInvalidParams) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("error = %v", err)
			}
			if !maps.Equal(got, integers(tt.want)) {
				t.Errorf("parameters = %v, want %v", got, tt.want)
			}
		})
	}
}

// What is executed is exactly what was evaluated: the canonical value of the step, not
// the binary noise the agent sent.
func TestCanonicalValueIsSent(t *testing.T) {
	tests := []struct {
		name   string
		entity string
		action string
		param  string
		sent   float64
		want   float64
	}{
		{"volume with float noise", "media_player.kitchen", "set_volume", "volume_level", 0.29, 0.29},
		{"volume a little off", "media_player.kitchen", "set_volume", "volume_level", 0.33000000099, 0.33},
		{"temperature", "climate.living_room", "set_temperature", "temperature", 19.990000001, 19.99},
	}
	categories := map[string]string{"set_volume": "media", "set_temperature": "climate"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call, err := buildCall(tt.entity, categories[tt.action], tt.action, map[string]any{tt.param: tt.sent}, "°C")
			if err != nil {
				t.Fatal(err)
			}
			got, _ := call.Data[tt.param].(float64)
			if math.Float64bits(got) != math.Float64bits(tt.want) {
				t.Errorf("sent %v, want %v", got, tt.want)
			}
		})
	}
}

// Values reaching the evaluation have been checked; one that is not expressible anyway
// fails closed instead of being dropped.
func TestEvaluationParametersFailClosed(t *testing.T) {
	tests := []struct {
		name             string
		category, action string
		data             map[string]any
	}{
		{"finer than the unit", "media", "set_volume", map[string]any{"volume_level": 0.335}},
		{"not a number", "cover", "set_position", map[string]any{"position": "30"}},
		{"not finite", "climate", "set_temperature", map[string]any{"temperature": math.Inf(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := evaluationParameters(tt.category, tt.action, tt.data, "°C"); err == nil {
				t.Errorf("evaluationParameters = %v, want an error", got)
			}
		})
	}
	if got, err := evaluationParameters("cover", "set_position", map[string]any{"position": 30}, "°C"); err != nil || got["position"] != evaluator.IntegerValue(30) {
		t.Errorf("checked integer: %v, %v", got, err)
	}
}

// A temperature is executed in the household's unit, within that unit's range: 101 °F is
// a valid setting, 101 °C is not (spec #21 part B).
func TestTemperatureRangePerUnit(t *testing.T) {
	if _, err := buildCall("climate.living_room", "climate", "set_temperature", map[string]any{"temperature": 101.0}, "°F"); err != nil {
		t.Errorf("101 °F: %v", err)
	}
	if _, err := buildCall("climate.living_room", "climate", "set_temperature", map[string]any{"temperature": 101.0}, "°C"); !errors.Is(err, errInvalidParams) {
		t.Errorf("101 °C: %v", err)
	}
}

// Spec #21 part B end to end: a limit in degrees Fahrenheit applies in a °F household and
// never in a °C one, where the request carries temperature instead.
func TestFahrenheitLimit(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-heating-f", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"}, "decision": "allow",
				"constraints": map[string]any{"temperature_f": map[string]any{"min": 6000, "max": 7500}}},
		}
	})
	s := h.session()
	for _, tc := range []struct {
		unit    string
		value   float64
		wantErr string
	}{
		{"°F", 70, ""},
		{"°F", 75.01, "denied: no_match: allowed only with temperature 60 to 75 °F"},
		{"°C", 21, "denied: no_match"},
	} {
		h.setUnit(tc.unit)
		_, errText := h.call(s, "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_temperature",
			"params": map[string]any{"temperature": tc.value}})
		if errText != tc.wantErr {
			t.Errorf("%v %s: %q, want %q", tc.value, tc.unit, errText, tc.wantErr)
		}
	}
}

// A temperature confirmed in °C is not executed once the household uses °F (and the other
// way round); the human sees the unit with the value.
func TestTemperatureUnitChangedWhileTheHumanDecides(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-heating", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"}, "decision": "ask"},
		}
	})
	f.journal, h.approver = h.journal, f
	h.url = h.serve(h.log)
	f.during = func() { h.setUnit("°F") }
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_temperature",
		"params": map[string]any{"temperature": 21.0}})
	if errText != "unavailable" || len(h.ha.recorded()) != 0 {
		t.Errorf("perform_action = %q, %d calls", errText, len(h.ha.recorded()))
	}
	if e := h.lastEntry(); path(e, "result", "error") != errTemperatureUnitChanged {
		t.Errorf("audit entry = %v", e)
	}
	if reqs := f.requests(); len(reqs) != 1 || reqs[0].Params["temperature"] != "21 °C" {
		t.Errorf("shown = %v", reqs[0].Params)
	}
}

// integers are the parameters of an evaluation that are all integers; nil for none.
func integers(in map[string]int64) map[string]evaluator.Value {
	if in == nil {
		return nil
	}
	out := make(map[string]evaluator.Value, len(in))
	for name, n := range in {
		out[name] = evaluator.IntegerValue(n)
	}
	return out
}
