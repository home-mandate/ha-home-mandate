// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
)

// askReadMandate lets the agent read everything, but locks only after a human confirms.
func askReadMandate(d map[string]any) {
	d["rules"] = []any{
		map[string]any{"id": "r-read", "resource": map[string]any{"any": true}, "actions": []any{"read"}, "decision": "allow"},
		map[string]any{"id": "r-lock-read", "resource": map[string]any{"category": "lock"}, "actions": []any{"read"}, "decision": "ask"},
	}
}

func askReadHarness(t *testing.T, f *fakeApprover, wait time.Duration) *harness {
	t.Helper()
	h := newHarness(t, askReadMandate)
	f.journal, h.approver, h.approvalWait = h.journal, f, wait
	h.url = h.serve(h.log)
	return h
}

func readLock(h *harness) (map[string]any, string) {
	h.t.Helper()
	return h.call(h.session(), "get_state", map[string]any{"entity_id": "lock.front_door", "reason": "Is the door locked?"})
}

// Issue #46: reading with ask asks a human like an action; after the confirmation the
// state is returned, and the read is in the audit log with the approval.
func TestAskReadApprovedReturnsTheState(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := askReadHarness(t, f, 0)
	out, errText := readLock(h)
	if errText != "" || out["entity_id"] != "lock.front_door" || out["state"] != "locked" || out["status"] != nil {
		t.Fatalf("get_state = %v, %q", out, errText)
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Action != "read" || reqs[0].EntityID != "lock.front_door" || reqs[0].Reason != "Is the door locked?" || reqs[0].Critical {
		t.Errorf("asked = %+v", reqs)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("Home Assistant called for a read")
	}
	e := h.lastEntry()
	if path(e, "request", "action") != "read" || path(e, "evaluation", "decision") != "ask" ||
		path(e, "approval", "outcome") != "approved" || path(e, "result", "status") != "executed" {
		t.Errorf("audit entry = %v", e)
	}
}

// One confirmation covers one read: the next read asks again; there is no replay.
func TestAskReadCoversOneRead(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := askReadHarness(t, f, 0)
	for i := range 2 {
		if out, errText := readLock(h); errText != "" || out["state"] != "locked" {
			t.Fatalf("read %d = %v, %q", i+1, out, errText)
		}
	}
	if n := len(f.requests()); n != 2 {
		t.Errorf("%d requests for two reads", n)
	}
}

func TestAskReadRefused(t *testing.T) {
	for _, tc := range []struct {
		result  approval.Result
		err     error
		errText string
	}{
		{approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}, nil, "denied: approval_rejected"},
		{approval.Result{Outcome: approval.OutcomeTimeout, At: answeredAt}, nil, "denied: approval_timeout"},
		{approval.Result{}, approval.ErrNoApprover, "denied: no_approver"},
	} {
		h := askReadHarness(t, &fakeApprover{result: tc.result, err: tc.err}, 0)
		if out, errText := readLock(h); errText != tc.errText || out != nil {
			t.Errorf("%v: %v, %q", tc.result.Outcome, out, errText)
		}
		if e := h.lastEntry(); path(e, "result", "denied_by") != "approval" || path(e, "request", "action") != "read" {
			t.Errorf("%v: audit entry = %v", tc.result.Outcome, e)
		}
	}
}

// Without an answer within the wait get_state is pending like perform_action; a second
// read meanwhile attaches to the same request; approval_status gives the state read when
// the human confirmed.
func TestAskReadPendingThenTheState(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := askReadHarness(t, f, 100*time.Millisecond)
	res, err := h.session().CallTool(context.Background(), &sdk.CallToolParams{Name: "get_state",
		Arguments: map[string]any{"entity_id": "lock.front_door"}})
	if err != nil || res.IsError {
		t.Fatalf("get_state = %+v, %v", res, err)
	}
	out, _ := res.StructuredContent.(map[string]any)
	ref, _ := out["approval_id"].(string)
	if out["status"] != "pending" || !approvalIDPattern.MatchString(ref) || out["next"] != "approval_status" || out["state"] != "" {
		t.Fatalf("pending get_state = %v", out)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "NOT READ YET") || !strings.Contains(text, ref) {
		t.Errorf("text = %s", text)
	}
	if again, _ := readLock(h); again["approval_id"] != ref {
		t.Errorf("second read = %v, want attached to %s", again, ref)
	}
	if n := len(f.requests()); n != 1 {
		t.Errorf("%d requests", n)
	}
	h.setState("lock.front_door", "unlocked") // read when confirmed, not when asked
	f.end(f.heldIDs()[0], approvedNow(h))
	status, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref})
	read, _ := status["read"].(map[string]any)
	if errText != "" || status["status"] != "executed" || read["entity_id"] != "lock.front_door" || read["state"] != "unlocked" {
		t.Errorf("approval_status = %v, %q", status, errText)
	}
}

// The state read is kept in memory only: later, the journal says the read was confirmed
// but its result is gone, and nothing about the device is stored.
func TestAskReadResultIsNotKept(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := askReadHarness(t, f, 100*time.Millisecond)
	out, _ := readLock(h)
	ref, _ := out["approval_id"].(string)
	f.end(f.heldIDs()[0], approvedNow(h))
	waitFor(t, func() bool { return path(h.lastEntry(), "result", "status") == "executed" })
	h.now.advance(resultKeep + time.Minute)
	if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); !strings.HasPrefix(errText, "failed: result_gone") {
		t.Errorf("approval_status later = %q", errText)
	}
	if strings.Contains(h.journalDump(), "locked") {
		t.Error("the journal holds the device's state")
	}
}

// journalDump returns every value in the approval journal as text.
func (h *harness) journalDump() string {
	h.t.Helper()
	rows, err := h.db.Query(`SELECT * FROM approval_journal`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var b strings.Builder
	for rows.Next() {
		values := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			h.t.Fatal(err)
		}
		for _, v := range values {
			b.Write(v)
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// The results of a read that needs a confirmation conform to the advertised schemas:
// get_state pending, and approval_status with the state read.
func TestOutputSchemasOfAskedReads(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := askReadHarness(t, f, 100*time.Millisecond)
	s := h.session()
	open, strict := schemas(t, s)
	check := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %+v, %v", tool, res, err)
		}
		data, _ := json.Marshal(res.StructuredContent)
		var v any
		_ = json.Unmarshal(data, &v)
		for name, r := range map[string]*jsonschema.Resolved{"advertised": open[tool], "strict": strict[tool]} {
			if err := r.Validate(v); err != nil {
				t.Errorf("%s %s: %s does not match: %v", tool, name, data, err)
			}
		}
		out, _ := v.(map[string]any)
		return out
	}
	pending := check("get_state", map[string]any{"entity_id": "lock.front_door"})
	f.end(f.heldIDs()[0], approvedNow(h))
	if out := check("approval_status", map[string]any{"approval_id": pending["approval_id"]}); out["read"] == nil {
		t.Errorf("approval_status = %v", out)
	}
}

// A read and an action on one device share the open request and the wait after a
// refusal (approver fatigue is per device); a withdrawn read starts the wait too.
func TestAskReadSharesTheDeviceWithActions(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := newHarness(t, func(d map[string]any) {
		askReadMandate(d)
		d["rules"] = append(d["rules"].([]any), map[string]any{"id": "r-unlock", "resource": map[string]any{"category": "lock"},
			"actions": []any{"unlock"}, "decision": "ask"})
	})
	f.journal, h.approver, h.approvalWait = h.journal, f, 100*time.Millisecond
	h.url = h.serve(h.log)
	out, _ := readLock(h)
	ref, _ := out["approval_id"].(string)
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); !strings.Contains(errText, "approval_pending") {
		t.Errorf("unlock while a read is open = %q", errText)
	}
	if cancelled, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": ref}); errText != "" || cancelled["status"] != "withdrawn" {
		t.Fatalf("approval_cancel = %v, %q", cancelled, errText)
	}
	if _, errText := readLock(h); !strings.Contains(errText, "approval_cooldown") {
		t.Errorf("read after the withdrawal = %q", errText)
	}
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); !strings.Contains(errText, "approval_cooldown") {
		t.Errorf("unlock after the withdrawn read = %q", errText)
	}
}

// A second read that attached to the open request gets the state too.
func TestAttachedReadGetsTheState(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := askReadHarness(t, f, 2*time.Second)
	first := make(chan map[string]any, 1)
	go func() {
		out, _ := readLock(h)
		first <- out
	}()
	waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
	second := make(chan map[string]any, 1)
	go func() {
		out, _ := readLock(h)
		second <- out
	}()
	time.Sleep(100 * time.Millisecond)
	f.end(f.heldIDs()[0], approvedNow(h))
	for _, ch := range []chan map[string]any{first, second} {
		if out := <-ch; out["state"] != "locked" {
			t.Errorf("read = %v", out)
		}
	}
	if n := len(f.requests()); n != 1 {
		t.Errorf("%d requests", n)
	}
}

// The state read after a confirmation is not handed out once reading is denied.
func TestAskReadStateNotHandedOutAfterTheMandateChanged(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := askReadHarness(t, f, 100*time.Millisecond)
	out, _ := readLock(h)
	ref, _ := out["approval_id"].(string)
	f.end(f.heldIDs()[0], approvedNow(h))
	waitFor(t, func() bool { return path(h.lastEntry(), "result", "status") == "executed" })
	if _, err := h.mandates.Put(context.Background(), mandateDoc(t, h.agent.ClientID, func(d map[string]any) {
		d["rules"] = []any{map[string]any{"id": "r-no-locks", "resource": map[string]any{"category": "lock"}, "actions": []any{"read"}, "decision": "deny"}}
	}), admin); err != nil {
		t.Fatal(err)
	}
	if status, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "denied: mandate_changed" {
		t.Errorf("approval_status = %v, %q", status, errText)
	}
}
