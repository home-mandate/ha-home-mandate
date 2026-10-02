// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolResult struct {
	out     map[string]any
	errText string
}

// callAsync runs a tool call that waits for a human.
func callAsync(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) <-chan toolResult {
	ch := make(chan toolResult, 1)
	go func() {
		out, errText := call(t, s, tool, args)
		ch <- toolResult{out, errText}
	}()
	return ch
}

func awaitResult(t *testing.T, ch <-chan toolResult) toolResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(45 * time.Second):
		t.Fatal("the tool call did not return")
		return toolResult{}
	}
}

// statusWith returns the HTTP status of an MCP request with token.
func statusWith(t *testing.T, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, env.mcpURL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func unlockArgs(entityID string) map[string]any {
	return map[string]any{"entity_id": entityID, "action": "unlock", "reason": "The parcel service is at the door"}
}

// Scenario 2: open door → approval request → admin-approver confirms → executed.
func TestScenario02ApprovedDoorOpens(t *testing.T) {
	notes := watchNotifications(t)
	s := session(t, newAgent(t, "Door helper", nil))
	ready(t, s)
	if state := haState(t, "lock.front_door"); state != "locked" {
		t.Fatalf("lock.front_door is %q before the test, want locked (demo default)", state)
	}
	result := callAsync(t, s, "perform_action", unlockArgs("lock.front_door"))
	n := nextNotification(t, notes)
	if n.Service != "persistent_notification" || n.Title != "Approval needed: Door helper" || len(n.Actions) != 2 ||
		!strings.Contains(n.Message, "not verified: The parcel service is at the door") {
		t.Fatalf("notification = %+v", n)
	}
	env.secrets = append(env.secrets, strings.TrimPrefix(n.Actions[0], "HM_APPROVE_"))
	answer(t, adminApprover, n.Actions[0])
	if r := awaitResult(t, result); r.errText != "" || r.out["status"] != "executed" {
		t.Fatalf("perform_action = %v, %q", r.out, r.errText)
	}
	eventually(t, "door unlocked in Home Assistant", 15*time.Second, func() bool { return haState(t, "lock.front_door") == "unlocked" })
	if !hasLine(auditLog(t), `"entity_id":"lock.front_door"`, `"outcome":"approved"`, `"by":"`+env.users[adminApprover].id+`"`, `"status":"executed"`) {
		t.Error("no approved and executed decision for lock.front_door in the audit log")
	}
}

// Scenario 3: open door → approval request → no answer → denied after the timeout, the
// door stays closed.
func TestScenario03NoAnswerKeepsTheDoorClosed(t *testing.T) {
	notes := watchNotifications(t)
	s := session(t, newAgent(t, "Impatient", nil))
	ready(t, s)
	if state := haState(t, "lock.openable_lock"); state != "locked" {
		t.Fatalf("lock.openable_lock is %q before the test, want locked (demo default)", state)
	}
	start := time.Now()
	result := callAsync(t, s, "perform_action", unlockArgs("lock.openable_lock"))
	nextNotification(t, notes)
	r := awaitResult(t, result)
	if r.errText != "denied: approval_timeout" || time.Since(start) < 10*time.Second {
		t.Errorf("perform_action = %q after %v", r.errText, time.Since(start))
	}
	if state := haState(t, "lock.openable_lock"); state != "locked" {
		t.Errorf("lock.openable_lock is %q", state)
	}
	if !hasLine(auditLog(t), `"entity_id":"lock.openable_lock"`, `"outcome":"timeout"`, `"denied_by":"approval"`) {
		t.Error("no timed-out decision in the audit log")
	}
}

// Scenario 4: open door → answer from admin-other → discarded, denied, audit entry
// "invalid approval"; the approver is warned (decision W8).
func TestScenario04AnswerFromANonApprover(t *testing.T) {
	notes := watchNotifications(t)
	s := session(t, newAgent(t, "Forged answer", nil))
	ready(t, s)
	result := callAsync(t, s, "perform_action", unlockArgs("lock.openable_lock"))
	n := nextNotification(t, notes)
	env.secrets = append(env.secrets, strings.TrimPrefix(n.Actions[0], "HM_APPROVE_"))
	answer(t, adminOther, n.Actions[0])
	if r := awaitResult(t, result); r.errText != "denied: approval_invalid" {
		t.Errorf("perform_action = %q", r.errText)
	}
	other := env.users[adminOther].id
	warning := nextNotification(t, notes)
	if warning.Title != "Warning: unauthorised answer" || !strings.Contains(warning.Message, other) || len(warning.Actions) != 0 {
		t.Errorf("warning = %+v", warning)
	}
	if state := haState(t, "lock.openable_lock"); state != "locked" {
		t.Errorf("lock.openable_lock is %q", state)
	}
	if !hasLine(auditLog(t), `"entity_id":"lock.openable_lock"`, `"outcome":"invalid_response"`, `"by":"`+other+`"`) {
		t.Error("no invalid_response decision in the audit log")
	}
}

// Scenario 6: revoke an agent → the next request with the old token is denied. (The UI
// variant follows in week 4; the administration command is used here.)
func TestScenario06RevokedAgentIsLockedOut(t *testing.T) {
	token := newAgent(t, "Revoke me", nil)
	if status := statusWith(t, token); status != http.StatusOK {
		t.Fatalf("before revocation: %d", status)
	}
	cli(t, "", "agent", "revoke", clientIDOf(t, "Revoke me"))
	if status := statusWith(t, token); status != http.StatusUnauthorized {
		t.Errorf("after revocation: %d", status)
	}
}

// Scenario 7: emergency stop → all agents blocked immediately; lifting it → only newly
// issued tokens work.
func TestScenario07EmergencyStop(t *testing.T) {
	a, b := newAgent(t, "Stop A", nil), newAgent(t, "Stop B", nil)
	stopped := false
	t.Cleanup(func() {
		if stopped {
			cli(t, "", "emergency-stop", "off")
		}
	})
	cli(t, "", "emergency-stop", "on")
	stopped = true
	for name, token := range map[string]string{"A": a, "B": b} {
		if status := statusWith(t, token); status != http.StatusUnauthorized {
			t.Errorf("agent %s during the stop: %d", name, status)
		}
	}
	// Admission is refused during the stop, even when a human agrees.
	p := requestPairing(t)
	if res := pairAs(t, p, adminApprover, "During stop", lastTemplate(t)); res.status != http.StatusOK {
		t.Fatalf("consent during the stop = %d", res.status)
	}
	if status, out := pollTokens(t, p); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("tokens during the stop = %d %v", status, out)
	}

	cli(t, "", "emergency-stop", "off")
	stopped = false
	if status := statusWith(t, a); status != http.StatusUnauthorized {
		t.Errorf("old token after the stop: %d", status)
	}
	if status := statusWith(t, newAgent(t, "Stop C", nil)); status != http.StatusOK {
		t.Errorf("new token after the stop: %d", status)
	}
	log := auditLog(t)
	if !strings.Contains(log, `"event":"emergency_stop.activated"`) || !strings.Contains(log, `"event":"emergency_stop.released"`) {
		t.Error("emergency stop not in the audit log")
	}
}

// lastTemplate returns the name of a stored mandate template.
func lastTemplate(t *testing.T) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(cli(t, "", "mandate", "template", "list")), "\n")
	return strings.Split(lines[len(lines)-1], "\t")[0]
}

// Scenario 10: user-plain tries to admit an agent → denied.
func TestScenario10PlainUserCannotAdmit(t *testing.T) {
	newAgent(t, "Template source", nil) // makes sure a template and the approver exist
	p := requestPairing(t)
	res := pairAs(t, p, userPlain, "Not allowed", lastTemplate(t))
	if res.status != http.StatusForbidden || !strings.Contains(res.body, "administrators") {
		t.Errorf("sign-in of user-plain = %d\n%s", res.status, res.body)
	}
	if status, out := pollTokens(t, p); status != http.StatusBadRequest || out["error"] != "authorization_pending" {
		t.Errorf("tokens = %d %v", status, out)
	}
	if !hasLine(auditLog(t), `"event":"auth.rejected"`, `"error":"not_admin"`, `"id":"`+env.users[userPlain].id+`"`) {
		t.Error("refused admission not in the audit log")
	}
}
