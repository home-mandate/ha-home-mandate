// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"maps"
	"testing"
)

func TestEvaluationParameters(t *testing.T) {
	tests := []struct {
		name             string
		category, action string
		params           map[string]any
		unit             string
		want             map[string]int64
	}{
		{"brightness in percent", "light", "set", map[string]any{"brightness_pct": 40.0}, "°C", map[string]int64{"brightness": 40}},
		{"colour only: no vocabulary parameter", "light", "set", map[string]any{"color_temp_kelvin": 3000.0}, "°C", nil},
		{"temperature in hundredths of a degree", "climate", "set_temperature", map[string]any{"temperature": 21.5}, "°C", map[string]int64{"temperature": 2150}},
		{"whole degrees", "climate", "set_temperature", map[string]any{"temperature": 21.0}, "", map[string]int64{"temperature": 2100}},
		{"negative temperature", "climate", "set_temperature", map[string]any{"temperature": -5.25}, "°C", map[string]int64{"temperature": -525}},
		{"float noise is not a fraction", "climate", "set_temperature", map[string]any{"temperature": 19.99}, "°C", map[string]int64{"temperature": 1999}},
		{"finer than the unit: not expressible", "climate", "set_temperature", map[string]any{"temperature": 21.555}, "°C", nil},
		{"Fahrenheit households: not converted", "climate", "set_temperature", map[string]any{"temperature": 70.0}, "°F", nil},
		{"position", "cover", "set_position", map[string]any{"position": 30.0}, "°C", map[string]int64{"position": 30}},
		{"volume level to percent", "media", "set_volume", map[string]any{"volume_level": 0.37}, "°C", map[string]int64{"volume": 37}},
		{"volume finer than a percent", "media", "set_volume", map[string]any{"volume_level": 0.375}, "°C", nil},
		{"full volume", "media", "set_volume", map[string]any{"volume_level": 1.0}, "°C", map[string]int64{"volume": 100}},
		{"action without parameters", "light", "turn_on", nil, "°C", nil},
		{"wrong type", "cover", "set_position", map[string]any{"position": "30"}, "°C", nil},
		{"unknown category", "toaster", "set", map[string]any{"brightness_pct": 40.0}, "°C", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluationParameters(tt.category, tt.action, tt.params, tt.unit); !maps.Equal(got, tt.want) {
				t.Errorf("evaluationParameters = %v, want %v", got, tt.want)
			}
		})
	}
}
