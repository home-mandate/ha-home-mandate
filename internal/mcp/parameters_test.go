// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"errors"
	"maps"
	"math"
	"testing"
)

// SPEC-v0 section 4.5: a value that cannot be expressed as an integer in the unit of the
// vocabulary makes the request invalid; it is never dropped or rounded (#44).
func TestRequestParameters(t *testing.T) {
	const (
		volumeRule      = "invalid_params: volume_level must be a whole percent, 0 to 1 in steps of 0.01 (e.g. 0.33)"
		temperatureRule = "invalid_params: temperature must be a number between -50 and 100 in steps of 0.01 degrees"
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
		{name: "Fahrenheit household: refused", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 70.0}, unit: "°F",
			wantErr: "invalid_params: temperature cannot be checked in a household that uses °F yet"},
		{name: "unexpected unit: refused", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.0}, unit: "K",
			wantErr: "invalid_params: temperature cannot be checked in a household that uses K yet"},
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
			if !maps.Equal(got, tt.want) {
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
			call, err := buildCall(tt.entity, categories[tt.action], tt.action, map[string]any{tt.param: tt.sent})
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
			if got, err := evaluationParameters(tt.category, tt.action, tt.data); err == nil {
				t.Errorf("evaluationParameters = %v, want an error", got)
			}
		})
	}
	if got, err := evaluationParameters("cover", "set_position", map[string]any{"position": 30}); err != nil || got["position"] != 30 {
		t.Errorf("checked integer: %v, %v", got, err)
	}
}
