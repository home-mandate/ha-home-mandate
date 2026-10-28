// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// FuzzToolArguments: the arguments of perform_action as an agent sends them, through the
// same steps as the gateway (exactParams, requestParameters, buildCall), for every
// action of the vocabulary. Nothing panics, every refusal is a defined error, a call that
// can be built has a fingerprint and only declared parameters, and the value that would
// be executed is the value that was evaluated.
func FuzzToolArguments(f *testing.F) {
	type target struct{ category, action string }
	var targets []target
	categories := make([]string, 0, len(vocabulary))
	for c := range vocabulary {
		categories = append(categories, c)
	}
	slices.Sort(categories)
	for _, c := range categories {
		for _, a := range actionsOf(c) {
			targets = append(targets, target{c, a})
		}
	}
	units := []string{celsius, fahrenheit, "", "K"}
	for _, seed := range []string{
		`{"entity_id":"light.kitchen","action":"set","params":{"brightness_pct":40}}`,
		`{"params":{"brightness_pct":50.00000000000000000001}}`,
		`{"params":{"temperature":21.5}}`, `{"params":{"temperature":-5.25e0}}`, `{"params":{"temperature":1e99}}`,
		`{"params":{"hvac_mode":"heat_cool"}}`, `{"params":{"position":"30"}}`, `{"params":{"volume_level":0.29}}`,
		`{"params":{"mode":"night"}}`, `{"params":{"code":"1234"}}`, `{"params":null}`, `{"params":[]}`,
		`{"params":{"a":1},"params":{"temperature":20}}`, `{"params":{"temperature":20,"temperature":"x"}}`,
		`[]`, `{"params":{"color_temp_kelvin":3000,"brightness_pct":-0}}`,
	} {
		f.Add([]byte(seed), uint8(0), uint8(0))
	}
	f.Fuzz(func(t *testing.T, args []byte, which, unitIndex uint8) {
		tg := targets[int(which)%len(targets)]
		unit := units[int(unitIndex)%len(units)]
		params, err := exactParams(&sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(args)}})
		if err != nil {
			return
		}
		evaluated, evalErr := requestParameters(tg.category, tg.action, params, unit)
		if evalErr != nil && !errors.Is(evalErr, errInvalidParams) && !errors.Is(evalErr, errTemperatureUnitUnknown) {
			t.Fatalf("%s %s: undefined error %v", tg.category, tg.action, evalErr)
		}
		call, err := buildCall("x.fuzz", tg.category, tg.action, params, unit)
		if err != nil {
			if !errors.Is(err, errInvalidParams) && !errors.Is(err, errNotSupported) {
				t.Fatalf("%s %s: undefined error %v", tg.category, tg.action, err)
			}
			return
		}
		if call.EntityID != "x.fuzz" || call.Domain != "x" || call.Service == "" {
			t.Fatalf("call %+v", call)
		}
		// Every call has a fingerprint, also one with a fractional number (21.5 degrees, a
		// volume of 0.4; issue #108, the seed in testdata keeps the case).
		if callDigest(call) == "" {
			t.Fatalf("no fingerprint for %+v", call)
		}
		spec, _ := specFor(tg.category, tg.action, unit)
		for name := range call.Data {
			if _, declared := spec.params[name]; !declared {
				t.Fatalf("undeclared parameter %q in %+v", name, call)
			}
		}
		if evalErr != nil {
			return // the decision refuses it (performAction passes evalErr on)
		}
		for callName, p := range evaluationParameterOf[tg.category][tg.action] {
			value, present := call.Data[callName]
			if !present {
				continue
			}
			name := p.name
			if p.perUnit != nil {
				name = p.perUnit[unit]
			}
			got, ok := evaluated[name]
			if !ok {
				t.Fatalf("%s is executed but was not evaluated: %+v", callName, call)
			}
			if p.values != nil {
				if text, _ := got.AsString(); text != p.values[value.(string)] {
					t.Fatalf("%s: executed %v, evaluated %q", callName, value, text)
				}
				continue
			}
			want, _ := exactSteps(value, int64(p.scale))
			if n, _ := got.AsInteger(); n != want {
				t.Fatalf("%s: executed %v, evaluated %d", callName, value, n)
			}
		}
	})
}

// FuzzGatewayRequest: any body an agent posts to the MCP endpoint, with or without its
// token, is answered without a panic and without a 5xx, and it reaches Home Assistant
// only for what the mandate allows: the voice assistant's lights and thermostat (locks
// need an approval, which this gateway cannot ask for; cameras and the alarm are
// denied). In the inputs, ENTITY stands for a device of the catalog, so that the fuzzer
// reaches the decisions behind it.
func FuzzGatewayRequest(f *testing.F) {
	// The hourly limit runs on the gateway's clock, which moves on by more than one
	// action's share of the limit per input: the limit never refuses everything.
	h := newHarness(f, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 1000} })
	h.limiterNow = h.now.Now
	h.serve(h.log)
	handler := h.gw.Handler()
	entities := []string{"light.kitchen", "lock.front_door", "camera.porch", "alarm_control_panel.home", "climate.living_room", "number.wallbox", "light.nowhere"}
	allowed := map[string]bool{"light.kitchen": true, "climate.living_room": true}
	call := func(tool, args string) []byte {
		return []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`)
	}
	for _, seed := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
		call("list_devices", `{}`),
		call("list_devices", `{"area":"kitchen","query":"Kü\u0301che"}`),
		call("get_state", `{"entity_id":"ENTITY"}`),
		call("perform_action", `{"entity_id":"ENTITY","action":"turn_on"}`),
		call("perform_action", `{"entity_id":"ENTITY","action":"set","params":{"brightness_pct":40}}`),
		call("perform_action", `{"entity_id":"ENTITY","action":"set_temperature","params":{"temperature":21}}`),
		call("perform_action", `{"entity_id":"ENTITY","action":"unlock","reason":"parcel","idempotency_key":"k-1"}`),
		call("perform_action", `{"entity_id":"ENTITY","action":"turn_on","params":{"brightness_pct":1e400}}`),
		call("approval_status", `{"approval_id":"x"}`),
		call("approval_cancel", `{"approval_id":"x"}`),
		call("list_my_permissions", `{}`),
		call("perform_action", `null`),
		[]byte(`[` + string(call("list_my_permissions", `{}`)) + `]`),
		[]byte(`{"jsonrpc":"1.0","id":null}`), []byte(`{`), []byte(``),
	} {
		f.Add(seed, uint8(0), true)
	}
	f.Fuzz(func(t *testing.T, body []byte, entity uint8, withToken bool) {
		body = bytes.ReplaceAll(body, []byte("ENTITY"), []byte(entities[int(entity)%len(entities)]))
		h.now.advance(4 * time.Second)
		before := len(h.ha.recorded())
		req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if withToken {
			req.Header.Set("Authorization", "Bearer "+h.token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code >= http.StatusInternalServerError {
			t.Fatalf("status %d for %q: %s", rec.Code, body, rec.Body)
		}
		calls := h.ha.recorded()[before:]
		if !withToken && len(calls) > 0 {
			t.Fatalf("calls without a token: %+v", calls)
		}
		for _, c := range calls {
			if !allowed[c.EntityID] {
				t.Fatalf("call the mandate does not allow: %+v for %q", c, body)
			}
		}
	})
}
