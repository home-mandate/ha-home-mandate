// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
)

// put adds or replaces a device of the fake directory.
func (f *fakeCatalog) put(d catalog.Device) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[d.EntityID] = d
}

// readView returns only the state values the Home Assistant profile lists for a category
// (SPEC-v0 section 11.8, profiles/home-assistant.md "What read returns"), with attribute
// sets as Home Assistant reports them.
func TestReadViewReturnsOnlyTheProfileValues(t *testing.T) {
	exact := strings.Repeat("ab ", maxReadState/3) + strings.Repeat("a", maxReadState%3)
	long := exact + "a"
	tests := []struct {
		name      string
		dev       catalog.Device
		wantState string
		wantAttrs map[string]any
	}{
		{"light", catalog.Device{EntityID: "light.kitchen", Category: "light", State: "on", Attributes: map[string]any{
			"friendly_name": "Kitchen", "brightness": 180.0, "color_mode": "color_temp", "color_temp_kelvin": 2700.0,
			"supported_color_modes": []any{"color_temp", "hs"}, "rgb_color": []any{255.0, 200.0, 150.0}, "effect_list": []any{"rainbow"},
			"supported_features": 44.0, "entity_picture": "/api/image/x?token=abc",
		}}, "on", map[string]any{"brightness": 180.0, "color_mode": "color_temp", "color_temp_kelvin": 2700.0, "supported_color_modes": []any{"color_temp", "hs"}}},
		{"switch", catalog.Device{EntityID: "switch.pump", Category: "switch", State: "off", Attributes: map[string]any{"friendly_name": "Pump", "current_power_w": 3.0}}, "off", nil},
		{"climate", catalog.Device{EntityID: "climate.living_room", Category: "climate", State: "heat_cool", Attributes: map[string]any{
			"hvac_action": "heating", "current_temperature": 20.5, "temperature": nil, "target_temp_low": 19.0, "target_temp_high": 23.0,
			"hvac_modes": []any{"off", "heat_cool"}, "min_temp": 7.0, "max_temp": 35.0, "preset_modes": []any{"eco"}, "friendly_name": "Living room",
		}}, "heat_cool", map[string]any{"hvac_action": "heating", "current_temperature": 20.5, "temperature": nil, "target_temp_low": 19.0,
			"target_temp_high": 23.0, "hvac_modes": []any{"off", "heat_cool"}, "min_temp": 7.0, "max_temp": 35.0}},
		{"cover", catalog.Device{EntityID: "cover.blinds", Category: "cover", State: "open", Attributes: map[string]any{"current_position": 60.0, "current_tilt_position": 20.0}},
			"open", map[string]any{"current_position": 60.0}},
		{"gate", catalog.Device{EntityID: "cover.garage", Category: "gate", State: "closed", Attributes: map[string]any{"current_position": 0.0, "device_class": "garage"}},
			"closed", map[string]any{"current_position": 0.0}},
		{"lock: no code format, no user", catalog.Device{EntityID: "lock.front_door", Category: "lock", State: "locked", Attributes: map[string]any{
			"code_format": `^\d{4}$`, "changed_by": "Anna", "friendly_name": "Front door",
		}}, "locked", nil},
		{"alarm: no code format, no user", catalog.Device{EntityID: "alarm_control_panel.home", Category: "alarm", State: "armed_away", Attributes: map[string]any{
			"code_format": "number", "changed_by": "Anna", "code_arm_required": true,
		}}, "armed_away", nil},
		{"camera: no picture, no stream token", catalog.Device{EntityID: "camera.porch", Category: "camera", State: "idle", Attributes: map[string]any{
			"entity_picture": "/api/camera_proxy/camera.porch?token=secret", "access_token": "secret", "frontend_stream_type": "hls", "brand": "X",
		}}, "idle", nil},
		{"media: no cover, no content URL", catalog.Device{EntityID: "media_player.tv", Category: "media", State: "playing", Attributes: map[string]any{
			"volume_level": 0.4, "is_volume_muted": false, "entity_picture": "/api/media_player_proxy/media_player.tv?token=abc",
			"media_content_id": "https://cdn.example/stream.m3u8?sig=abc", "media_title": "News", "source_list": []any{"TV"},
		}}, "playing", map[string]any{"volume_level": 0.4, "is_volume_muted": false}},
		{"sensor", catalog.Device{EntityID: "sensor.temperature", Category: "sensor", State: "21.5", Attributes: map[string]any{
			"unit_of_measurement": "°C", "device_class": "temperature", "state_class": "measurement", "friendly_name": "Temperature",
		}}, "21.5", map[string]any{"unit_of_measurement": "°C", "device_class": "temperature"}},
		{"binary sensor", catalog.Device{EntityID: "binary_sensor.door", Category: "sensor", State: "on", Attributes: map[string]any{"device_class": "door"}},
			"on", map[string]any{"device_class": "door"}},
		{"sensor: URL with credentials", catalog.Device{EntityID: "sensor.snapshot", Category: "sensor", State: "https://user:pass@nvr.local/snap.jpg", Attributes: map[string]any{"device_class": "url"}},
			"", map[string]any{"device_class": "url"}},
		{"sensor: secret in a parameter", catalog.Device{EntityID: "sensor.feed", Category: "sensor", State: "http://feed.example/x?api_key=abc"}, "", nil},
		{"sensor: longer than the limit", catalog.Device{EntityID: "sensor.text", Category: "sensor", State: long}, "", nil},
		{"sensor: exactly the limit", catalog.Device{EntityID: "sensor.text", Category: "sensor", State: exact}, exact, nil},
		{"scene", catalog.Device{EntityID: "scene.evening", Category: "scene", State: "2026-11-16T18:00:00+00:00", Attributes: map[string]any{"entity_id": []any{"light.kitchen"}}},
			"2026-11-16T18:00:00+00:00", nil},
		{"script", catalog.Device{EntityID: "script.goodnight", Category: "script", State: "off", Attributes: map[string]any{"last_triggered": "2026-11-15T22:00:00+00:00"}}, "off", nil},
		{"other: listed domain, state only", catalog.Device{EntityID: "number.wallbox", Category: "other", State: "16", Attributes: map[string]any{"min": 6.0, "max": 32.0}}, "16", nil},
		{"other: fan", catalog.Device{EntityID: "fan.bathroom", Category: "other", State: "on", Attributes: map[string]any{"percentage": 50.0}}, "on", nil},
		{"other: person", catalog.Device{EntityID: "person.anna", Category: "other", State: "home", Attributes: map[string]any{
			"latitude": 52.52, "longitude": 13.40, "gps_accuracy": 10.0, "source": "device_tracker.phone",
		}}, "", nil},
		{"other: device tracker", catalog.Device{EntityID: "device_tracker.phone", Category: "other", State: "not_home", Attributes: map[string]any{"ip": "192.0.2.7", "latitude": 52.5}}, "", nil},
		{"other: update", catalog.Device{EntityID: "update.core", Category: "other", State: "on", Attributes: map[string]any{
			"release_url": "https://example/release", "entity_picture": "https://brands.example/x.png",
		}}, "", nil},
		{"other: text", catalog.Device{EntityID: "input_text.note", Category: "other", State: "the code is 1234"}, "", nil},
		{"other: unknown domain", catalog.Device{EntityID: "vendor_thing.x", Category: "other", State: "ok"}, "", nil},
		{"unknown category", catalog.Device{EntityID: "light.x", Category: "future", State: "on"}, "", nil},
		{"listed attribute with a URL", catalog.Device{EntityID: "light.x", Category: "light", State: "on", Attributes: map[string]any{"color_mode": "http://x/?token=a"}}, "on", nil},
		{"listed attribute as an object", catalog.Device{EntityID: "light.x", Category: "light", State: "on", Attributes: map[string]any{"brightness": map[string]any{"url": "x"}}}, "on", nil},
		{"listed list with an object", catalog.Device{EntityID: "light.x", Category: "light", State: "on", Attributes: map[string]any{"supported_color_modes": []any{"hs", map[string]any{}}}}, "on", nil},
		{"listed list with a URL", catalog.Device{EntityID: "climate.x", Category: "climate", State: "heat", Attributes: map[string]any{"hvac_modes": []any{"heat", "https://x"}}}, "heat", nil},
		{"state with a URL in any category", catalog.Device{EntityID: "switch.x", Category: "switch", State: "rtsp://cam.local/live"}, "", nil},
		{"state: URL without a scheme", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "//nvr.local/snap?key=abc"}, "", nil},
		{"state: credentials without a scheme", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "admin:secret@nvr.local"}, "", nil},
		{"state: e-mail address", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "anna@example.org"}, "", nil},
		{"state: network share", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: `\\nas\backup`}, "", nil},
		{"state: bare key", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "a1b2c3d4e5f6a7b8c9d0e1f2"}, "", nil},
		{"state: bare key in base64", catalog.Device{EntityID: "select.x", Category: "other", State: "dGhpcyBpcyBhIHNlY3JldCBrZXk="}, "", nil},
		{"state: timestamp", catalog.Device{EntityID: "sensor.last_boot", Category: "sensor", State: "2026-11-16T06:12:00+00:00", Attributes: map[string]any{"device_class": "timestamp"}},
			"2026-11-16T06:12:00+00:00", map[string]any{"device_class": "timestamp"}},
		{"state: short token", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "firmware_1_2_3_stable"}, "firmware_1_2_3_stable", nil},
		{"state: sentence", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "Waschgang beendet, bitte ausräumen"}, "Waschgang beendet, bitte ausräumen", nil},
		{"unit with symbols", catalog.Device{EntityID: "sensor.price", Category: "sensor", State: "0.31", Attributes: map[string]any{"unit_of_measurement": "€/kWh", "device_class": "monetary"}},
			"0.31", map[string]any{"unit_of_measurement": "€/kWh", "device_class": "monetary"}},
		{"attribute with a colon", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "1", Attributes: map[string]any{"unit_of_measurement": "key:abc"}}, "1", nil},
		{"attribute with a parameter", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "1", Attributes: map[string]any{"device_class": "x?k=v"}}, "1", nil},
		{"attribute longer than the limit", catalog.Device{EntityID: "sensor.x", Category: "sensor", State: "1", Attributes: map[string]any{"device_class": strings.Repeat("a", maxReadAttribute+1)}}, "1", nil},
		{"attribute list longer than the limit", catalog.Device{EntityID: "climate.x", Category: "climate", State: "heat", Attributes: map[string]any{"hvac_modes": manyModes(maxReadList + 1)}}, "heat", nil},
		{"attribute list at the limit", catalog.Device{EntityID: "climate.x", Category: "climate", State: "heat", Attributes: map[string]any{"hvac_modes": manyModes(maxReadList)}},
			"heat", map[string]any{"hvac_modes": manyModes(maxReadList)}},
		{"nested list", catalog.Device{EntityID: "light.x", Category: "light", State: "on", Attributes: map[string]any{"supported_color_modes": []any{[]any{"hs"}}}}, "on", nil},
		{"typed values", catalog.Device{EntityID: "light.x", Category: "light", State: "on", Attributes: map[string]any{"brightness": json.Number("12"), "color_temp_kelvin": 3000}},
			"on", map[string]any{"brightness": json.Number("12"), "color_temp_kelvin": 3000}},
		{"no attributes", catalog.Device{EntityID: "light.x", Category: "light", State: "on"}, "on", nil},
		{"other without an entity ID", catalog.Device{Category: "other", State: "on"}, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, attrs := readView(tt.dev)
			if state != tt.wantState {
				t.Errorf("state = %q, want %q", state, tt.wantState)
			}
			if len(attrs) != 0 || len(tt.wantAttrs) != 0 {
				if !reflect.DeepEqual(attrs, tt.wantAttrs) {
					t.Errorf("attributes = %v, want %v", attrs, tt.wantAttrs)
				}
			}
		})
	}
}

func manyModes(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = fmt.Sprintf("m%d", i)
	}
	return out
}

// readView never changes the directory's map.
func TestReadViewDoesNotModifyTheDevice(t *testing.T) {
	attrs := map[string]any{"brightness": 10.0, "entity_picture": "x"}
	readView(catalog.Device{EntityID: "light.x", Category: "light", State: "on", Attributes: attrs})
	if len(attrs) != 2 {
		t.Errorf("attributes changed: %v", attrs)
	}
}

func readAll(d map[string]any) {
	d["rules"] = []any{map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"}}
}

// get_state returns no attribute and no state outside the profile.
func TestGetStateReturnsOnlyTheProfileValues(t *testing.T) {
	h := newHarness(t, readAll)
	h.catalog.put(catalog.Device{EntityID: "person.anna", Category: "other", State: "home", Attributes: map[string]any{"latitude": 52.52, "longitude": 13.4}})
	h.catalog.put(catalog.Device{EntityID: "sensor.snapshot", Category: "sensor", State: "https://user:pass@nvr.local/snap.jpg", Attributes: map[string]any{"device_class": "url"}})
	h.catalog.put(catalog.Device{EntityID: "media_player.tv", Category: "media", State: "playing", Attributes: map[string]any{
		"volume_level": 0.4, "media_content_id": "https://cdn.example/stream.m3u8?sig=abc", "entity_picture": "/api/media_player_proxy/media_player.tv?token=abc"}})
	for id, leak := range map[string]string{"person.anna": "52.52", "sensor.snapshot": "user:pass", "media_player.tv": "sig=abc"} {
		out, errText := h.call(h.session(), "get_state", map[string]any{"entity_id": id})
		if errText != "" {
			t.Fatalf("get_state %s: %q", id, errText)
		}
		if s := mustJSON(out); strings.Contains(s, leak) || strings.Contains(s, "token=") {
			t.Errorf("get_state %s leaks: %s", id, s)
		}
	}
	out, _ := h.call(h.session(), "get_state", map[string]any{"entity_id": "media_player.tv"})
	if attrs, _ := out["attributes"].(map[string]any); out["state"] != "playing" || attrs["volume_level"] != 0.4 || len(attrs) != 1 || attrs["friendly_name"] != nil {
		t.Errorf("get_state media_player.tv = %v", out)
	}
}

// A read a human confirmed returns the same values as one without a confirmation.
func TestConfirmedReadReturnsOnlyTheProfileValues(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := askReadHarness(t, f, 0)
	h.catalog.put(catalog.Device{EntityID: "lock.front_door", Category: "lock", Areas: []string{"hallway", "floor:ground"}, State: "locked",
		Attributes: map[string]any{"code_format": `^\d{4}$`, "changed_by": "Anna"}})
	out, errText := readLock(h)
	if errText != "" || out["state"] != "locked" {
		t.Fatalf("get_state = %v, %q", out, errText)
	}
	if s := mustJSON(out); strings.Contains(s, "code_format") || strings.Contains(s, "Anna") {
		t.Errorf("confirmed read leaks: %s", s)
	}
}

// list_devices shows a state only where read would return it.
func TestListDevicesShowsOnlyTheProfileState(t *testing.T) {
	h := newHarness(t, readAll)
	h.catalog.put(catalog.Device{EntityID: "person.anna", Category: "other", State: "home"})
	h.catalog.put(catalog.Device{EntityID: "input_text.note", Category: "other", State: "https://x/?api_key=abc"})
	out, errText := h.call(h.session(), "list_devices", map[string]any{"category": "other"})
	if errText != "" {
		t.Fatal(errText)
	}
	s := mustJSON(out)
	if strings.Contains(s, `"home"`) || strings.Contains(s, "api_key") {
		t.Errorf("list_devices leaks: %s", s)
	}
	if !strings.Contains(s, `"state":"16"`) {
		t.Errorf("list_devices lost the state of a listed domain: %s", s)
	}
}
