// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// Tests of the approval journal in the gateway (issue #27, SPEC-v0 section 11.1 item 9).

var requestID = strings.Repeat("d", 32)

func (h *harness) journalResult(id string) map[string]any {
	h.t.Helper()
	var result string
	if err := h.db.QueryRow(`SELECT coalesce(result, '') FROM approval_journal WHERE id = ?`, id).Scan(&result); err != nil {
		h.t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	return out
}

// A confirmed request is executing in the journal, committed, while Home Assistant is
// called, so that a crash then is recognised as an unknown outcome; it ends with the
// executed entry.
func TestConfirmedRequestIsExecutingDuringTheCall(t *testing.T) {
	f := &fakeApprover{result: approval.Result{ID: requestID, Outcome: approval.OutcomeApproved, By: approverID, Via: approval.ViaPush, At: answeredAt}}
	h := approvalHarness(t, f)
	var during, by string
	h.ha.onCall = func() {
		_ = h.db.QueryRow(`SELECT state, answered_by FROM approval_journal WHERE id = ?`, requestID).Scan(&during, &by)
	}
	if errText := unlock(h, nil); errText != "" {
		t.Fatalf("perform_action = %q", errText)
	}
	if during != "executing" || by != approverID {
		t.Errorf("journal during the call: %s by %s", during, by)
	}
	if state, outcome := h.journalRow(requestID); state != "ended" || outcome != "approved" {
		t.Errorf("journal after: %s %s", state, outcome)
	}
	if r := h.journalResult(requestID); r["status"] != "executed" {
		t.Errorf("result = %v", r)
	}
}

// Every end of a request ends it in the journal together with its entry.
func TestEveryEndEndsTheJournalEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		result approval.Result
		setup  func(h *harness)
		status any
	}{
		"rejected":       {approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}, nil, "denied"},
		"timeout":        {approval.Result{Outcome: approval.OutcomeTimeout, At: answeredAt}, nil, "denied"},
		"invalid answer": {approval.Result{Outcome: approval.OutcomeInvalidResponse, By: "user-2", At: answeredAt}, nil, "denied"},
		"call failed": {approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt},
			func(h *harness) { h.ha.set(func(f *fakeHA) { f.err = ha.ErrDisconnected }) }, "failed"},
		"mandate changed": {approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt},
			func(h *harness) { h.approver.(*fakeApprover).during = func() { h.revokeMandate() } }, "denied"},
	} {
		tc.result.ID = requestID
		f := &fakeApprover{result: tc.result}
		h := approvalHarness(t, f)
		if tc.setup != nil {
			tc.setup(h)
		}
		_ = unlock(h, nil)
		if state, outcome := h.journalRow(requestID); state != "ended" || outcome != tc.result.Outcome {
			t.Errorf("%s: journal %s %s", name, state, outcome)
		}
		if r := h.journalResult(requestID); r["status"] != tc.status || path(h.lastEntry(), "result", "status") != tc.status {
			t.Errorf("%s: journal result %v, entry %v", name, r, h.lastEntry())
		}
	}
}

// Fail closed: a request the journal cannot take is not asked, a confirmed one it cannot
// mark executing is not executed.
func TestNothingHappensWithoutTheJournal(t *testing.T) {
	h := approvalHarness(t, &fakeApprover{err: approval.ErrJournal})
	if errText := unlock(h, nil); errText != "unavailable" {
		t.Errorf("journal refused the request: %q", errText)
	}
	if e := h.lastEntry(); path(e, "result", "status") != "failed" || path(e, "result", "error") != "journal_unavailable" || path(e, "approval") != nil {
		t.Errorf("audit entry = %v", e)
	}

	f := &fakeApprover{result: approval.Result{ID: requestID, Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h = approvalHarness(t, f)
	f.journal = nil // the request is not in the journal: it cannot be marked executing
	if errText := unlock(h, nil); errText != "unavailable" {
		t.Errorf("confirmed without journal: %q", errText)
	}
	if calls := h.ha.recorded(); len(calls) != 0 {
		t.Error("Home Assistant called")
	}
	if e := h.lastEntry(); path(e, "result", "error") != "journal_unavailable" || path(e, "approval", "outcome") != "approved" {
		t.Errorf("audit entry = %v", e)
	}
}

// A request that reached nobody is no request (SPEC-v0 section 9.1: no approval) and is
// not closed in the UI, but it ends in the journal.
func TestUndeliveredRequestEndsInTheJournal(t *testing.T) {
	f := &fakeApprover{result: approval.Result{ID: requestID}, err: approval.ErrNoApprover}
	h := approvalHarness(t, f)
	var ids []string
	h.log.OnCommit(func(_ int64, e audit.Entry) { ids = append(ids, e.ApprovalID) })
	if errText := unlock(h, nil); errText != "denied: no_approver" {
		t.Errorf("perform_action = %q", errText)
	}
	if e := h.lastEntry(); path(e, "approval") != nil || path(e, "result", "denied_by") != "approval" {
		t.Errorf("audit entry = %v", e)
	}
	if len(ids) != 1 || ids[0] != "" {
		t.Errorf("approval IDs = %q", ids)
	}
	if state, _ := h.journalRow(requestID); state != "ended" {
		t.Errorf("journal = %s", state)
	}
}

func (h *harness) revokeMandate() {
	if _, err := h.db.Exec(`UPDATE mandates SET status = 'revoked'`); err != nil {
		h.t.Error(err)
	}
}

// The digest is that of the effective call: the same call however the numbers or keys
// were written, another for other data.
func TestCallDigest(t *testing.T) {
	var a, b map[string]any
	if err := errors.Join(json.Unmarshal([]byte(`{"brightness_pct": 50, "transition": 2}`), &a),
		json.Unmarshal([]byte(`{"transition": 2.0, "brightness_pct": 50.0}`), &b)); err != nil {
		t.Fatal(err)
	}
	call := func(data map[string]any) string {
		return callDigest(ha.ServiceCall{Domain: "light", Service: "turn_on", EntityID: "light.kitchen", Data: data})
	}
	if call(a) != call(b) || !strings.HasPrefix(call(a), "sha256:") || len(call(a)) != 71 {
		t.Errorf("digests %s, %s", call(a), call(b))
	}
	if call(a) == call(map[string]any{"brightness_pct": 51}) || call(nil) == call(a) {
		t.Error("different calls share a digest")
	}
	if callDigest(ha.ServiceCall{Data: map[string]any{"x": func() {}}}) != "" {
		t.Error("unencodable call has a digest")
	}
}

// brokenEnd is the journal whose step inside the audit transaction fails; ending a
// request on its own still works.
type brokenEnd struct{ *approval.Journal }

func (b brokenEnd) End(string, *audit.Approval, audit.Result) func(context.Context, *sql.Tx) error {
	return func(context.Context, *sql.Tx) error { return fmt.Errorf("%w: disk I/O error", approval.ErrJournal) }
}

// The journal never costs the audit entry of an end: when ending the request inside the
// entry's transaction fails, the entry is written without it and the request is ended on
// its own. An execution stays fail closed: no entry, no call.
func TestAJournalFailureNeverLosesTheEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		result  approval.Result
		during  func(h *harness)
		status  any
		errText string
	}{
		"rejected":  {approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}, nil, "denied", "denied: approval_rejected"},
		"timeout":   {approval.Result{Outcome: approval.OutcomeTimeout, At: answeredAt}, nil, "denied", "denied: approval_timeout"},
		"cancelled": {approval.Result{Outcome: approval.OutcomeCancelled, Cause: audit.CauseEmergencyStop, At: answeredAt}, nil, "denied", "denied: emergency_stop"},
		"denied after the answer": {approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt},
			func(h *harness) { h.revokeMandate() }, "denied", "denied: mandate_changed"},
	} {
		tc.result.ID = requestID
		f := &fakeApprover{result: tc.result}
		h := newHarness(t, nil)
		f.journal, h.approver, h.journalOverride = h.journal, f, brokenEnd{h.journal}
		var logs safeBuffer
		h.logger = slog.New(slog.NewTextHandler(&logs, nil))
		h.url = h.serve(h.log)
		if tc.during != nil {
			f.during = func() { tc.during(h) }
		}
		if errText := unlock(h, nil); errText != tc.errText {
			t.Errorf("%s: %q", name, errText)
		}
		if e := h.lastEntry(); path(e, "evaluation", "decision") != "ask" || path(e, "result", "status") != tc.status ||
			path(e, "approval", "outcome") != tc.result.Outcome {
			t.Errorf("%s: audit entry = %v", name, e)
		}
		if state, _ := h.journalRow(requestID); state != "ended" {
			t.Errorf("%s: journal %s", name, state)
		}
		if !strings.Contains(logs.String(), "approval journal not ended with the audit entry") {
			t.Errorf("%s: log = %s", name, logs.String())
		}
	}

	f := &fakeApprover{result: approval.Result{ID: requestID, Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := newHarness(t, nil)
	f.journal, h.approver, h.journalOverride = h.journal, f, brokenEnd{h.journal}
	h.url = h.serve(h.log)
	if errText := unlock(h, nil); errText != "unavailable" {
		t.Errorf("execution: %q", errText)
	}
	if calls := h.ha.recorded(); len(calls) != 0 {
		t.Error("Home Assistant called without the journal")
	}
	if e := h.lastEntry(); path(e, "result", "status") != "failed" || path(e, "result", "error") != "journal_unavailable" {
		t.Errorf("execution: audit entry = %v", e)
	}
	if state, _ := h.journalRow(requestID); state != "ended" {
		t.Errorf("execution: journal %s", state)
	}
}

// safeBuffer is a log destination for concurrent writers.
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// deadlineJournal records the deadline Executing was given.
type deadlineJournal struct {
	*approval.Journal
	deadline time.Time
	ok       bool
}

func (d *deadlineJournal) Executing(ctx context.Context, id string, a audit.Approval) error {
	d.deadline, d.ok = ctx.Deadline()
	return d.Journal.Executing(ctx, id, a)
}

// Marking a request executing is bounded, so that a locked database fails closed
// instead of holding the confirmed action.
func TestExecutingIsBounded(t *testing.T) {
	f := &fakeApprover{result: approval.Result{ID: requestID, Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := newHarness(t, nil)
	dj := &deadlineJournal{Journal: h.journal}
	f.journal, h.approver, h.journalOverride = h.journal, f, dj
	h.url = h.serve(h.log)
	start := time.Now()
	if errText := unlock(h, nil); errText != "" {
		t.Fatalf("perform_action = %q", errText)
	}
	if !dj.ok || dj.deadline.Sub(start) > journalTimeout+time.Second || journalTimeout > 12*time.Second {
		t.Errorf("deadline %v (ok %v)", dj.deadline.Sub(start), dj.ok)
	}
}
