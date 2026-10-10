// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// Tests of the short wait, the pending result, approval_status and approval_cancel
// (issue #27, SPEC-v0 section 11.1 items 4, 5, 7 and 8).

var approvalIDPattern = regexp.MustCompile(`^apr_[0-9a-f]{32}$`)

// pendingHarness holds every request until the test ends it; tool calls wait 100 ms.
func pendingHarness(t *testing.T) (*harness, *fakeApprover) {
	t.Helper()
	f := &fakeApprover{hold: true}
	h := newHarness(t, nil)
	f.journal, h.approver, h.approvalWait = h.journal, f, 100*time.Millisecond
	h.url = h.serve(h.log)
	return h, f
}

func (h *harness) sessionWith(token string) *sdk.ClientSession {
	h.t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: h.url, HTTPClient: &http.Client{Transport: bearer{token}},
	}, nil)
	if err != nil {
		h.t.Fatalf("connect: %v", err)
	}
	h.t.Cleanup(func() { _ = s.Close() })
	return s
}

// pendingUnlock asks to unlock and expects the pending result; it returns the approval ID
// and the request's ID.
func pendingUnlock(t *testing.T, h *harness, f *fakeApprover, entity ...string) (string, string) {
	t.Helper()
	entityID := "lock.front_door"
	if len(entity) > 0 {
		entityID = entity[0]
	}
	res, err := h.session().CallTool(context.Background(), &sdk.CallToolParams{Name: "perform_action",
		Arguments: map[string]any{"entity_id": entityID, "action": "unlock", "reason": "parcel"}})
	if err != nil || res.IsError {
		t.Fatalf("perform_action = %+v, %v", res, err)
	}
	out, _ := res.StructuredContent.(map[string]any)
	ref, _ := out["approval_id"].(string)
	if out["status"] != "pending" || !approvalIDPattern.MatchString(ref) || out["open_until"] == nil || out["next"] != "approval_status" {
		t.Fatalf("structured = %v", out)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	for _, want := range []string{"NOT EXECUTED YET", "Do not tell the user it was done", ref, "approval_status", "approval_cancel", "unlock on " + entityID} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q: %s", want, text)
		}
	}
	reqs := f.requests()
	return ref, reqs[len(reqs)-1].Record.AgentRef
}

// approvedNow is a confirmation given now by the gateway's clock.
func approvedNow(h *harness) approval.Result {
	return approval.Result{Outcome: approval.OutcomeApproved, By: approverID, Via: approval.ViaPush, At: h.now.Now()}
}

func (f *fakeApprover) heldIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for id := range f.held {
		ids = append(ids, id)
	}
	return ids
}

// Without an answer within the bound the agent gets the pending result, nothing is
// executed; the confirmation executes at once, and approval_status returns the result.
func TestPendingResultThenTheOutcome(t *testing.T) {
	h, f := pendingHarness(t)
	ref, agentRef := pendingUnlock(t, h, f)
	if ref != agentRef {
		t.Fatalf("approval ID %s, journal %s", ref, agentRef)
	}
	if len(h.ha.recorded()) != 0 {
		t.Fatal("executed before an answer")
	}
	// Still waiting: the status says so, again with the ID.
	if out, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "" || out["status"] != "pending" || out["approval_id"] != ref {
		t.Errorf("status while waiting = %v, %q", out, errText)
	}
	id := f.heldIDs()[0]
	f.end(id, approvedNow(h))
	// The execution follows the confirmation, not the status call.
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	if out, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "" || out["status"] != "executed" {
		t.Errorf("status after the answer = %v, %q", out, errText)
	}
	if out, _ := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); out["status"] != "executed" || len(h.ha.recorded()) != 1 {
		t.Errorf("second status = %v, %d calls", out, len(h.ha.recorded()))
	}
	if e := h.lastEntry(); path(e, "result", "status") != "executed" || path(e, "approval", "outcome") != "approved" {
		t.Errorf("audit entry = %v", e)
	}
	if state, _ := h.journalRow(id); state != "ended" {
		t.Errorf("journal = %s", state)
	}
}

// An answer within the bound gives the result as before; a refusal while the agent polls
// is the same error the synchronous call would have given.
func TestStatusGivesTheRefusalAsTheCallWould(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	f.end(f.heldIDs()[0], approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: time.Now()})
	if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "denied: approval_rejected" {
		t.Errorf("status = %q", errText)
	}
	h.approvalWait = 5 * time.Second
	h.url = h.serve(h.log)
	h.now.advance(cooldownMax)
	go func() {
		waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
		f.end(f.heldIDs()[0], approvedNow(h))
	}()
	if out, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); errText != "" || out["status"] != "executed" {
		t.Errorf("answer within the bound = %v, %q", out, errText)
	}
}

// The agent's call ending (cut off, cancelled) ends nothing: the request stays open, and
// a confirmation afterwards executes the action exactly once.
func TestAnAbandonedCallLeavesTheRequestOpen(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := newHarness(t, nil)
	f.journal, h.approver, h.approvalWait = h.journal, f, time.Minute
	h.url = h.serve(h.log)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _ = h.session().CallTool(ctx, &sdk.CallToolParams{Name: "perform_action",
		Arguments: map[string]any{"entity_id": "lock.front_door", "action": "unlock"}})
	waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
	time.Sleep(50 * time.Millisecond)
	id := f.heldIDs()[0]
	if state, _ := h.journalRow(id); state != "open" || len(h.ha.recorded()) != 0 {
		t.Fatalf("after the call ended: journal %s, calls %d", state, len(h.ha.recorded()))
	}
	f.end(id, approvedNow(h))
	waitFor(t, func() bool { state, _ := h.journalRow(id); return state == "ended" })
	if calls := h.ha.recorded(); len(calls) != 1 {
		t.Errorf("executed %d times", len(calls))
	}
	if e := h.lastEntry(); path(e, "result", "status") != "executed" {
		t.Errorf("audit entry = %v", e)
	}
}

// SPEC-v0 section 11.1 item 8: the agent withdraws its own open request; nobody answered,
// so it is cancelled/withdrawn. An answered request, another agent's or an unknown one
// cannot be withdrawn.
func TestApprovalCancel(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	id := f.heldIDs()[0]
	if out, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": ref}); errText != "" || out["status"] != "withdrawn" {
		t.Fatalf("cancel = %v, %q", out, errText)
	}
	e := h.lastEntry()
	if path(e, "approval", "outcome") != "cancelled" || path(e, "approval", "cause") != "withdrawn" || path(e, "approval", "by") != nil ||
		path(e, "result", "denied_by") != "approval" {
		t.Errorf("audit entry = %v", e)
	}
	if state, outcome := h.journalRow(id); state != "ended" || outcome != "cancelled" {
		t.Errorf("journal = %s %s", state, outcome)
	}
	if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "denied: approval_withdrawn" {
		t.Errorf("status = %q", errText)
	}
	if _, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": ref}); !strings.HasPrefix(errText, "conflict") {
		t.Errorf("second cancel = %q", errText)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("a withdrawn request was executed")
	}

	// Answered already: the answer stands.
	h.now.advance(cooldownMax)
	ref, _ = pendingUnlock(t, h, f)
	f.end(f.heldIDs()[0], approvedNow(h))
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	if _, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": ref}); !strings.HasPrefix(errText, "conflict") {
		t.Errorf("cancel after the answer = %q", errText)
	}
}

// An approval ID is bound to its agent: another agent, an unknown ID and a malformed one
// get the same answer, and nothing about the request.
func TestApprovalIDsAreBoundToTheAgent(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	other, err := h.agents.Register(context.Background(), "Other", admin)
	if err != nil {
		t.Fatal(err)
	}
	s := h.sessionWith(h.issue(other.ClientID))
	var answers []string
	for _, args := range []struct{ tool, id string }{
		{"approval_status", ref}, {"approval_cancel", ref}, {"approval_status", "apr_" + strings.Repeat("0", 32)},
		{"approval_cancel", "apr_" + strings.Repeat("0", 32)}, {"approval_status", "x"},
	} {
		_, errText := h.call(s, args.tool, map[string]any{"approval_id": args.id})
		answers = append(answers, errText)
	}
	for _, a := range answers {
		if a != answers[0] || !strings.HasPrefix(a, "not_found") {
			t.Errorf("answers = %q", answers)
			break
		}
	}
	if len(f.cancelled) != 0 {
		t.Errorf("another agent withdrew %v", f.cancelled)
	}
}

// SPEC-v0 section 11.1 item 7: a request counts until it ends, polled or not; a third is
// denied without asking anyone; an end frees the place.
func TestPendingRequestsCountUntilTheyEnd(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	pendingUnlock(t, h, f, "lock.back_door")
	if errText := unlock(h, map[string]any{"entity_id": "lock.garden_gate"}); errText != "denied: approval_pending" || len(f.requests()) != 2 {
		t.Errorf("third = %q, %d asked", errText, len(f.requests()))
	}
	for _, id := range f.heldIDs() {
		var agentRef string
		_ = h.db.QueryRow(`SELECT agent_ref FROM approval_journal WHERE id = ?`, id).Scan(&agentRef)
		if agentRef == ref {
			f.end(id, approvedNow(h))
		}
	}
	// The outcome is final, so the request no longer counts.
	waitFor(t, func() bool {
		out, _ := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref})
		return out["status"] == "executed"
	})
	pendingUnlock(t, h, f, "lock.garden_gate")
	if len(f.requests()) != 3 {
		t.Errorf("%d asked", len(f.requests()))
	}
}

// Status calls are no actions: rate limited on their own, not counted towards
// max_actions_per_hour, no audit entry.
func TestStatusCallsAreLimitedAndNotActions(t *testing.T) {
	h, _ := pendingHarness(t)
	s := h.session()
	before := h.auditLog()
	id := "apr_" + strings.Repeat("0", 32)
	for range statusPerHour {
		if _, errText := h.call(s, "approval_status", map[string]any{"approval_id": id}); !strings.HasPrefix(errText, "not_found") {
			t.Fatalf("status = %q", errText)
		}
	}
	if _, errText := h.call(s, "approval_status", map[string]any{"approval_id": id}); errText != codeRateLimited {
		t.Errorf("over the limit = %q", errText)
	}
	if _, errText := h.call(s, "approval_cancel", map[string]any{"approval_id": id}); errText != codeRateLimited {
		t.Errorf("cancel over the limit = %q", errText)
	}
	if h.auditLog() != before {
		t.Error("status calls are in the audit log")
	}
	// Actions are not affected (the mandate allows 60 an hour).
	if _, errText := h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "" {
		t.Errorf("action after many status calls = %q", errText)
	}
}

// After a restart approval_status answers from the journal: a waiting request was
// ended as interrupted, an execution under way with an unknown outcome.
func TestStatusAfterARestart(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	h.now.advance(cooldownMax)
	ref2, _ := pendingUnlock(t, h, f, "lock.back_door")
	row2 := ""
	for _, id := range f.heldIDs() {
		var agentRef string
		_ = h.db.QueryRow(`SELECT agent_ref FROM approval_journal WHERE id = ?`, id).Scan(&agentRef)
		if agentRef == ref2 {
			row2 = id
		}
	}
	if err := h.journal.Executing(context.Background(), row2, audit.Approval{Outcome: "approved", By: approverID, Via: "push", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// The restart: a new gateway on the same database after the recovery.
	if _, err := h.journal.Recover(context.Background(), h.log); err != nil {
		t.Fatal(err)
	}
	h.approver = &fakeApprover{}
	h.url = h.serve(h.log)
	if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "denied: approval_interrupted" {
		t.Errorf("waiting request = %q", errText)
	}
	if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref2}); errText != "failed: outcome_unknown" {
		t.Errorf("execution under way = %q", errText)
	}
	if _, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": ref}); !strings.HasPrefix(errText, "conflict") {
		t.Errorf("cancel after the restart = %q", errText)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("executed after a restart")
	}
}

// Emergency stop and revocation end pending requests as before; the status reports the
// denial (after an emergency stop with a token issued after it).
func TestStatusAfterEmergencyStopAndRevocation(t *testing.T) {
	for name, tc := range map[string]struct {
		cause, want string
		during      func(h *harness)
	}{
		"emergency stop": {audit.CauseEmergencyStop, "denied: emergency_stop", func(*harness) {}},
		"mandate revoked": {audit.CauseRevoked, "denied: revoked", func(h *harness) {
			_ = h.mandates.Revoke(context.Background(), "m-voice-assistant", admin)
		}},
	} {
		h, f := pendingHarness(t)
		ref, _ := pendingUnlock(t, h, f)
		tc.during(h)
		f.end(f.heldIDs()[0], approval.Result{Outcome: approval.OutcomeCancelled, Cause: tc.cause, At: time.Now()})
		if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != tc.want {
			t.Errorf("%s: status = %q", name, errText)
		}
		if e := h.lastEntry(); path(e, "approval", "cause") != tc.cause || len(h.ha.recorded()) != 0 {
			t.Errorf("%s: audit entry = %v", name, e)
		}
	}
}

// The journal's outcome gives the same reason codes as the synchronous answer.
func TestJournalReasonCodes(t *testing.T) {
	for _, tc := range []struct {
		st   approval.Status
		want string
	}{
		{approval.Status{Outcome: "rejected", Result: audit.Result{Status: "denied", DeniedBy: "approval"}}, "denied: approval_rejected"},
		{approval.Status{Outcome: "timeout", Result: audit.Result{Status: "denied", DeniedBy: "approval"}}, "denied: approval_timeout"},
		{approval.Status{Outcome: "invalid_response", Result: audit.Result{Status: "denied", DeniedBy: "approval"}}, "denied: approval_invalid"},
		{approval.Status{Outcome: "cancelled", Cause: "revoked", Result: audit.Result{Status: "denied", DeniedBy: "authentication"}}, "denied: unauthorized"},
		{approval.Status{Outcome: "cancelled", Cause: "revoked", Result: audit.Result{Status: "denied", DeniedBy: "mandate"}}, "denied: revoked"},
		{approval.Status{Outcome: "cancelled", Cause: "x", Result: audit.Result{Status: "denied", DeniedBy: "approval"}}, "denied: approval_cancelled"},
		{approval.Status{Outcome: "approved", Result: audit.Result{Status: "denied", DeniedBy: "emergency_stop"}}, "denied: emergency_stop"},
		{approval.Status{Outcome: "approved", Result: audit.Result{Status: "denied", DeniedBy: "authentication"}}, "denied: unauthorized"},
		{approval.Status{Outcome: "approved", Result: audit.Result{Status: "denied", DeniedBy: "mandate"}}, "denied: mandate_changed"},
		{approval.Status{Outcome: "approved", Result: audit.Result{Status: "denied", DeniedBy: "approval"}}, "denied: approval_expired"},
		{approval.Status{Result: audit.Result{Status: "denied", DeniedBy: "approval"}}, "denied: no_approver"},
		{approval.Status{Outcome: "approved", Result: audit.Result{Status: "failed"}}, "failed"},
	} {
		if _, _, err := journalResult(tc.st, true); err == nil || err.Error() != tc.want {
			t.Errorf("%+v: %v, want %s", tc.st, err, tc.want)
		}
	}
	if _, out, err := journalResult(approval.Status{Result: audit.Result{Status: "executed"}}, true); err != nil || out.Status != "executed" {
		t.Errorf("executed = %v, %v", out, err)
	}
}

// pushes records the notifications the real approval service sends.
type pushes struct {
	mu   sync.Mutex
	sent []ha.Notification
}

func (p *pushes) Notify(_ context.Context, _ string, n ha.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, n)
	return nil
}

func (p *pushes) nonce() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.sent {
		for _, a := range n.Actions {
			if strings.HasPrefix(a.Action, "HM_APPROVE_") {
				return strings.TrimPrefix(a.Action, "HM_APPROVE_")
			}
		}
	}
	return ""
}

// With the real approval service: the call returns the pending result, the human
// confirms on the phone afterwards, the gateway executes once at that moment, and
// approval_status gives the result.
func TestPendingWithTheApprovalService(t *testing.T) {
	h := newHarness(t, nil)
	approvers := approval.NewApprovers(h.db, h.log)
	if err := approvers.Put(context.Background(), approval.Approver{UserID: approverID,
		Devices: []approval.Device{{Service: "mobile_app_phone", Critical: true}}}, audit.Actor{Kind: audit.ActorUser, ID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	push := &pushes{}
	svc := approval.New(approval.Config{Approvers: approvers, Notifier: push, Journal: h.journal, ServiceUser: h.serviceUser})
	h.approver, h.approvalWait = svc, 100*time.Millisecond
	h.url = h.serve(h.log)
	res, err := h.session().CallTool(context.Background(), &sdk.CallToolParams{Name: "perform_action",
		Arguments: map[string]any{"entity_id": "lock.front_door", "action": "unlock"}})
	if err != nil || res.IsError {
		t.Fatalf("perform_action = %+v, %v", res, err)
	}
	ref := res.StructuredContent.(map[string]any)["approval_id"].(string)
	data, _ := json.Marshal(map[string]any{"action": "HM_APPROVE_" + push.nonce()})
	svc.HandleEvent(ha.Event{EventType: ha.EventMobileAppNotificationAction, Data: data, Context: ha.EventContext{UserID: approverID}})
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	if out, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); out["status"] != "executed" {
		t.Errorf("status = %v, %q", out, errText)
	}
	if len(svc.Open()) != 0 || len(h.ha.recorded()) != 1 {
		t.Errorf("open %v, calls %d", svc.Open(), len(h.ha.recorded()))
	}
}

// An answer within the bound whose execution takes long does not hold the call beyond a
// short grace: the agent gets the pending result and the outcome through approval_status.
func TestAnAnsweredCallWaitsOnlyAGrace(t *testing.T) {
	h, f := pendingHarness(t)
	h.approvalWait = 2 * time.Second
	h.url = h.serve(h.log)
	old := answerGrace
	answerGrace = 100 * time.Millisecond
	defer func() { answerGrace = old }()
	release := make(chan struct{})
	h.ha.onCall = func() { <-release }
	defer close(release)
	go func() {
		waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
		f.end(f.heldIDs()[0], approvedNow(h))
	}()
	start := time.Now()
	res, err := h.session().CallTool(context.Background(), &sdk.CallToolParams{Name: "perform_action",
		Arguments: map[string]any{"entity_id": "lock.front_door", "action": "unlock"}})
	if err != nil || res.IsError || time.Since(start) > time.Second {
		t.Fatalf("perform_action = %+v, %v after %v", res, err, time.Since(start))
	}
	// Answered, still executing: pending, but it must not say nobody answered.
	out := res.StructuredContent.(map[string]any)
	text := res.Content[0].(*sdk.TextContent).Text
	if out["status"] != "pending" || out["phase"] != "answered" || !strings.Contains(text, "confirmed by a human") ||
		!strings.Contains(text, "NOT FINISHED YET") || strings.Contains(text, "waiting for a human") || strings.Contains(text, "nothing has happened") {
		t.Errorf("result = %v: %s", out, text)
	}
}

// Statuses from the journal give the codes the synchronous call gives.
func TestJournalFailureCodes(t *testing.T) {
	for errCode, want := range map[string]string{"ha_error": "failed", "mandate_unavailable": "unavailable", "journal_unavailable": "unavailable",
		"clock_behind": "unavailable", "ha_unavailable": "unavailable", "outcome_unknown": "failed: outcome_unknown"} {
		_, _, err := journalResult(approval.Status{Outcome: "approved", Result: audit.Result{Status: "failed", Error: errCode}}, true)
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %s", errCode, err, want)
		}
	}
}

// On shutdown the door closes first: an answer accepted before is settled (the gateway
// waits for its execution), one after is refused, and a request that ends afterwards
// without an answer is left to the next start: its journal row stays.
func TestCloseSettlesAcceptedAnswersAndRefusesLaterOnes(t *testing.T) {
	h, f := pendingHarness(t)
	pendingUnlock(t, h, f)
	pendingUnlock(t, h, f, "lock.back_door")
	ids := []string{fmt.Sprintf("%032x", 1), fmt.Sprintf("%032x", 2)} // the fake's IDs, in order
	release := make(chan struct{})
	h.ha.onCall = func() { <-release }
	f.end(ids[0], approvedNow(h))
	waitFor(t, func() bool { state, _ := h.journalRow(ids[0]); return state == "executing" })
	closed := make(chan bool, 1)
	go func() { closed <- h.gw.Close(5 * time.Second) }()
	select {
	case <-closed:
		t.Fatal("Close did not wait for the execution under way")
	case <-time.After(100 * time.Millisecond):
	}
	if f.end(ids[1], approvedNow(h)) {
		t.Error("an answer was taken after the door closed")
	}
	close(release)
	if !<-closed {
		t.Error("Close reported unfinished work")
	}
	if state, _ := h.journalRow(ids[0]); state != "ended" {
		t.Errorf("executed request: %s", state)
	}
	f.end(ids[1], approval.Result{Outcome: approval.OutcomeTimeout, At: time.Now()})
	time.Sleep(50 * time.Millisecond)
	if state, _ := h.journalRow(ids[1]); state != "open" || len(h.ha.recorded()) != 1 {
		t.Errorf("after Close: %s, %d calls", state, len(h.ha.recorded()))
	}
	if !h.gw.Close(time.Millisecond) {
		t.Error("second Close")
	}
}

// An answer racing with Close is either settled with that answer or discarded; it is
// never recorded as cancelled after it was accepted (SPEC-v0 section 11.1 item 8).
func TestAnAnswerRacingWithCloseIsNeverLost(t *testing.T) {
	for i := range 20 {
		h := newHarness(t, nil)
		approvers := approval.NewApprovers(h.db, h.log)
		if err := approvers.Put(context.Background(), approval.Approver{UserID: approverID,
			Devices: []approval.Device{{Service: "mobile_app_phone", Critical: true}}}, audit.Actor{Kind: audit.ActorUser, ID: admin.ID}); err != nil {
			t.Fatal(err)
		}
		push := &pushes{}
		svc := approval.New(approval.Config{Approvers: approvers, Notifier: push, Journal: h.journal, ServiceUser: h.serviceUser})
		h.approver, h.approvalWait = svc, 50*time.Millisecond
		h.url = h.serve(h.log)
		if out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); out["status"] != "pending" {
			t.Fatalf("perform_action = %v", out)
		}
		data, _ := json.Marshal(map[string]any{"action": "HM_APPROVE_" + push.nonce()})
		var wg sync.WaitGroup
		wg.Go(func() {
			svc.HandleEvent(ha.Event{EventType: ha.EventMobileAppNotificationAction, Data: data, Context: ha.EventContext{UserID: approverID}})
		})
		wg.Go(func() { h.gw.Close(5 * time.Second) })
		wg.Wait()
		// The next start ends whatever is still open.
		if _, err := h.journal.Recover(context.Background(), h.log); err != nil {
			t.Fatal(err)
		}
		e := h.lastEntry()
		executed := path(e, "result", "status") == "executed" && path(e, "approval", "outcome") == "approved"
		interrupted := path(e, "approval", "outcome") == "cancelled" && path(e, "approval", "cause") == "interrupted" && len(h.ha.recorded()) == 0
		if !executed && !interrupted {
			t.Fatalf("run %d: audit entry = %v, %d calls", i, e, len(h.ha.recorded()))
		}
	}
}

// Approver fatigue: withdrawing and asking again starts the same wait as a refusal, so
// an agent cannot notify the approvers again and again; an end by the emergency stop or a
// revocation does not.
func TestAWithdrawalStartsTheWait(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	if out, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": ref}); out["status"] != "withdrawn" {
		t.Fatalf("cancel = %v, %q", out, errText)
	}
	if errText := unlock(h, nil); errText != "denied: approval_cooldown" || len(f.requests()) != 1 {
		t.Errorf("asked again = %q, %d asked", errText, len(f.requests()))
	}

	h, f = pendingHarness(t)
	pendingUnlock(t, h, f)
	f.end(f.heldIDs()[0], approval.Result{Outcome: approval.OutcomeCancelled, Cause: audit.CauseEmergencyStop, At: time.Now()})
	waitFor(t, func() bool {
		return h.lastEntry()["event"] == "decision" && path(h.lastEntry(), "approval", "cause") == "emergency_stop"
	})
	pendingUnlock(t, h, f) // no wait after an emergency stop
}

// Results kept in memory are forgotten after resultKeep also without a new request; the
// journal answers then.
func TestOldResultsAreForgottenOnLookup(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	f.end(f.heldIDs()[0], approvedNow(h))
	waitFor(t, func() bool {
		out, _ := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref})
		return out["status"] == "executed"
	})
	h.now.advance(resultKeep + time.Minute)
	if out, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); out["status"] != "executed" {
		t.Errorf("from the journal = %v, %q", out, errText)
	}
	h.gw.mu.Lock()
	n := len(h.gw.waits)
	h.gw.mu.Unlock()
	if n != 0 {
		t.Errorf("%d results kept", n)
	}
}
