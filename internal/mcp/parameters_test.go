// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
		setpointRule     = "invalid_params: temperature, or target_temp_low with target_temp_high, is required"
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
		{name: "unknown unit, temperature missing", category: "climate", action: "set_temperature", params: nil, unit: "", wantErr: setpointRule},
		// Spec #84: a range setpoint is temperature_low with temperature_high, in the household's unit.
		{name: "range setpoint", category: "climate", action: "set_temperature", params: map[string]any{"target_temp_low": 18.0, "target_temp_high": 22.5}, unit: "°C",
			want: map[string]int64{"temperature_low": 1800, "temperature_high": 2250}},
		{name: "range setpoint in Fahrenheit", category: "climate", action: "set_temperature", params: map[string]any{"target_temp_low": 65.0, "target_temp_high": 72.0}, unit: "°F",
			want: map[string]int64{"temperature_low_f": 6500, "temperature_high_f": 7200}},
		{name: "range end finer than the unit", category: "climate", action: "set_temperature", params: map[string]any{"target_temp_low": 18.005, "target_temp_high": 22.0}, unit: "°C",
			wantErr: "invalid_params: target_temp_low must be a number between -50 and 100 in steps of 0.01 degrees"},
		{name: "half a range", category: "climate", action: "set_temperature", params: map[string]any{"target_temp_low": 18.0}, unit: "°C",
			wantErr: "invalid_params: target_temp_low and target_temp_high go together"},
		{name: "a temperature and a range", category: "climate", action: "set_temperature", params: map[string]any{"temperature": 21.0, "target_temp_low": 18.0, "target_temp_high": 22.0}, unit: "°C",
			wantErr: "invalid_params: temperature or target_temp_low with target_temp_high, not both"},
		{name: "range while the unit is unknown", category: "climate", action: "set_temperature", params: map[string]any{"target_temp_low": 18.0, "target_temp_high": 22.0}, unit: "", unitUnknown: true},
		{name: "arm without a mode", category: "alarm", action: "arm", params: nil, unit: "°C", wantErr: "invalid_params: mode is required"},
		{name: "arm with an unknown mode", category: "alarm", action: "arm", params: map[string]any{"mode": "panic"}, unit: "°C",
			wantErr: "invalid_params: mode must be one of home, away, night, vacation, custom_bypass"},
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

// What is executed is exactly what was evaluated: the value the agent gave, by its exact
// decimal value. A number that is no whole step of the unit is refused, also where binary
// floating point would round it to one (SPEC-v0 section 4.5).
func TestNumbersAreReadExactly(t *testing.T) {
	tests := []struct {
		name     string
		category string
		action   string
		param    string
		sent     any
		want     any // nil: refused
		evaluate int64
	}{
		{"volume", "media", "set_volume", "volume_level", json.Number("0.29"), 0.29, 29},
		{"volume as float", "media", "set_volume", "volume_level", 0.29, 0.29, 29},
		{"volume a little off", "media", "set_volume", "volume_level", json.Number("0.33000000099"), nil, 0},
		{"temperature", "climate", "set_temperature", "temperature", json.Number("19.99"), 19.99, 1999},
		{"temperature a little off", "climate", "set_temperature", "temperature", json.Number("19.990000001"), nil, 0},
		{"exponent", "light", "set", "brightness_pct", json.Number("2e1"), 20, 20},
		{"trailing zeros", "light", "set", "brightness_pct", json.Number("20.000"), 20, 20},
		{"a fraction floating point rounds away", "light", "set", "brightness_pct", json.Number("50.00000000000000000001"), nil, 0},
		{"a huge exponent", "light", "set", "brightness_pct", json.Number("1e400"), nil, 0},
		{"a tiny exponent", "light", "set", "brightness_pct", json.Number("1e-400"), nil, 0},
		{"no JSON number", "light", "set", "brightness_pct", json.Number("0x10"), nil, 0},
		{"out of range", "cover", "set_position", "position", json.Number("101"), nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entity := tt.category + ".x"
			call, err := buildCall(entity, tt.category, tt.action, map[string]any{tt.param: tt.sent}, "°C")
			if tt.want == nil {
				if err == nil {
					t.Fatalf("sent %v: %v, want refused", tt.sent, call.Data)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if call.Data[tt.param] != tt.want {
				t.Errorf("sent %v, want %v", call.Data[tt.param], tt.want)
			}
			got, err := requestParameters(tt.category, tt.action, map[string]any{tt.param: tt.sent}, "°C")
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range got {
				if n, _ := v.AsInteger(); n != tt.evaluate {
					t.Errorf("evaluated %v, want %d", v, tt.evaluate)
				}
			}
		})
	}
}

// The HVAC modes of Home Assistant are evaluated as mode (Home Assistant profile); the
// requested mode is executed unchanged.
func TestClimateModes(t *testing.T) {
	for mode, want := range map[string]string{"off": "off", "heat": "heat", "cool": "cool", "heat_cool": "auto", "auto": "auto", "dry": "dry", "fan_only": "fan_only"} {
		got, err := requestParameters("climate", "set_mode", map[string]any{"hvac_mode": mode}, "°C")
		if s, _ := got["mode"].AsString(); err != nil || s != want || len(got) != 1 {
			t.Errorf("%s: %v, %v; want mode %s", mode, got, err, want)
		}
		call, err := buildCall("climate.x", "climate", "set_mode", map[string]any{"hvac_mode": mode}, "°C")
		if err != nil || call.Data["hvac_mode"] != mode || call.Service != "set_hvac_mode" {
			t.Errorf("%s: call %+v, %v", mode, call, err)
		}
	}
	for _, mode := range []any{"eco", "Heat", 4, nil} {
		if got, err := requestParameters("climate", "set_mode", map[string]any{"hvac_mode": mode}, "°C"); err == nil {
			t.Errorf("%v: %v, want refused", mode, got)
		}
	}
	if _, err := requestParameters("climate", "set_mode", nil, "°C"); err == nil {
		t.Error("set_mode without a mode was accepted")
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

// params is read from the arguments as sent: exactly the key "params", the last of
// duplicates as the SDK validated it, numbers as json.Number.
func TestExactParams(t *testing.T) {
	read := func(args string) (map[string]any, error) {
		return exactParams(&sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(args)}})
	}
	for args, want := range map[string]map[string]any{
		`{"params":{"brightness_pct":50.00000000000000000001}}`: {"brightness_pct": json.Number("50.00000000000000000001")},
		`{"params":{"a":1},"params":{"b":2}}`:                   {"b": json.Number("2")},
		`{"Params":{"a":1}}`:                                    nil,
		`{"entity_id":"light.x"}`:                               nil,
		`{"params":null}`:                                       nil,
		`{}`:                                                    nil,
	} {
		got, err := read(args)
		if err != nil || !maps.Equal(got, want) {
			t.Errorf("%s: %v, %v; want %v", args, got, err, want)
		}
	}
	for _, args := range []string{`[]`, `{"params":[1]}`, `{"params":"x"}`} {
		if got, err := read(args); err == nil {
			t.Errorf("%s: %v, want an error", args, got)
		}
	}
	if got, err := exactParams(nil); got != nil || err != nil {
		t.Errorf("without a request: %v, %v", got, err)
	}
}

func TestExactStepsEdges(t *testing.T) {
	for value, want := range map[any]bool{
		json.Number("1234567890123456789012345678901"):  false, // 31 digits: a number, out of range
		json.Number("12345678901234567890123456789012"): false, // 32 digits: no number here
		json.Number("-0"):   true,
		json.Number("2E1"):  true,
		json.Number("2e+1"): true,
		true:                false,
		"20":                false,
		math.NaN():          false,
	} {
		if _, ok := exactSteps(value, 1); ok != want {
			t.Errorf("exactSteps(%v) = %t, want %t", value, ok, want)
		}
	}
}

// Spec #84: the arming mode is a parameter of the evaluation, as the agent names it; the
// vocabulary's five modes are Home Assistant's five arming services.
func TestArmModeIsEvaluated(t *testing.T) {
	for _, mode := range []string{"home", "away", "night", "vacation", "custom_bypass"} {
		got, err := requestParameters("alarm", "arm", map[string]any{"mode": mode}, "°C")
		if err != nil || len(got) != 1 || got["mode"] != evaluator.StringValue(mode) {
			t.Errorf("%s: parameters = %v, %v", mode, got, err)
		}
		call, err := buildCall("alarm_control_panel.home", "alarm", "arm", map[string]any{"mode": mode}, "°C")
		if err != nil || call.Service != "alarm_arm_"+mode || call.Data != nil {
			t.Errorf("%s: call = %+v, %v", mode, call, err)
		}
	}
}

// A range setpoint is executed as Home Assistant's target_temp_low and target_temp_high,
// in the household's unit and its range: 101 °F is a valid upper end.
func TestRangeSetpointIsExecutedInTheHouseholdsUnit(t *testing.T) {
	params := map[string]any{"target_temp_low": 60.0, "target_temp_high": 101.0}
	call, err := buildCall("climate.living_room", "climate", "set_temperature", params, "°F")
	if err != nil || call.Service != "set_temperature" || call.Data["target_temp_low"] != 60.0 || call.Data["target_temp_high"] != 101.0 {
		t.Fatalf("°F: call = %+v, %v", call, err)
	}
	if _, err := buildCall("climate.living_room", "climate", "set_temperature", params, "°C"); !errors.Is(err, errInvalidParams) {
		t.Fatalf("°C: error = %v, want invalid_params", err)
	}
}

// Spec #84: a range setpoint goes through like a single one: the human sees both ends with
// their unit, Home Assistant gets target_temp_low and target_temp_high, the audit entry
// has temperature_low and temperature_high; a limit on one end is named in the agent's
// terms.
func TestRangeSetpointEndToEnd(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-heating", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"}, "decision": "ask"},
		}
	})
	f.journal, h.approver = h.journal, f
	h.url = h.serve(h.log)
	s := h.session()
	_, errText := h.call(s, "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_temperature",
		"params": map[string]any{"target_temp_low": 18.0, "target_temp_high": 22.5}})
	if errText != "" {
		t.Fatalf("perform_action = %q", errText)
	}
	if reqs := f.requests(); len(reqs) != 1 || reqs[0].Params["target_temp_low"] != "18 °C" || reqs[0].Params["target_temp_high"] != "22.5 °C" {
		t.Errorf("shown = %v", reqs)
	}
	if calls := h.ha.recorded(); len(calls) != 1 || calls[0].Service != "set_temperature" || calls[0].Data["target_temp_low"] != 18.0 || calls[0].Data["target_temp_high"] != 22.5 {
		t.Errorf("calls = %+v", calls)
	}
	if e := h.lastEntry(); path(e, "request", "parameters", "temperature_low") != float64(1800) || path(e, "request", "parameters", "temperature_high") != float64(2250) {
		t.Errorf("audit entry = %v", e)
	}
}

// A limit on one end of a range setpoint is named in the agent's terms.
func TestRangeSetpointLimitIsNamed(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{
			map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
			map[string]any{"id": "r-heating", "resource": map[string]any{"category": "climate"}, "actions": []any{"set_temperature"}, "decision": "allow",
				"constraints": map[string]any{"temperature_high": map[string]any{"max": 2400}}},
		}
	})
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "climate.living_room", "action": "set_temperature",
		"params": map[string]any{"target_temp_low": 18.0, "target_temp_high": 25.0}})
	if errText != "denied: no_match: allowed only with target_temp_high up to 24 °C" {
		t.Errorf("above the limit: %q", errText)
	}
}
