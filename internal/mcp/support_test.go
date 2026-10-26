// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// Issue #89: what an entity can do, from Home Assistant's supported_features and
// attributes (profile Home Assistant, "What an entity can do"); an entity that reports
// nothing is asked as before.
func TestCanDo(t *testing.T) {
	light := func(modes ...any) map[string]any { return map[string]any{"supported_color_modes": modes} }
	for _, tc := range []struct {
		name     string
		category string
		action   string
		call     ha.ServiceCall
		attrs    map[string]any
		want     bool
	}{
		{"lock open without OPEN", "lock", "open", ha.ServiceCall{Service: "open"}, map[string]any{"supported_features": float64(0)}, false},
		{"lock open with OPEN", "lock", "open", ha.ServiceCall{Service: "open"}, map[string]any{"supported_features": float64(1)}, true},
		{"lock unlock needs nothing", "lock", "unlock", ha.ServiceCall{Service: "unlock"}, map[string]any{"supported_features": float64(0)}, true},
		{"lock without supported_features", "lock", "open", ha.ServiceCall{Service: "open"}, map[string]any{}, true},
		{"lock with a features value that is no number", "lock", "open", ha.ServiceCall{Service: "open"}, map[string]any{"supported_features": "1"}, true},
		{"cover stop without STOP", "cover", "stop", ha.ServiceCall{}, map[string]any{"supported_features": float64(1 | 2 | 4)}, false},
		{"cover set_position with SET_POSITION", "cover", "set_position", ha.ServiceCall{}, map[string]any{"supported_features": float64(4)}, true},
		{"gate close without CLOSE", "gate", "close", ha.ServiceCall{}, map[string]any{"supported_features": float64(1)}, false},
		{"climate temperature without TARGET_TEMPERATURE", "climate", "set_temperature", ha.ServiceCall{}, map[string]any{"supported_features": float64(128)}, false},
		{"climate temperature with it", "climate", "set_temperature", ha.ServiceCall{}, map[string]any{"supported_features": float64(1 | 128)}, true},
		{"climate mode not offered", "climate", "set_mode", ha.ServiceCall{Data: map[string]any{"hvac_mode": "cool"}}, map[string]any{"hvac_modes": []any{"off", "heat"}}, false},
		{"climate mode offered", "climate", "set_mode", ha.ServiceCall{Data: map[string]any{"hvac_mode": "heat"}}, map[string]any{"hvac_modes": []any{"off", "heat"}}, true},
		{"climate without hvac_modes", "climate", "set_mode", ha.ServiceCall{Data: map[string]any{"hvac_mode": "cool"}}, map[string]any{}, true},
		{"media volume without VOLUME_SET", "media", "set_volume", ha.ServiceCall{}, map[string]any{"supported_features": float64(1 | 16384)}, false},
		{"media play with PLAY", "media", "play", ha.ServiceCall{}, map[string]any{"supported_features": float64(16384)}, true},
		{"media turn_off without TURN_OFF", "media", "turn_off", ha.ServiceCall{}, map[string]any{"supported_features": float64(128)}, false},
		{"alarm arm night without ARM_NIGHT", "alarm", "arm", ha.ServiceCall{Service: "alarm_arm_night"}, map[string]any{"supported_features": float64(1 | 2)}, false},
		{"alarm arm away with ARM_AWAY", "alarm", "arm", ha.ServiceCall{Service: "alarm_arm_away"}, map[string]any{"supported_features": float64(2)}, true},
		{"alarm disarm needs nothing", "alarm", "disarm", ha.ServiceCall{Service: "alarm_disarm"}, map[string]any{"supported_features": float64(0)}, true},
		{"light brightness on an on/off light", "light", "set", ha.ServiceCall{Data: map[string]any{"brightness_pct": 50}}, light("onoff"), false},
		{"light brightness on a dimmable light", "light", "set", ha.ServiceCall{Data: map[string]any{"brightness_pct": 50}}, light("brightness"), true},
		{"light colour temperature without color_temp", "light", "set", ha.ServiceCall{Data: map[string]any{"color_temp_kelvin": 3000}}, light("hs"), false},
		{"light colour temperature with it", "light", "set", ha.ServiceCall{Data: map[string]any{"color_temp_kelvin": 3000}}, light("color_temp", "hs"), true},
		{"light without supported_color_modes", "light", "set", ha.ServiceCall{Data: map[string]any{"brightness_pct": 50}}, map[string]any{}, true},
		{"light turn_on needs nothing", "light", "turn_on", ha.ServiceCall{}, light("onoff"), true},
		{"switch needs nothing", "switch", "turn_on", ha.ServiceCall{}, map[string]any{"supported_features": float64(0)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canDo(tc.category, tc.action, tc.call, tc.attrs); got != tc.want {
				t.Errorf("canDo = %v, want %v", got, tc.want)
			}
		})
	}
}

// The actions an entity can carry out, named to the agent and listed in its permissions:
// every vocabulary action except read whose needs the entity reports as missing.
func TestActionsItCanDo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		attrs    map[string]any
		want     []string
	}{
		{"lock without OPEN", "lock", map[string]any{"supported_features": float64(0)}, []string{"lock", "read", "unlock"}},
		{"lock reporting nothing", "lock", map[string]any{}, []string{"lock", "open", "read", "unlock"}},
		{"on/off light", "light", map[string]any{"supported_color_modes": []any{"onoff"}}, []string{"read", "turn_off", "turn_on"}},
		{"dimmable light", "light", map[string]any{"supported_color_modes": []any{"brightness"}}, []string{"read", "set", "turn_off", "turn_on"}},
		{"climate without modes", "climate", map[string]any{"supported_features": float64(1), "hvac_modes": []any{}}, []string{"read", "set_temperature"}},
		{"alarm without any arm mode", "alarm", map[string]any{"supported_features": float64(0)}, []string{"disarm", "read"}},
		{"media player that only plays", "media", map[string]any{"supported_features": float64(1 | 16384)}, []string{"pause", "play", "read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionsItCanDo(tc.category, tc.attrs); !slices.Equal(got, tc.want) {
				t.Errorf("actionsItCanDo = %v, want %v", got, tc.want)
			}
		})
	}
}

func (h *harness) setAttrs(entityID string, attrs map[string]any) {
	h.catalog.mu.Lock()
	defer h.catalog.mu.Unlock()
	dev := h.catalog.devices[entityID]
	dev.Attributes = attrs
	h.catalog.devices[entityID] = dev
}

// Issue #89: a lock without OPEN is not opened on a confirmation; nobody is asked, the
// agent learns what the lock can do, and the entry is failed/not_supported without an
// approval. Once the lock reports OPEN again, the human is asked as before.
func TestNobodyIsAskedForWhatTheDeviceCannotDo(t *testing.T) {
	h, f := pendingHarness(t)
	h.setAttrs("lock.front_door", map[string]any{"supported_features": float64(0)})
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "open"})
	if errText != "not_supported: lock.front_door cannot open; it can: lock, unlock" {
		t.Errorf("open a lock without OPEN = %q", errText)
	}
	if len(f.requests()) != 0 {
		t.Error("a human was asked")
	}
	if e := h.lastEntry(); path(e, "result", "status") != "failed" || path(e, "result", "error") != "not_supported" ||
		path(e, "approval") != nil || path(e, "evaluation", "decision") != "ask" {
		t.Errorf("audit entry = %v", e)
	}
	if calls := h.ha.recorded(); len(calls) != 0 {
		t.Errorf("Home Assistant was called: %+v", calls)
	}
	h.setAttrs("lock.front_door", map[string]any{"supported_features": float64(1)})
	if out, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "open"}); out["status"] != "pending" {
		t.Errorf("open a lock with OPEN = %v %q", out, errText)
	}
	if len(f.requests()) != 1 {
		t.Errorf("asked %d times", len(f.requests()))
	}
}

// An agent that may not read the device learns that it cannot do this, not what it can.
func TestWhatTheDeviceCanDoOnlyForAgentsThatMayReadIt(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = []any{map[string]any{"id": "r-open", "resource": map[string]any{"category": "lock"}, "actions": []any{"open"}, "decision": "allow", "allow_critical": true}}
	})
	f.journal, h.approver, h.approvalWait = h.journal, f, 100*time.Millisecond
	h.url = h.serve(h.log)
	h.setAttrs("lock.back_door", map[string]any{"supported_features": float64(0)})
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.back_door", "action": "open"})
	if !strings.HasPrefix(errText, "not_supported") || strings.Contains(errText, "it can") {
		t.Errorf("an agent that may not read the lock learned its features: %q", errText)
	}
}

// list_my_permissions offers only what the device can carry out (issue #89).
func TestPermissionsListOnlyWhatTheDeviceCanDo(t *testing.T) {
	h := newHarness(t, nil)
	h.setAttrs("lock.front_door", map[string]any{"supported_features": float64(0)})
	h.setAttrs("lock.back_door", map[string]any{"supported_features": float64(1)})
	out, errText := h.call(h.session(), "list_my_permissions", map[string]any{})
	if errText != "" {
		t.Fatal(errText)
	}
	got := permissionsByDevice(out)
	if _, ok := got["lock.front_door"]["open"]; ok {
		t.Errorf("lock without OPEN offers open: %v", got["lock.front_door"])
	}
	if got["lock.back_door"]["open"] != "ask" || got["lock.front_door"]["unlock"] != "ask" {
		t.Errorf("permissions = %v / %v", got["lock.front_door"], got["lock.back_door"])
	}
}
