// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// setHAState creates or replaces the state of an entity through the REST API of Home
// Assistant, as an integration would, and removes it when the test ends.
func setHAState(t *testing.T, entityID, state string, attrs map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"state": state, "attributes": attrs})
	req, _ := http.NewRequest(http.MethodPost, env.haURL+"/api/states/"+entityID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.haToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("set state of %s: HTTP %d", entityID, resp.StatusCode)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, env.haURL+"/api/states/"+entityID, nil)
		req.Header.Set("Authorization", "Bearer "+env.haToken)
		if resp, err := httpClient().Do(req); err == nil {
			resp.Body.Close()
		}
	})
}

// readAnything lets the agent read every device and nothing else.
func readAnything(doc map[string]any) {
	doc["rules"] = []any{map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"}}
}

// read returns only the state values the Home Assistant profile lists (SPEC-v0 section
// 11.8): with real entities of Home Assistant and states an integration could set, the agent
// gets no picture or stream token, no credentials or keys, no code format, no user and no
// location, in get_state and in list_devices.
func TestReadReturnsOnlyTheProfileValues(t *testing.T) {
	setHAState(t, "sensor.e2e_temperature", "21.5", map[string]any{"unit_of_measurement": "°C", "device_class": "temperature", "state_class": "measurement", "friendly_name": "E2E Temperature"})
	setHAState(t, "sensor.e2e_snapshot", "https://user:pass@nvr.local/snap.jpg", map[string]any{"friendly_name": "E2E Snapshot"})
	setHAState(t, "sensor.e2e_key", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6", nil)
	setHAState(t, "lock.e2e_door", "locked", map[string]any{"code_format": `^\d{4}$`, "changed_by": "E2E Anna", "friendly_name": "E2E Door"})
	setHAState(t, "person.e2e_anna", "home", map[string]any{"latitude": 52.5162, "longitude": 13.3777, "gps_accuracy": 12, "friendly_name": "E2E Anna"})
	setHAState(t, "input_text.e2e_note", "https://example.org/hook?api_key=e2esecret", nil)

	s := session(t, newAgent(t, "Read filter", readAnything))
	ready(t, s)
	get := func(id string) map[string]any {
		t.Helper()
		var out map[string]any
		eventually(t, id+" in the directory", 15*time.Second, func() bool {
			var errText string
			out, errText = call(t, s, "get_state", map[string]any{"entity_id": id})
			return errText == ""
		})
		return out
	}
	leaks := []string{"token", "entity_picture", "user:pass", "e2esecret", "a1b2c3d4", "code_format", "E2E Anna", "52.5162", "friendly_name", "://"}
	checkNoLeak := func(what string, v any) {
		t.Helper()
		got := string(mustJSON(t, v))
		for _, leak := range leaks {
			if strings.Contains(got, leak) {
				t.Errorf("%s contains %q: %s", what, leak, got)
			}
		}
	}

	// Precondition: Home Assistant itself carries the token of the demo camera.
	if pic, _ := haAttributes(t, "camera.demo_camera")["entity_picture"].(string); !strings.Contains(pic, "token=") {
		t.Fatalf("demo camera without a token in entity_picture (%q): the test would prove nothing", pic)
	}
	camera := get("camera.demo_camera")
	checkNoLeak("camera", camera)
	if camera["state"] == "" || camera["attributes"] != nil {
		t.Errorf("camera = %v, want its state and no attributes", camera)
	}

	temp := get("sensor.e2e_temperature")
	checkNoLeak("temperature sensor", temp)
	if attrs, _ := temp["attributes"].(map[string]any); temp["state"] != "21.5" || attrs["unit_of_measurement"] != "°C" || attrs["device_class"] != "temperature" || len(attrs) != 2 {
		t.Errorf("temperature sensor = %v", temp)
	}
	for _, id := range []string{"sensor.e2e_snapshot", "sensor.e2e_key", "person.e2e_anna", "input_text.e2e_note"} {
		out := get(id)
		checkNoLeak(id, out)
		if out["state"] != "" || out["attributes"] != nil {
			t.Errorf("%s = %v, want neither state nor attributes", id, out)
		}
	}
	door := get("lock.e2e_door")
	checkNoLeak("lock", door)
	if door["state"] != "locked" || door["attributes"] != nil {
		t.Errorf("lock = %v, want locked without attributes", door)
	}

	// list_devices shows the same states as get_state.
	list, errText := call(t, s, "list_devices", map[string]any{"search": "e2e", "limit": 50})
	if errText != "" {
		t.Fatal(errText)
	}
	listed := string(mustJSON(t, list))
	for _, leak := range []string{"user:pass", "e2esecret", "a1b2c3d4", `"home"`} {
		if strings.Contains(listed, leak) {
			t.Errorf("list_devices contains %q: %s", leak, listed)
		}
	}
	if !strings.Contains(listed, `"state":"21.5"`) || !strings.Contains(listed, `"state":"locked"`) {
		t.Errorf("list_devices lost allowed states: %s", listed)
	}
}
