// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// renameEntity changes an entity ID in Home Assistant's entity registry.
func renameEntity(t *testing.T, from, to string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := dialHA(ctx, env.haToken)
	if err != nil {
		t.Fatal(err)
	}
	defer c.conn.CloseNow()
	if _, err := c.command(ctx, map[string]any{"type": "config/entity_registry/update", "entity_id": from, "new_entity_id": to}); err != nil {
		t.Fatalf("rename %s to %s: %v", from, to, err)
	}
}

type staleMandate struct {
	AgentDisplayName string `json:"agent_display_name"`
	StaleReferences  []struct {
		RuleID   string `json:"rule_id"`
		EntityID string `json:"entity_id"`
	} `json:"stale_references"`
}

// A rename in Home Assistant leaves a deny rule on the old ID that no longer protects the
// device on its own: until a human resolves the rename, the rule keeps applying to the
// renamed device, the mandate reports it, and the critical mark moves along.
func TestDirectoryRenameIsReported(t *testing.T) {
	const old, renamed, agent = "light.ceiling_lights", "light.ceiling_lights_renamed", "Directory rename"
	token := newAgent(t, agent, func(d map[string]any) {
		d["rules"] = append(d["rules"].([]any), map[string]any{"id": "r-ceiling", "resource": map[string]any{"entity_id": old},
			"actions": []any{"turn_on"}, "decision": "deny"})
	})
	ui := uiLogin(t, adminApprover)
	critical := func(id string) (bool, bool) {
		var catalog struct {
			Devices []struct {
				EntityID string `json:"entity_id"`
				Critical bool   `json:"critical"`
			} `json:"devices"`
		}
		ui.ok(http.MethodGet, "api/devices", nil, &catalog)
		for _, d := range catalog.Devices {
			if d.EntityID == id {
				return true, d.Critical
			}
		}
		return false, false
	}
	stale := func() []string {
		var list []staleMandate
		ui.ok(http.MethodGet, "api/mandates", nil, &list)
		var ids []string
		for _, m := range list {
			if m.AgentDisplayName != agent {
				continue
			}
			for _, r := range m.StaleReferences {
				ids = append(ids, r.RuleID+"="+r.EntityID)
			}
		}
		return ids
	}
	if ids := stale(); len(ids) != 0 {
		t.Fatalf("stale references before the rename: %v", ids)
	}
	ui.ok(http.MethodPut, "api/devices/critical", map[string]any{"entity_id": old, "critical": true}, nil)

	renameEntity(t, old, renamed)
	t.Cleanup(func() {
		ui.ok(http.MethodPut, "api/devices/critical", map[string]any{"entity_id": renamed, "critical": false}, nil)
		renameEntity(t, renamed, old)
		eventually(t, "the old ID back in the catalog", 30*time.Second, func() bool { known, _ := critical(old); return known })
		ui.ok(http.MethodPut, "api/devices/critical", map[string]any{"entity_id": old, "critical": false}, nil)
	})

	eventually(t, "the stale reference", 30*time.Second, func() bool {
		ids := stale()
		return len(ids) == 1 && ids[0] == "r-ceiling="+old
	})
	// The mark moves after the refresh that found the rename.
	eventually(t, "the critical mark on the new ID", 30*time.Second, func() bool {
		known, marked := critical(renamed)
		return known && marked
	})
	// The log line is written after the refresh that already shows the stale reference.
	eventually(t, "the rename in the server log", 30*time.Second, func() bool {
		return hasLine(logsOf(env.hm), "entity renamed in Home Assistant", old, renamed)
	})

	// The deny rule on the former ID keeps protecting the renamed light, although the
	// voice assistant may switch every light: the stricter evaluation wins.
	ui.ok(http.MethodPut, "api/devices/critical", map[string]any{"entity_id": renamed, "critical": false}, nil)
	var renames []struct {
		EntityID string   `json:"entity_id"`
		Formers  []string `json:"formers"`
	}
	ui.ok(http.MethodGet, "api/renames", nil, &renames)
	if len(renames) != 1 || renames[0].EntityID != renamed || renames[0].Formers[0] != old {
		t.Fatalf("renames = %+v", renames)
	}
	s := session(t, token)
	ready(t, s)
	if _, errText := call(t, s, "perform_action", map[string]any{"entity_id": renamed, "action": "turn_on"}); !strings.HasPrefix(errText, "denied") {
		t.Errorf("renamed light before the rename is resolved = %q, want denied", errText)
	}
	// Dismissed: the rule on the former ID no longer applies, the broad rule does.
	ui.ok(http.MethodPost, "api/renames/dismiss", map[string]any{"entity_id": renamed, "formers": []string{old}, "confirm": true}, nil)
	if _, errText := call(t, s, "perform_action", map[string]any{"entity_id": renamed, "action": "turn_on"}); errText != "" {
		t.Errorf("renamed light after dismissing = %q, want executed", errText)
	}
}
