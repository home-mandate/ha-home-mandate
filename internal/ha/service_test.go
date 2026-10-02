// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"testing"
)

func TestCallServiceSendsOneTargetedCall(t *testing.T) {
	f := newFakeHA(t)
	got := make(chan fakeMsg, 1)
	f.handle("call_service", func(m fakeMsg) (any, *CommandError) {
		got <- m
		return map[string]any{"context": map[string]any{"id": "c1"}}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	err := c.CallService(context.Background(), ServiceCall{
		Domain: "light", Service: "turn_on", EntityID: "light.kitchen", Data: map[string]any{"brightness_pct": 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := <-got
	target, _ := m["target"].(map[string]any)
	data, _ := m["service_data"].(map[string]any)
	if m["domain"] != "light" || m["service"] != "turn_on" || target["entity_id"] != "light.kitchen" || len(target) != 1 ||
		data["brightness_pct"] != 40.0 || m["return_response"] != false {
		t.Errorf("call_service message = %v", m)
	}
}

func TestCallServiceRejectsBeforeSending(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	bad := map[string]ServiceCall{
		"service not allowed":      {Domain: "homeassistant", Service: "restart", EntityID: "homeassistant.x"},
		"script reload":            {Domain: "script", Service: "reload", EntityID: "script.x"},
		"domain mismatch":          {Domain: "lock", Service: "unlock", EntityID: "light.kitchen"},
		"no entity":                {Domain: "light", Service: "turn_on"},
		"several entities":         {Domain: "light", Service: "turn_on", EntityID: "light.a, light.b"},
		"all entities":             {Domain: "light", Service: "turn_on", EntityID: "all"},
		"malformed entity":         {Domain: "light", Service: "turn_on", EntityID: "light."},
		"target in data":           {Domain: "light", Service: "turn_on", EntityID: "light.a", Data: map[string]any{"entity_id": "lock.front"}},
		"area in data":             {Domain: "light", Service: "turn_on", EntityID: "light.a", Data: map[string]any{"area_id": "hallway"}},
		"device in data":           {Domain: "light", Service: "turn_on", EntityID: "light.a", Data: map[string]any{"device_id": "d1"}},
		"label in data":            {Domain: "light", Service: "turn_on", EntityID: "light.a", Data: map[string]any{"label_id": "l1"}},
		"floor in data":            {Domain: "light", Service: "turn_on", EntityID: "light.a", Data: map[string]any{"floor_id": "f1"}},
		"notify through this path": {Domain: "notify", Service: "mobile_app_phone", EntityID: "notify.x"},
	}
	for name, call := range bad {
		if err := c.CallService(context.Background(), call); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("%s: CallService = %v, want ErrCommandNotAllowed", name, err)
		}
	}
	roundTrip(t, c)
	if n := f.count("call_service"); n != 0 {
		t.Errorf("call_service reached HA %d times", n)
	}
}

func TestCallServiceReportsHAErrors(t *testing.T) {
	f := newFakeHA(t)
	f.handle("call_service", func(fakeMsg) (any, *CommandError) {
		return nil, &CommandError{Code: "home_assistant_error", Message: "Lock jammed"}
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	var cerr *CommandError
	err := c.CallService(context.Background(), ServiceCall{Domain: "lock", Service: "unlock", EntityID: "lock.front"})
	if !errors.As(err, &cerr) {
		t.Errorf("CallService = %v, want CommandError", err)
	}
	if !c.Connected() {
		t.Error("Connected() = false while connected")
	}
}

func TestConnectedBeforeRun(t *testing.T) {
	c, err := New(testConfig("ws://localhost:1/api/websocket"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Connected() {
		t.Error("connected before Run")
	}
	if err := c.CallService(context.Background(), ServiceCall{Domain: "light", Service: "turn_on", EntityID: "light.a"}); !errors.Is(err, ErrDisconnected) {
		t.Errorf("CallService before Run = %v, want ErrDisconnected", err)
	}
}
