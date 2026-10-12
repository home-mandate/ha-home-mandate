// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"fmt"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// SPEC-v0 section 11.1 item 10 (issue #27, layer 3): the state the request started from
// decides at the execution, per the target-state table.
func TestStateAtExecution(t *testing.T) {
	dev := func(state string, attrs map[string]any) catalog.Device {
		return catalog.Device{State: state, Attributes: attrs}
	}
	call := func(data map[string]any) ha.ServiceCall { return ha.ServiceCall{Data: data} }
	for _, tc := range []struct {
		name             string
		category, action string
		call             ha.ServiceCall
		recorded         string
		now              catalog.Device
		want             string
	}{
		{"unchanged", "cover", "open", call(nil), "closed", dev("closed", nil), ""},
		{"opened meanwhile", "cover", "open", call(nil), "closed", dev("open", nil), errAlreadyInState},
		{"opening meanwhile", "gate", "open", call(nil), "closed", dev("opening", nil), errAlreadyInState},
		// Partly open is not open: open_cover would open it fully.
		{"partly open", "cover", "open", call(nil), "open", dev("open", map[string]any{"current_position": 50}), ""},
		{"fully open", "cover", "open", call(nil), "closed", dev("open", map[string]any{"current_position": 100}), errAlreadyInState},
		{"closing meanwhile", "cover", "open", call(nil), "open", dev("closing", nil), errStateChanged},
		{"other state", "cover", "close", call(nil), "open", dev("opening", nil), errStateChanged},
		{"recorded already reached", "cover", "close", call(nil), "closed", dev("closed", nil), errAlreadyInState},
		{"light on", "light", "turn_on", call(nil), "off", dev("on", nil), errAlreadyInState},
		{"light off unchanged", "light", "turn_off", call(nil), "on", dev("on", nil), ""},
		{"switch", "switch", "turn_off", call(nil), "on", dev("off", nil), errAlreadyInState},
		{"locked", "lock", "lock", call(nil), "unlocked", dev("locking", nil), errAlreadyInState},
		{"unlock jammed", "lock", "unlock", call(nil), "locked", dev("jammed", nil), errStateChanged},
		{"alarm armed", "alarm", "arm", ha.ServiceCall{Service: "alarm_arm_night"}, "disarmed", dev("armed_night", nil), errAlreadyInState},
		{"alarm armed otherwise", "alarm", "arm", ha.ServiceCall{Service: "alarm_arm_night"}, "disarmed", dev("armed_away", nil), errStateChanged},
		{"disarm", "alarm", "disarm", call(nil), "armed_away", dev("disarmed", nil), errAlreadyInState},
		{"media play", "media", "play", call(nil), "paused", dev("playing", nil), errAlreadyInState},
		{"media on", "media", "turn_on", call(nil), "off", dev("idle", nil), errAlreadyInState},
		{"mode", "climate", "set_mode", call(map[string]any{"hvac_mode": "heat"}), "off", dev("heat", nil), errAlreadyInState},
		{"temperature reached", "climate", "set_temperature", call(map[string]any{"temperature": 21.5}), "heat",
			dev("heat", map[string]any{"temperature": 21.5}), errAlreadyInState},
		{"temperature not reached", "climate", "set_temperature", call(map[string]any{"temperature": 21.5}), "heat",
			dev("heat", map[string]any{"temperature": 20.0}), ""},
		{"temperature, mode changed", "climate", "set_temperature", call(map[string]any{"temperature": 21.5}), "heat",
			dev("off", map[string]any{"temperature": 20.0}), errStateChanged},
		{"position reached", "cover", "set_position", call(map[string]any{"position": 40}), "open",
			dev("open", map[string]any{"current_position": 40}), errAlreadyInState},
		{"position without attribute", "cover", "set_position", call(map[string]any{"position": 40}), "open", dev("open", nil), ""},
		{"volume reached", "media", "set_volume", call(map[string]any{"volume_level": 0.5}), "playing",
			dev("playing", map[string]any{"volume_level": 0.5}), errAlreadyInState},
		// No layer 3: stateless actions and unreliable values.
		{"script", "script", "run", call(nil), "off", dev("on", nil), ""},
		{"scene", "scene", "activate", call(nil), "2026-10-10T12:00:00", dev("2026-10-10T12:05:00", nil), ""},
		{"lock open", "lock", "open", call(nil), "locked", dev("open", nil), ""},
		{"cover stop", "cover", "stop", call(nil), "opening", dev("open", nil), ""},
		{"light set", "light", "set", call(map[string]any{"brightness_pct": 50}), "on", dev("off", nil), ""},
		// A state-only rule says nothing about a call with data: no layer 3.
		{"turn_on with data", "light", "turn_on", call(map[string]any{"brightness_pct": 50}), "on", dev("on", nil), ""},
		// No state known when the request was made: no layer 3.
		{"unknown before", "cover", "open", call(nil), "", dev("open", nil), ""},
		// The device is gone or unavailable now: changed.
		{"unavailable now", "cover", "open", call(nil), "closed", dev("unavailable", nil), errStateChanged},
	} {
		if got := stateCheck(tc.category, tc.action, tc.call, tc.recorded, tc.now, true); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := stateCheck("cover", "open", ha.ServiceCall{}, "closed", catalog.Device{}, false); got != errStateChanged {
		t.Errorf("device gone: %q", got)
	}
	// Only states of the category's known set are recorded and shown.
	for _, tc := range []struct{ category, state, want string }{
		{"cover", "closed", "closed"}, {"gate", "opening", "opening"}, {"lock", "jammed", "jammed"}, {"alarm", "armed_night", "armed_night"},
		{"light", "on", "on"}, {"climate", "heat", "heat"}, {"media", "playing", "playing"},
		{"cover", "", ""}, {"cover", "unknown", ""}, {"cover", "unavailable", ""}, {"light", "<b>on</b>", ""},
		{"cover", "on", ""}, {"scene", "2026-10-10T12:00:00", ""}, {"script", "on", ""}, {"other", "16", ""}, {"alarm", "armed_", ""},
	} {
		if got := shownState(tc.category, tc.state); got != tc.want {
			t.Errorf("shownState(%s, %q) = %q", tc.category, tc.state, got)
		}
	}
}

// The locks of the checks are a fixed set: agent-chosen keys cannot make them grow, and
// the same key always gets the same lock.
func TestStripedLocks(t *testing.T) {
	g := New(Config{})
	if g.keyLock("a") != g.keyLock("a") || g.deviceLock("a") != g.deviceLock("a") {
		t.Error("the same key, another lock")
	}
	for i := range 10000 {
		g.keyLock(fmt.Sprint("key-", i))
	}
	if len(g.keyLocks) != lockStripes || len(g.deviceLocks) != lockStripes {
		t.Errorf("%d, %d locks", len(g.keyLocks), len(g.deviceLocks))
	}
}
