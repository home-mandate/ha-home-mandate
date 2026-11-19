// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Audit mode end to end (SPEC-v0 sections 4.6, 11.5 and 11.6 item 4; issues #170 and #171):
// one administrator proposes it, the approvers are told, the proposal is recorded and
// expires after a day, the proposer cannot confirm it while another administrator could,
// and once confirmed the agent learns no limits, neither in list_my_permissions nor from
// its result, while Home Assistant carries out what the limits would have denied. The demo
// heat pump takes a single setpoint in °C.

const auditHeatPump = "climate.heatpump"

// heatPumpLimits allows reading everything and setting the heat pump between 16 and 23 °C,
// nothing else: no other rule may allow a setpoint without the limits.
func heatPumpLimits(d map[string]any) {
	d["rules"] = []any{
		map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
		map[string]any{"id": "r-heatpump", "resource": map[string]any{"entity_id": auditHeatPump},
			"actions": []any{"set_temperature"}, "decision": "allow",
			"constraints": map[string]any{"temperature": map[string]any{"min": 1600, "max": 2300}}},
	}
}

// mandateDetail is the part of GET api/mandates/{id} the scenario reads.
type mandateDetail struct {
	Summary struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		Digest           string `json:"digest"`
		AuditMode        bool   `json:"audit_mode"`
		AuditModePending bool   `json:"audit_mode_pending"`
	} `json:"summary"`
	Document map[string]any `json:"document"`
	Pending  *struct {
		Digest     string `json:"digest"`
		ProposedAt string `json:"proposed_at"`
		ExpiresAt  string `json:"expires_at"`
	} `json:"pending"`
}

// mandateOf returns the mandate of the agent with display name agent.
func mandateOf(ui *uiClient, agent string) mandateDetail {
	ui.t.Helper()
	var list []struct {
		ID               string `json:"id"`
		AgentDisplayName string `json:"agent_display_name"`
	}
	ui.ok(http.MethodGet, "api/mandates", nil, &list)
	for _, m := range list {
		if m.AgentDisplayName == agent {
			var d mandateDetail
			ui.ok(http.MethodGet, "api/mandates/"+m.ID, nil, &d)
			return d
		}
	}
	ui.t.Fatalf("no mandate of %q", agent)
	return mandateDetail{}
}

// setEnforce stores the mandate as the editor does, its editable fields based on the
// version in force, with "enforce": false for audit mode and without it to enforce.
func setEnforce(ui *uiClient, m mandateDetail, enforce bool) mandateDetail {
	ui.t.Helper()
	draft := map[string]any{}
	for _, k := range []string{"rules", "approval", "limits", "valid_from", "expires"} {
		if v, ok := m.Document[k]; ok {
			draft[k] = v
		}
	}
	if !enforce {
		draft["enforce"] = false
	}
	var out mandateDetail
	ui.ok(http.MethodPut, "api/mandates/"+m.Summary.ID, map[string]any{"name": m.Summary.Name, "draft": draft,
		"base_digest": m.Summary.Digest}, &out)
	return out
}

// heatTo asks for a setpoint on the heat pump.
func heatTo(celsius float64) map[string]any {
	return map[string]any{"entity_id": auditHeatPump, "action": "set_temperature", "params": map[string]any{"temperature": celsius}}
}

func TestAuditModeProposalAndLimits(t *testing.T) {
	const agent = "Audit mode helper"
	notes := watchNotifications(t)
	s := session(t, newAgent(t, agent, heatPumpLimits))
	ready(t, s)

	// Enforced: the agent learns its limits, and 25 °C is refused with them.
	const limits = `"limits":{"set_temperature":[{"temperature":{"max":23,"min":16,"unit":"°C"}}]}`
	perms, errText := call(t, s, "list_my_permissions", map[string]any{"category": "climate"})
	if errText != "" || !strings.Contains(string(mustJSON(t, perms)), limits) {
		t.Fatalf("list_my_permissions while enforced = %s, %q", mustJSON(t, perms), errText)
	}
	const refusal = "denied: no_match: allowed only with temperature 16 to 23 °C"
	if _, errText := call(t, s, "perform_action", heatTo(25)); errText != refusal {
		t.Fatalf("25 °C while enforced = %q, want %q", errText, refusal)
	}

	// The proposal: waiting, told to the approvers, recorded, expiring after a day.
	anna, ben := uiLogin(t, adminApprover), uiLogin(t, adminOther)
	m := setEnforce(anna, mandateOf(anna, agent), false)
	if m.Pending == nil || !m.Summary.AuditModePending || m.Summary.AuditMode {
		t.Fatalf("after the proposal: %+v", m.Summary)
	}
	proposedAt, err1 := time.Parse(time.RFC3339, m.Pending.ProposedAt)
	expiresAt, err2 := time.Parse(time.RFC3339, m.Pending.ExpiresAt)
	if err1 != nil || err2 != nil || expiresAt.Sub(proposedAt) != 24*time.Hour {
		t.Errorf("proposed_at %q, expires_at %q: want a day apart", m.Pending.ProposedAt, m.Pending.ExpiresAt)
	}
	nextNotificationWhere(t, notes, func(n notification) bool {
		return n.Title == "Home-Mandate: audit mode proposed" || n.Title == "Home-Mandate: Probebetrieb vorgeschlagen"
	})
	if !hasLine(auditLog(t), `"event":"mandate.proposed"`, `"`+m.Summary.ID+`"`, `"`+m.Pending.Digest+`"`) {
		t.Error("no mandate.proposed in the audit log")
	}
	if _, errText := call(t, s, "perform_action", heatTo(25)); errText != refusal {
		t.Errorf("25 °C while only proposed = %q, want %q", errText, refusal)
	}

	// The proposer cannot confirm while another administrator could; the other one can.
	confirm := map[string]any{"digest": m.Pending.Digest}
	path := "api/mandates/" + m.Summary.ID + "/audit-mode/confirm"
	if status, body := anna.do(http.MethodPost, path, confirm); status != http.StatusForbidden || !strings.Contains(string(body), "own_proposal") {
		t.Fatalf("the proposer's confirmation = %d %s, want 403 own_proposal", status, body)
	}
	ben.ok(http.MethodPost, path, confirm, &m)
	if !m.Summary.AuditMode || m.Pending != nil {
		t.Fatalf("after the confirmation: %+v", m.Summary)
	}

	// In audit mode: no limits named, and Home Assistant sets what they would have denied.
	perms, errText = call(t, s, "list_my_permissions", map[string]any{"category": "climate"})
	if got := string(mustJSON(t, perms)); errText != "" || strings.Contains(got, "limits") || strings.Contains(got, "23") {
		t.Errorf("list_my_permissions in audit mode = %s, %q; no limits may be named", got, errText)
	}
	out, errText := call(t, s, "perform_action", heatTo(25))
	if got := string(mustJSON(t, out)) + errText; errText != "" || out["status"] != "executed" || strings.Contains(got, "16") || strings.Contains(got, "23") {
		t.Fatalf("25 °C in audit mode = %v, %q; want executed without limits", out, errText)
	}
	eventually(t, "25 °C set in Home Assistant", 15*time.Second, func() bool { return haAttributes(t, auditHeatPump)["temperature"] == 25.0 })

	// Enforced again at once; a new proposal, rejected by the other administrator, is
	// recorded with why it ended, and the limits are named again.
	m = setEnforce(anna, m, true)
	if m.Summary.AuditMode || m.Pending != nil {
		t.Fatalf("enforced again: %+v", m.Summary)
	}
	m = setEnforce(anna, m, false)
	if m.Pending == nil {
		t.Fatalf("second proposal: %+v", m.Summary)
	}
	rejected := m.Pending.Digest
	ben.ok(http.MethodPost, "api/mandates/"+m.Summary.ID+"/audit-mode/discard", nil, &m)
	if m.Pending != nil || m.Summary.AuditMode {
		t.Fatalf("after the rejection: %+v", m.Summary)
	}
	if !hasLine(auditLog(t), `"event":"mandate.discarded"`, `"`+rejected+`"`, `"cause":"rejected"`) {
		t.Error("no mandate.discarded with cause rejected in the audit log")
	}
	if perms, _ := call(t, s, "list_my_permissions", map[string]any{"category": "climate"}); !strings.Contains(string(mustJSON(t, perms)), limits) {
		t.Errorf("list_my_permissions after the rejection = %s", mustJSON(t, perms))
	}
	if out := cli(t, "", "audit", "verify"); !strings.Contains(out, "entries=") {
		t.Errorf("audit verify = %q", out)
	}
}
