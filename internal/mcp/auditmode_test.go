// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// trialRules allows reading and switching on lights; everything else no rule decides.
func trialRules(d map[string]any) {
	d["rules"] = []any{
		map[string]any{"id": "r-lights", "resource": map[string]any{"category": "light"}, "actions": []any{"read", "turn_on"}, "decision": "allow"},
		map[string]any{"id": "r-no-arming", "resource": map[string]any{"category": "alarm"}, "actions": []any{"arm"}, "decision": "deny"},
	}
}

// auditMode switches the agent's mandate to audit mode as a human does: proposed in the
// editor, confirmed by its digest.
func (h *harness) auditMode() {
	h.t.Helper()
	ctx := context.Background()
	m, err := h.mandates.ForAgent(ctx, h.agent.ClientID)
	if err != nil {
		h.t.Fatal(err)
	}
	info, err := h.mandates.Get(ctx, m.Mandate.ID())
	if err != nil {
		h.t.Fatal(err)
	}
	next := mandateDoc(h.t, h.agent.ClientID, func(d map[string]any) {
		trialRules(d)
		d["enforce"] = false
	})
	if _, err := h.mandates.Update(ctx, info.ID, next, mandate.Change{BaseDigest: info.Digest, MayPropose: true}, admin); !errors.Is(err, mandate.ErrAuditModeProposed) {
		h.t.Fatalf("Update = %v", err)
	}
	p, _, _ := h.mandates.Pending(ctx, info.ID)
	if _, err := h.mandates.ConfirmAuditMode(ctx, info.ID, p.Digest, audit.Actor{Kind: audit.ActorUser, ID: "user-2"}); err != nil {
		h.t.Fatal(err)
	}
}

// In audit mode a deny by the rules of an action that is not critical is executed as an
// allow and recorded with enforced: false; the agent learns nothing of it. Critical
// actions stay denied (SPEC-v0 sections 4.6 and 11.6).
func TestAuditModeExecutesDeniesSilently(t *testing.T) {
	h := newHarness(t, trialRules)
	h.auditMode()
	s := h.session()

	// No rule decides turn_off: executed; a deny rule on arming: executed as well.
	for _, args := range []map[string]any{
		{"entity_id": "light.kitchen", "action": "turn_off"},
		{"entity_id": "alarm_control_panel.home", "action": "arm"},
	} {
		out, errText := h.call(s, "perform_action", args)
		if errText != "" || out["status"] != "executed" {
			t.Fatalf("%v: %v %q, want executed like an allow", args, out, errText)
		}
		e := h.lastEntry()
		if path(e, "evaluation", "decision") != "deny" || path(e, "evaluation", "enforced") != false ||
			path(e, "result", "status") != "executed" || path(e, "request", "resource", "critical") != false {
			t.Errorf("%v: audit entry = %v", args, e)
		}
	}
	// Reading is the same: a device no rule lets the agent read is listed and read.
	if _, errText := h.call(s, "get_state", map[string]any{"entity_id": "climate.living_room"}); errText != "" {
		t.Errorf("get_state = %q", errText)
	}
	// Disarming is critical: its deny is enforced and it is not listed.
	perms, _ := h.call(s, "list_my_permissions", map[string]any{"category": "alarm"})
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"arm":"allow","read":"allow"},"category":"alarm","entity_ids":["alarm_control_panel.home"]}]` {
		t.Errorf("permissions in audit mode = %s; the agent must see an allow", got)
	}
	// A critical action stays denied, also in audit mode.
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); errText == "" {
		t.Error("unlock executed in audit mode")
	}
	if e := h.lastEntry(); path(e, "evaluation", "enforced") != nil || path(e, "result", "status") != "denied" {
		t.Errorf("critical action: %v", e)
	}
	if calls := h.ha.recorded(); len(calls) != 2 {
		t.Errorf("Home Assistant calls = %+v", calls)
	}
	if v, _ := h.log.Verify(context.Background()); !v.Valid {
		t.Errorf("the audit log does not verify: %+v", v)
	}
}
