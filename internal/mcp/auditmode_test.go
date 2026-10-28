// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
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
	h.auditModeWith(trialRules)
}

// auditModeWith is auditMode with the rules set by rules.
func (h *harness) auditModeWith(rules func(map[string]any)) {
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
		rules(d)
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

// askRules lets the agent read everything and asks before switching lights; a deny rule
// matches switching on in the kitchen as well.
func askRules(d map[string]any) {
	d["rules"] = []any{
		map[string]any{"id": "r-read-all", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
		map[string]any{"id": "r-ask-lights", "resource": map[string]any{"category": "light"}, "actions": []any{"turn_on", "turn_off"}, "decision": "ask"},
		map[string]any{"id": "r-no-kitchen", "resource": map[string]any{"area": "kitchen"}, "actions": []any{"turn_on"}, "decision": "deny"},
	}
}

// trialAskHarness is an approval harness whose mandate has askRules in audit mode.
func trialAskHarness(t *testing.T, f *fakeApprover) *harness {
	t.Helper()
	h := approvalHarnessWith(t, f, askRules)
	h.auditModeWith(askRules)
	return h
}

// checkConfirmEntry checks the last entry: the deny that was not enforced and required a
// confirmation (SPEC-v0 section 9.1), the outcome of the approval request and the result;
// and that the log verifies against the specification's schema.
func checkConfirmEntry(t *testing.T, h *harness, outcome string, result map[string]any) {
	t.Helper()
	e := h.lastEntry()
	if path(e, "evaluation", "decision") != "deny" || path(e, "evaluation", "reason") != "rule" ||
		path(e, "evaluation", "rule_id") != "r-no-kitchen" || path(e, "evaluation", "enforced") != false ||
		path(e, "evaluation", "confirm") != true || path(e, "evaluation", "approval_timeout") != "PT2M" ||
		path(e, "approval", "outcome") != outcome || mustJSON(e["result"]) != mustJSON(result) {
		t.Errorf("audit entry = %s", mustJSON(e))
	}
	if v, err := h.log.Verify(context.Background()); err != nil || !v.Valid {
		t.Errorf("the audit log does not verify: %+v, %v", v, err)
	}
}

// In audit mode a deny rule that wins over a matching ask rule does not take the
// confirmation away (SPEC-v0 sections 4.6 and 11.6, spec issue #59): the approver is asked
// and told that the mandate would deny, the action runs only after the approval, and the
// entry carries confirm: true. The agent sees an ask, nothing of the deny.
func TestAuditModeAsksBeforeADenyThatAnAskRuleMatches(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := trialAskHarness(t, f)
	s := h.session()

	perms, _ := h.call(s, "list_my_permissions", map[string]any{"category": "light"})
	if got := mustJSON(perms["groups"]); got != `[{"actions":{"read":"allow","set":"allow","turn_off":"ask","turn_on":"ask"},"category":"light","entity_ids":["light.kitchen"]}]` {
		t.Errorf("permissions = %s; the agent must see an ask", got)
	}
	out, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"})
	if errText != "" || out["status"] != "executed" {
		t.Fatalf("perform_action = %v %q", out, errText)
	}
	for _, leak := range []string{"r-no-kitchen", "deny", "audit", "enforce", "confirm"} {
		if strings.Contains(mustJSON(out)+mustJSON(perms), leak) {
			t.Errorf("the agent learns %q: %s %s", leak, mustJSON(out), mustJSON(perms))
		}
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].WouldDenyRule != "r-no-kitchen" || reqs[0].Timeout != 2*time.Minute || reqs[0].Approvers[0] != approverID {
		t.Errorf("asked = %+v", reqs)
	}
	if calls := h.ha.recorded(); len(calls) != 1 || calls[0].Service != "turn_on" {
		t.Errorf("Home Assistant calls = %+v", calls)
	}
	checkConfirmEntry(t, h, "approved", map[string]any{"status": "executed"})

	// Outside the kitchen no deny rule matches: a plain ask, unchanged.
	h.addDevice(catalog.Device{EntityID: "light.hall", Category: "light", Areas: []string{"hallway"}, State: "off"})
	if out, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.hall", "action": "turn_on"}); errText != "" || out["status"] != "executed" {
		t.Fatalf("plain ask = %v %q", out, errText)
	}
	if e := h.lastEntry(); path(e, "evaluation", "decision") != "ask" || path(e, "evaluation", "confirm") != nil {
		t.Errorf("plain ask entry = %s", mustJSON(e))
	}
}

// Rejected, timed out or not answerable: not executed, recorded as for an ask, with
// confirm: true. The agent gets the answer of an ask.
func TestAuditModeConfirmationRefused(t *testing.T) {
	for _, tc := range []struct {
		result  approval.Result
		err     error
		errText string
		outcome any
	}{
		{approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}, nil, "denied: approval_rejected", "rejected"},
		{approval.Result{Outcome: approval.OutcomeTimeout, At: answeredAt}, nil, "denied: approval_timeout", "timeout"},
		{approval.Result{}, approval.ErrNoApprover, "denied: no_approver", nil},
	} {
		h := trialAskHarness(t, &fakeApprover{result: tc.result, err: tc.err})
		if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != tc.errText {
			t.Errorf("%v: %q, want %q", tc.outcome, errText, tc.errText)
		}
		if calls := h.ha.recorded(); len(calls) != 0 {
			t.Errorf("%v: Home Assistant called", tc.outcome)
		}
		e := h.lastEntry()
		if path(e, "evaluation", "confirm") != true || path(e, "result", "denied_by") != "approval" || path(e, "approval", "outcome") != tc.outcome {
			t.Errorf("%v: audit entry = %s", tc.outcome, mustJSON(e))
		}
		if v, err := h.log.Verify(context.Background()); err != nil || !v.Valid {
			t.Errorf("%v: the audit log does not verify: %+v, %v", tc.outcome, v, err)
		}
	}
}

// Without anyone to ask (no approval service), it is denied as an ask is.
func TestAuditModeConfirmationWithoutApprovals(t *testing.T) {
	h := newHarness(t, askRules)
	h.auditModeWith(askRules)
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); !strings.HasPrefix(errText, codeApprovalRequired) {
		t.Errorf("perform_action = %q", errText)
	}
	if e := h.lastEntry(); path(e, "evaluation", "confirm") != true || path(e, "result", "denied_by") != "approval" || e["approval"] != nil {
		t.Errorf("audit entry = %s", mustJSON(e))
	}
	if v, err := h.log.Verify(context.Background()); err != nil || !v.Valid {
		t.Errorf("the audit log does not verify: %+v, %v", v, err)
	}
}

// After the confirmation the request is evaluated again (SPEC-v0 section 11.1 item 6,
// decision E2): a confirmed deny with confirm: true runs on allow, ask or again such a
// deny, and any other deny stops it, recorded with the evaluation that was confirmed. A
// confirmed ask is still stopped by every deny, also one that is not enforced.
func TestAuditModeEvaluatedAgainAfterTheConfirmation(t *testing.T) {
	approved := approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}
	move := func(h *harness, entityID string, areas []string, critical bool) {
		dev, _ := h.catalog.Lookup(entityID)
		dev.Areas, dev.Critical = areas, critical
		h.addDevice(dev)
	}
	for name, tc := range map[string]struct {
		entityID string
		during   func(h *harness)
		errText  string
		entry    map[string]any // evaluation decision, confirm and result of the entry
	}{
		"the deny no longer matches: executed as the ask it is now": {"light.kitchen",
			func(h *harness) { move(h, "light.kitchen", []string{"hallway"}, false) }, "",
			map[string]any{"decision": "deny", "confirm": true, "result": map[string]any{"status": "executed"}}},
		"again a deny that requires a confirmation: executed": {"light.kitchen",
			func(h *harness) { move(h, "light.kitchen", []string{"kitchen"}, false) }, "",
			map[string]any{"decision": "deny", "confirm": true, "result": map[string]any{"status": "executed"}}},
		"an enforced deny (the device marked critical): not executed": {"light.kitchen",
			func(h *harness) { move(h, "light.kitchen", []string{"kitchen"}, true) }, "denied: mandate_changed",
			map[string]any{"decision": "deny", "confirm": true, "result": map[string]any{"status": "denied", "denied_by": "mandate"}}},
		"a confirmed ask and then a deny that is not enforced: not executed": {"light.hall",
			func(h *harness) { move(h, "light.hall", []string{"kitchen"}, false) }, "denied: mandate_changed",
			map[string]any{"decision": "ask", "confirm": nil, "result": map[string]any{"status": "denied", "denied_by": "mandate"}}},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeApprover{result: approved}
			h := trialAskHarness(t, f)
			h.addDevice(catalog.Device{EntityID: "light.hall", Category: "light", Areas: []string{"hallway"}, State: "off"})
			f.during = func() { tc.during(h) }
			_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": tc.entityID, "action": "turn_on"})
			if errText != tc.errText {
				t.Errorf("perform_action = %q, want %q", errText, tc.errText)
			}
			if calls := h.ha.recorded(); (len(calls) == 1) != (tc.errText == "") {
				t.Errorf("Home Assistant calls = %+v", calls)
			}
			e := h.lastEntry()
			if path(e, "evaluation", "decision") != tc.entry["decision"] || path(e, "evaluation", "confirm") != tc.entry["confirm"] ||
				path(e, "approval", "outcome") != "approved" || mustJSON(e["result"]) != mustJSON(tc.entry["result"]) {
				t.Errorf("audit entry = %s", mustJSON(e))
			}
			if v, err := h.log.Verify(context.Background()); err != nil || !v.Valid {
				t.Errorf("the audit log does not verify: %+v, %v", v, err)
			}
		})
	}
}

// The rule of SPEC-v0 section 11.1 item 6 for both kinds of confirmed results.
func TestExecutableAfterTheConfirmation(t *testing.T) {
	const digest = "sha256:1"
	ask := evaluator.Result{Decision: evaluator.Ask, MandateDigest: digest}
	allow := evaluator.Result{Decision: evaluator.Allow, MandateDigest: digest}
	confirm := evaluator.Result{Decision: evaluator.Deny, Reason: evaluator.ReasonRule, NotEnforced: true, Confirm: true, MandateDigest: digest}
	notEnforced := evaluator.Result{Decision: evaluator.Deny, Reason: evaluator.ReasonNoMatch, NotEnforced: true, MandateDigest: digest}
	enforced := evaluator.Result{Decision: evaluator.Deny, Reason: evaluator.ReasonRule, MandateDigest: digest}
	other := ask
	other.MandateDigest = "sha256:2"
	for _, tc := range []struct {
		confirmed, now evaluator.Result
		want           bool
	}{
		{ask, allow, true}, {ask, ask, true}, {ask, confirm, false}, {ask, notEnforced, false}, {ask, enforced, false}, {ask, other, false},
		{confirm, allow, true}, {confirm, ask, true}, {confirm, confirm, true}, {confirm, notEnforced, false}, {confirm, enforced, false},
		{confirm, other, false},
	} {
		if got := executableAfterConfirmation(tc.confirmed, tc.now); got != tc.want {
			t.Errorf("confirmed %+v, now %+v: %t, want %t", tc.confirmed, tc.now, got, tc.want)
		}
	}
}
