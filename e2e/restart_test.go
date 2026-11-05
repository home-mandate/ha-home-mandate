// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// restartLight and slowSwitch belong to this file's scenarios only. The switch takes
// slowDelay to turn on, so that the process can be killed during the call.
const (
	restartLight = "light.office_rgbw_lights"
	slowSwitch   = "switch.e2e_slow"
	slowDelay    = 8 * time.Second
)

// killGateway kills the process without warning (SIGKILL) and starts the same container.
func killGateway(t *testing.T) {
	t.Helper()
	if _, err := run("kill", "-s", "KILL", env.hm); err != nil {
		t.Fatal(err)
	}
	if _, err := run("start", env.hm); err != nil {
		t.Fatal(err)
	}
	if err := waitHTTP(env.mcpURL, time.Minute); err != nil {
		t.Fatal(err)
	}
	direct, err := hostPort(env.hm, "8099")
	if err != nil {
		t.Fatal(err)
	}
	env.uiDirect = "http://" + direct
}

// askRule makes every action on entityID need a confirmation.
func askRule(entityID string) func(map[string]any) {
	return func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-restart", "resource": map[string]any{"entity_id": entityID},
			"actions": []any{"turn_on", "turn_off"}, "decision": "ask"}}, d["rules"].([]any)...)
	}
}

// toggle is the action that changes a light from state.
func toggle(state string) string {
	if state == "on" {
		return "turn_off"
	}
	return "turn_on"
}

// pendingRequest asks and waits for the pending result, which comes after HM_APPROVAL_WAIT
// with nobody having answered, and for the notification; it returns both.
func pendingRequest(t *testing.T, s *sdk.ClientSession, notes <-chan notification, entityID, action string) (string, notification) {
	t.Helper()
	ch := callAsync(t, s, "perform_action", map[string]any{"entity_id": entityID, "action": action})
	n := nextApproval(t, notes)
	env.secrets = append(env.secrets, strings.TrimPrefix(n.Actions[0], "HM_APPROVE_"))
	r := awaitResult(t, ch)
	id, _ := r.out["approval_id"].(string)
	if r.errText != "" || r.out["status"] != "pending" || id == "" {
		t.Fatalf("request = %v, %q", r.out, r.errText)
	}
	return id, n
}

// nextTitled waits for a notification whose title starts with prefix.
func nextTitled(t *testing.T, ch <-chan notification, prefix string) notification {
	t.Helper()
	for {
		if n := nextNotification(t, ch); strings.HasPrefix(n.Title, prefix) {
			return n
		}
	}
}

// Issue #34: the process is killed while a request waits for its answer. After the start
// nothing is executed, the audit log says why, approval_status answers from the journal,
// and the notification without buttons replaces the one that asked.
func TestRestartWhileARequestIsOpen(t *testing.T) {
	notes := watchNotifications(t)
	token := newAgent(t, "Killed while open", askRule(restartLight))
	s := session(t, token)
	ready(t, s)
	before := haState(t, restartLight)
	id, _ := pendingRequest(t, s, notes, restartLight, toggle(before))

	killGateway(t)

	after := session(t, token)
	ready(t, after)
	if n := nextTitled(t, notes, "Approval request ended"); len(n.Actions) != 0 {
		t.Errorf("the replacement has buttons: %v", n.Actions)
	}
	if _, errText := call(t, after, "approval_status", map[string]any{"approval_id": id}); errText != "denied: approval_interrupted" {
		t.Errorf("approval_status = %q", errText)
	}
	time.Sleep(2 * time.Second)
	if state := haState(t, restartLight); state != before {
		t.Errorf("%s changed from %q to %q", restartLight, before, state)
	}
	if !hasLine(auditLog(t), `"entity_id":"`+restartLight+`"`, `"outcome":"cancelled"`, `"cause":"interrupted"`) {
		t.Error("no cancelled/interrupted entry in the audit log")
	}
}

// Issue #34: the process is killed during the confirmed call. Whether it was executed is
// unknown: the audit log and approval_status say so, the person is told to check, and the
// call is not repeated.
func TestRestartWhileAConfirmedRequestIsExecuting(t *testing.T) {
	notes := watchNotifications(t)
	token := newAgent(t, "Killed while executing", askRule(slowSwitch))
	s := session(t, token)
	ready(t, s)
	id, n := pendingRequest(t, s, notes, slowSwitch, "turn_on")

	answer(t, adminApprover, n.Actions[0])
	time.Sleep(slowDelay / 4) // the answer has arrived, the call is under way
	killGateway(t)

	after := session(t, token)
	ready(t, after)
	if n := nextTitled(t, notes, "Please check"); len(n.Actions) != 0 {
		t.Errorf("the replacement has buttons: %v", n.Actions)
	}
	if _, errText := call(t, after, "approval_status", map[string]any{"approval_id": id}); errText != "failed: outcome_unknown" {
		t.Errorf("approval_status = %q", errText)
	}
	log := auditLog(t)
	if !hasLine(log, `"entity_id":"`+slowSwitch+`"`, `"status":"failed"`, `"error":"outcome_unknown"`, `"outcome":"approved"`) {
		t.Error("no failed/outcome_unknown entry in the audit log")
	}
	if hasLine(log, `"entity_id":"`+slowSwitch+`"`, `"status":"executed"`) {
		t.Error("recorded as executed")
	}
}

// Issue #34: the process is killed after the execution, before the agent fetched the
// result. The agent still gets it, and nothing is executed again.
func TestRestartAfterAnExecutionTheAgentHasNotFetched(t *testing.T) {
	notes := watchNotifications(t)
	token := newAgent(t, "Killed before fetching", askRule(restartLight))
	s := session(t, token)
	ready(t, s)
	before := haState(t, restartLight)
	id, n := pendingRequest(t, s, notes, restartLight, toggle(before))

	answer(t, adminApprover, n.Actions[0])
	eventually(t, "light changed in Home Assistant", 10*time.Second, func() bool { return haState(t, restartLight) != before })
	eventually(t, "execution recorded", 10*time.Second, func() bool {
		return hasLine(cli(t, "", "audit", "export"), `"entity_id":"`+restartLight+`"`, `"status":"executed"`)
	})
	killGateway(t)

	after := session(t, token)
	ready(t, after)
	out, errText := call(t, after, "approval_status", map[string]any{"approval_id": id})
	if errText != "" || out["status"] != "executed" {
		t.Errorf("approval_status = %v, %q", out, errText)
	}
	time.Sleep(2 * time.Second)
	if state := haState(t, restartLight); state == before {
		t.Errorf("%s back to %q: executed again?", restartLight, state)
	}
	executed := 0
	for _, line := range strings.Split(auditLog(t), "\n") {
		if strings.Contains(line, `"entity_id":"`+restartLight+`"`) && strings.Contains(line, `"status":"executed"`) {
			executed++
		}
	}
	if executed != 1 {
		t.Errorf("%d executed entries, want 1", executed)
	}
}
