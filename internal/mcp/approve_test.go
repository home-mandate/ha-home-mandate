// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/pdp"
)

// fakeApprover answers with result, after running during (e.g. to change the world
// while the human decides).
type fakeApprover struct {
	mu     sync.Mutex
	result approval.Result
	err    error
	during func()
	asked  []approval.Request
}

func (f *fakeApprover) Ask(_ context.Context, req approval.Request) (approval.Result, error) {
	f.mu.Lock()
	f.asked = append(f.asked, req)
	during, res, err := f.during, f.result, f.err
	f.mu.Unlock()
	if during != nil {
		during()
	}
	return res, err
}

func (f *fakeApprover) requests() []approval.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]approval.Request(nil), f.asked...)
}

const approverID = "user-1" // approver of the voice assistant example

var answeredAt = time.Date(2026, 10, 13, 12, 0, 30, 0, time.UTC)

func approvalHarness(t *testing.T, f *fakeApprover) *harness {
	t.Helper()
	h := newHarness(t, nil)
	h.approver = f
	h.url = h.serve(h.log)
	return h
}

func unlock(h *harness, args map[string]any) string {
	h.t.Helper()
	call := map[string]any{"entity_id": "lock.front_door", "action": "unlock", "reason": "The parcel service is at the door"}
	for k, v := range args {
		call[k] = v
	}
	_, errText := h.call(h.session(), "perform_action", call)
	return errText
}

// E2E scenario 2 at unit level: ask → an approver confirms → executed.
func TestApprovedActionIsExecuted(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := approvalHarness(t, f)
	if errText := unlock(h, nil); errText != "" {
		t.Fatalf("perform_action = %q", errText)
	}
	calls := h.ha.recorded()
	if len(calls) != 1 || calls[0].Domain != "lock" || calls[0].Service != "unlock" {
		t.Errorf("calls = %+v", calls)
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Agent != "Voice assistant" || reqs[0].Device != "lock.front_door" || reqs[0].Action != "unlock" ||
		reqs[0].Reason != "The parcel service is at the door" || len(reqs[0].Approvers) != 1 || reqs[0].Approvers[0] != approverID ||
		reqs[0].Timeout != 2*time.Minute {
		t.Errorf("asked = %+v", reqs)
	}
	e := h.lastEntry()
	if path(e, "evaluation", "decision") != "ask" || path(e, "approval", "outcome") != "approved" ||
		path(e, "approval", "by") != approverID || path(e, "result", "status") != "executed" {
		t.Errorf("audit entry = %v", e)
	}
	if r, err := h.log.Verify(context.Background()); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

// E2E scenarios 3 and 4 at unit level: no execution unless approved.
func TestRefusedApprovals(t *testing.T) {
	for _, tc := range []struct {
		result  approval.Result
		err     error
		errText string
		outcome any
	}{
		{approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}, nil, "denied: approval_rejected", "rejected"},
		{approval.Result{Outcome: approval.OutcomeTimeout, At: answeredAt}, nil, "denied: approval_timeout", "timeout"},
		{approval.Result{Outcome: approval.OutcomeInvalidResponse, By: "user-2", At: answeredAt}, nil, "denied: approval_invalid", "invalid_response"},
		{approval.Result{}, approval.ErrNoApprover, "denied: no_approver", nil},
		{approval.Result{}, errors.New("database is locked"), "denied: no_approver", nil},
	} {
		h := approvalHarness(t, &fakeApprover{result: tc.result, err: tc.err})
		if errText := unlock(h, nil); errText != tc.errText {
			t.Errorf("%v %v: %q", tc.result.Outcome, tc.err, errText)
		}
		if calls := h.ha.recorded(); len(calls) != 0 {
			t.Errorf("%v: Home Assistant called", tc.result.Outcome)
		}
		e := h.lastEntry()
		if path(e, "result", "denied_by") != "approval" || path(e, "approval", "outcome") != tc.outcome {
			t.Errorf("%v: audit entry = %v", tc.result.Outcome, e)
		}
		if tc.result.Outcome == approval.OutcomeTimeout && path(e, "approval", "by") != nil {
			t.Errorf("timeout with a responder: %v", e)
		}
	}
}

// Invalid parameters are refused before a human is bothered.
func TestParametersAreCheckedBeforeAsking(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := approvalHarness(t, f)
	if errText := unlock(h, map[string]any{"params": map[string]any{"code": "1234"}}); !strings.HasPrefix(errText, "invalid_params") {
		t.Errorf("extra parameter: %q", errText)
	}
	if errText := unlock(h, map[string]any{"reason": strings.Repeat("r", maxReasonRunes+1)}); errText != "invalid_params" {
		t.Errorf("long reason: %q", errText)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d humans asked", n)
	}
}

// What changes while the human decides is checked again before executing.
func TestChecksAfterApproval(t *testing.T) {
	approved := approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}
	for name, tc := range map[string]struct {
		during   func(h *harness)
		errText  string
		deniedBy any
		status   any
	}{
		"emergency stop": {func(h *harness) {
			_, _ = h.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('emergency_stop', 'on', '2026-10-13T12:00:00Z')`)
		}, "denied: emergency_stop", "emergency_stop", "denied"},
		"mandate revoked": {func(h *harness) {
			_ = h.mandates.Revoke(context.Background(), "m-voice-assistant", admin)
		}, "denied: mandate_changed", "mandate", "denied"},
		"mandate changed": {func(h *harness) {
			_, _ = h.mandates.Put(context.Background(), mandateDoc(h.t, h.agent.ClientID, func(d map[string]any) {
				d["limits"] = map[string]any{"max_actions_per_hour": 61}
			}), admin)
		}, "denied: mandate_changed", "mandate", "denied"},
		"Home Assistant gone": {func(h *harness) { h.ha.set(func(f *fakeHA) { f.connected = false }) },
			"unavailable", nil, "failed"},
	} {
		f := &fakeApprover{result: approved}
		h := approvalHarness(t, f)
		f.during = func() { tc.during(h) }
		if errText := unlock(h, nil); errText != tc.errText {
			t.Errorf("%s: %q", name, errText)
		}
		if calls := h.ha.recorded(); len(calls) != 0 {
			t.Errorf("%s: Home Assistant called", name)
		}
		e := h.lastEntry()
		if path(e, "approval", "outcome") != "approved" || path(e, "result", "denied_by") != tc.deniedBy ||
			path(e, "result", "status") != tc.status {
			t.Errorf("%s: audit entry = %v", name, e)
		}
	}
}

func TestFailedCallAfterApprovalIsLogged(t *testing.T) {
	h := approvalHarness(t, &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}})
	h.ha.set(func(f *fakeHA) { f.err = ha.ErrDisconnected })
	if errText := unlock(h, nil); errText != "failed" {
		t.Errorf("perform_action = %q", errText)
	}
	if e := h.lastEntry(); path(e, "result", "error") != "ha_unavailable" || path(e, "approval", "outcome") != "approved" {
		t.Errorf("audit entry = %v", e)
	}
}

// Without an approval service, ask stays a refusal; reads are never asked for.
func TestAskWithoutApprovals(t *testing.T) {
	h := newHarness(t, nil)
	if errText := unlock(h, nil); !strings.HasPrefix(errText, "approval_required") {
		t.Errorf("unlock = %q", errText)
	}
	h2 := newHarness(t, func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-ask-read", "resource": map[string]any{"entity_id": "light.kitchen"},
			"actions": []any{"read"}, "decision": "ask"}}, d["rules"].([]any)...)
	})
	h2.approver = &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h2.url = h2.serve(h2.log)
	_, errText := h2.call(h2.session(), "get_state", map[string]any{"entity_id": "light.kitchen"})
	if !strings.HasPrefix(errText, "approval_required") {
		t.Errorf("get_state with read ask = %q", errText)
	}
}

func TestApprovalTimeout(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"PT2M": 2 * time.Minute, "PT30S": 30 * time.Second, "PT1M30S": 90 * time.Second, "PT10S": 10 * time.Second,
		"": 0, "P1D": 0, "PTxM": 0, "PT5X": 0, "PT1MxS": 0, "PT1M3": 0,
	} {
		if got := approvalTimeout(in); got != want {
			t.Errorf("approvalTimeout(%q) = %v, want %v", in, got, want)
		}
	}
}

// An agent may have at most maxPendingAsks approval requests waiting.
func TestPendingAsksAreBounded(t *testing.T) {
	release := make(chan struct{})
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}}
	f.during = func() { <-release }
	h := approvalHarness(t, f)
	results := make(chan string, maxPendingAsks)
	for range maxPendingAsks {
		go func() { results <- unlock(h, nil) }()
	}
	waitFor(t, func() bool { return len(f.requests()) == maxPendingAsks })
	if errText := unlock(h, nil); errText != "denied: approval_pending" {
		t.Errorf("third request = %q", errText)
	}
	close(release)
	for range maxPendingAsks {
		if r := <-results; r != "denied: approval_rejected" {
			t.Errorf("waiting request = %q", r)
		}
	}
	// The slots are free again.
	f.during = nil
	if errText := unlock(h, nil); errText != "denied: approval_rejected" {
		t.Errorf("after the others = %q", errText)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A token revoked while the human decides (e.g. after refresh token reuse) stops the
// action.
func TestRevokedTokenAfterApproval(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := approvalHarness(t, f)
	f.during = func() { _, _ = h.db.Exec(`UPDATE tokens SET revoked_at = '2026-10-13T12:00:00Z'`) }
	if errText := unlock(h, nil); errText != "denied: unauthorized" {
		t.Errorf("perform_action = %q", errText)
	}
	if calls := h.ha.recorded(); len(calls) != 0 {
		t.Error("Home Assistant called")
	}
	if e := h.lastEntry(); path(e, "result", "denied_by") != "authentication" || path(e, "approval", "outcome") != "approved" {
		t.Errorf("audit entry = %v", e)
	}
}

// failingDecider loads the mandate once, then fails.
type failingDecider struct {
	Decider
	calls int
}

func (f *failingDecider) Snapshot(ctx context.Context, clientID string) (*pdp.Snapshot, error) {
	f.calls++
	snap, err := f.Decider.Snapshot(ctx, clientID)
	if f.calls > 1 {
		return snap, errors.New("database is locked")
	}
	return snap, err
}

func TestMandateUnavailableAfterApproval(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: answeredAt}}
	h := newHarness(t, nil)
	h.approver, h.decider = f, &failingDecider{Decider: h.pdp}
	h.url = h.serve(h.log)
	if errText := unlock(h, nil); errText != "unavailable" {
		t.Errorf("perform_action = %q", errText)
	}
	if e := h.lastEntry(); path(e, "result", "error") != "mandate_unavailable" || len(h.ha.recorded()) != 0 {
		t.Errorf("audit entry = %v", e)
	}
}

// The human sees the service data that will be executed.
func TestApprovalShowsTheServiceData(t *testing.T) {
	f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: answeredAt}}
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-ask-light", "resource": map[string]any{"entity_id": "light.kitchen"},
			"actions": []any{"set"}, "decision": "ask"}}, d["rules"].([]any)...)
	})
	h.approver = f
	h.url = h.serve(h.log)
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "set",
		"params": map[string]any{"brightness_pct": 40}})
	if errText != "denied: approval_rejected" {
		t.Fatalf("perform_action = %q", errText)
	}
	if reqs := f.requests(); len(reqs) != 1 || reqs[0].Params["brightness_pct"] != 40 {
		t.Errorf("asked = %+v", reqs)
	}
}

// Decision F2: the request says whether the action is critical (the UI channel depends
// on it) and which agent asks; the audit entry records the channel of the answer.
func TestRequestIsMarkedAndChannelRecorded(t *testing.T) {
	for _, via := range []string{approval.ViaPush, approval.ViaUI} {
		f := &fakeApprover{result: approval.Result{Outcome: approval.OutcomeApproved, By: approverID, Via: via, At: answeredAt}}
		h := approvalHarness(t, f)
		if errText := unlock(h, nil); errText != "" {
			t.Fatalf("%s: perform_action = %q", via, errText)
		}
		reqs := f.requests()
		if len(reqs) != 1 || !reqs[0].Critical || reqs[0].ClientID != h.agent.ClientID {
			t.Errorf("%s: asked = %+v", via, reqs)
		}
		if e := h.lastEntry(); path(e, "approval", "via") != via || path(e, "approval", "by") != approverID {
			t.Errorf("%s: audit entry = %v", via, e)
		}
		if r, err := h.log.Verify(context.Background()); err != nil || !r.Valid {
			t.Errorf("%s: audit log = %+v, %v", via, r, err)
		}
	}
}

// Decision F1: a request ended by the emergency stop or a revocation is denied without
// an approval in the audit entry, with the cause as denied_by.
func TestCancelledApprovals(t *testing.T) {
	cancelled := approval.Result{Outcome: approval.OutcomeCancelled, At: answeredAt}
	for name, tc := range map[string]struct {
		during   func(h *harness)
		errText  string
		deniedBy string
	}{
		"emergency stop": {func(h *harness) {
			_, _ = h.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('emergency_stop', 'on', '2026-10-13T12:00:00Z')`)
		}, "denied: emergency_stop", "emergency_stop"},
		"agent revoked": {func(*harness) {}, "denied: unauthorized", "authentication"},
	} {
		f := &fakeApprover{result: cancelled}
		h := approvalHarness(t, f)
		f.during = func() { tc.during(h) }
		if errText := unlock(h, nil); errText != tc.errText {
			t.Errorf("%s: %q", name, errText)
		}
		if calls := h.ha.recorded(); len(calls) != 0 {
			t.Errorf("%s: Home Assistant called", name)
		}
		e := h.lastEntry()
		if path(e, "approval") != nil || path(e, "result", "status") != "denied" || path(e, "result", "denied_by") != tc.deniedBy {
			t.Errorf("%s: audit entry = %v", name, e)
		}
		if r, err := h.log.Verify(context.Background()); err != nil || !r.Valid {
			t.Errorf("%s: audit log = %+v, %v", name, r, err)
		}
	}
}
