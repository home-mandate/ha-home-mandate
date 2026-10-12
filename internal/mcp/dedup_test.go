// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// Tests of repeated requests (issue #27 part C, SPEC-v0 section 11.1 items 2, 6 and 10).

func (h *harness) setState(entityID, state string) {
	h.catalog.mu.Lock()
	defer h.catalog.mu.Unlock()
	dev := h.catalog.devices[entityID]
	dev.State = state
	h.catalog.devices[entityID] = dev
}

func countEntries(h *harness, errCode string) int {
	n := 0
	for line := range strings.Lines(h.auditLog()) {
		if strings.Contains(line, `"error":"`+errCode+`"`) {
			n++
		}
	}
	return n
}

// Layer 1: the identical call while a request is open asks nobody; it gets the same
// approval ID, and after the confirmation both calls get the same result from one
// execution.
func TestARepeatedCallAttachesToTheOpenRequest(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	again, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock", "reason": "again"})
	if errText != "" || again["status"] != "pending" || again["approval_id"] != ref || len(f.requests()) != 1 {
		t.Fatalf("repeat = %v, %q, %d asked", again, errText, len(f.requests()))
	}
	// Both callers wait for the same outcome.
	h.gw.mu.Lock()
	h.gw.cfg.ApprovalWait = 5 * time.Second // no call is running
	h.gw.mu.Unlock()
	results := make(chan map[string]any, 2)
	for range 2 {
		go func() {
			out, _ := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref})
			results <- out
		}()
	}
	time.Sleep(50 * time.Millisecond)
	f.end(f.heldIDs()[0], approvedNow(h))
	for range 2 {
		if out := <-results; out["status"] != "executed" {
			t.Errorf("result = %v", out)
		}
	}
	if calls := h.ha.recorded(); len(calls) != 1 {
		t.Errorf("executed %d times", len(calls))
	}
	if n := countEntries(h, errDuplicate); n != 1 {
		t.Errorf("%d duplicate entries", n)
	}
}

// The issue's case: a call, the client gives up and calls again after 60 s (the first
// request still open), then the human confirms: one notification, one execution, and
// both calls (waiting) get the same result.
func TestCallRepeatedAfterTheCutOffExecutesOnce(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := newHarness(t, nil)
	f.journal, h.approver, h.approvalWait = h.journal, f, 5*time.Second
	h.url = h.serve(h.log)
	results := make(chan map[string]any, 2)
	call := func() {
		out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
		results <- out
	}
	go call()
	waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
	h.now.advance(time.Minute)
	go call()
	time.Sleep(100 * time.Millisecond)
	f.end(f.heldIDs()[0], approvedNow(h))
	for range 2 {
		if out := <-results; out["status"] != "executed" {
			t.Errorf("result = %v", out)
		}
	}
	if len(f.requests()) != 1 || len(h.ha.recorded()) != 1 {
		t.Errorf("%d asked, %d executed", len(f.requests()), len(h.ha.recorded()))
	}
}

// Concurrent identical calls make one request.
func TestConcurrentIdenticalCallsMakeOneRequest(t *testing.T) {
	h, f := pendingHarness(t)
	var wg sync.WaitGroup
	refs := make(chan any, 5)
	for range 5 {
		wg.Go(func() {
			out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
			refs <- out["approval_id"]
		})
	}
	wg.Wait()
	close(refs)
	var first any
	for r := range refs {
		if first == nil {
			first = r
		}
		if r == nil || r != first {
			t.Errorf("approval IDs differ: %v, %v", first, r)
		}
	}
	if n := len(f.requests()); n != 1 {
		t.Errorf("%d requests", n)
	}
}

// Another action or other parameters on the device while a request is open: denied,
// nobody notified, with the open request's ID.
func TestAnotherCallWhileARequestIsOpen(t *testing.T) {
	h, f := pendingHarness(t)
	ref, _ := pendingUnlock(t, h, f)
	_, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "open"})
	if !strings.HasPrefix(errText, "denied: approval_pending") || !strings.Contains(errText, ref) || len(f.requests()) != 1 {
		t.Errorf("other action = %q, %d asked", errText, len(f.requests()))
	}
	if e := h.lastEntry(); path(e, "result", "error") != "approval_pending" || path(e, "approval") != nil {
		t.Errorf("audit entry = %v", e)
	}
}

// Layer 2: confirmed just before the client's cut-off, repeated afterwards: already
// executed, not again, no new request; also after a restart (from the journal); after the
// window a new request.
func TestReplayWindow(t *testing.T) {
	h, f := pendingHarness(t)
	h.ha.onCall = func() { h.setState("lock.front_door", "unlocked") }
	ref, _ := pendingUnlock(t, h, f)
	f.end(f.heldIDs()[0], approvedNow(h))
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	waitFor(t, func() bool {
		out, _ := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref})
		return out["status"] == "executed"
	})
	check := func(when string) {
		t.Helper()
		out, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
		if errText != "" || out["status"] != "already_executed" || out["approval_id"] != ref || out["confirmed_at"] == nil {
			t.Errorf("%s: repeat = %v, %q", when, out, errText)
		}
		if len(f.requests()) != 1 || len(h.ha.recorded()) != 1 {
			t.Errorf("%s: %d asked, %d executed", when, len(f.requests()), len(h.ha.recorded()))
		}
	}
	check("at once")
	h.url = h.serve(h.log) // a restart: the window comes from the journal
	check("after a restart")
	if n := countEntries(h, errAlreadyExecuted); n != 2 {
		t.Errorf("%d already_executed entries", n)
	}
	// The request's timeout is one minute here (the fake), the window 6 minutes; locked
	// again meanwhile.
	h.now.advance(7 * time.Minute)
	h.setState("lock.front_door", "locked")
	if out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); out["status"] != "pending" || len(f.requests()) != 2 {
		t.Errorf("after the window = %v, %d asked", out, len(f.requests()))
	}
}

// script.run twice within the window: the script runs once.
func TestAScriptRunsOnceWithinTheWindow(t *testing.T) {
	f := &fakeApprover{hold: true}
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-ask-script", "resource": map[string]any{"category": "script"},
			"actions": []any{"run"}, "decision": "ask"}}, d["rules"].([]any)...)
	})
	h.catalog.devices["script.garage_pulse"] = catalog.Device{EntityID: "script.garage_pulse", Category: "script", State: "off"}
	f.journal, h.approver, h.approvalWait = h.journal, f, 2*time.Second
	h.url = h.serve(h.log)
	go func() {
		waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
		f.end(f.heldIDs()[0], approvedNow(h))
	}()
	run := map[string]any{"entity_id": "script.garage_pulse", "action": "run"}
	if out, errText := h.call(h.session(), "perform_action", run); out["status"] != "executed" {
		t.Fatalf("first = %v, %q", out, errText)
	}
	if out, _ := h.call(h.session(), "perform_action", run); out["status"] != "already_executed" {
		t.Errorf("second = %v", out)
	}
	if len(h.ha.recorded()) != 1 {
		t.Errorf("ran %d times", len(h.ha.recorded()))
	}
}

// Layer 3, the issue's garage: a door on a pulse relay toggles on every open_cover. Two
// agents ask to open it, both are confirmed: it opens once, the second is not executed
// (already_in_state), and its agent learns why.
func TestTwoAgentsOpenTheGarageOnce(t *testing.T) {
	f := &fakeApprover{hold: true}
	rule := func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-ask-garage", "resource": map[string]any{"category": "cover"},
			"actions": []any{"open", "close"}, "decision": "ask"}}, d["rules"].([]any)...)
	}
	h := newHarness(t, rule)
	h.catalog.devices["cover.garage_door"] = catalog.Device{EntityID: "cover.garage_door", Category: "cover", State: "closed",
		Attributes: map[string]any{"friendly_name": "Garage door"}}
	f.journal, h.approver, h.approvalWait = h.journal, f, 50*time.Millisecond
	h.url = h.serve(h.log)
	other, err := h.agents.Register(context.Background(), "Second assistant", admin)
	if err != nil {
		t.Fatal(err)
	}
	doc := mandateDoc(t, other.ClientID, func(d map[string]any) { d["id"] = "m-second"; rule(d) })
	if _, err := h.mandates.Put(context.Background(), doc, admin); err != nil {
		t.Fatal(err)
	}
	open := map[string]any{"entity_id": "cover.garage_door", "action": "open"}
	a, _ := h.call(h.session(), "perform_action", open)
	b, _ := h.call(h.sessionWith(h.issue(other.ClientID)), "perform_action", open)
	if a["status"] != "pending" || b["status"] != "pending" || len(f.requests()) != 2 {
		t.Fatalf("a = %v, b = %v", a, b)
	}
	for _, req := range f.requests() {
		if req.State != "closed" || req.Device != "Garage door" {
			t.Errorf("request shows %q (%q)", req.Device, req.State)
		}
	}
	// The relay pulses: the door starts opening.
	h.ha.onCall = func() { h.setState("cover.garage_door", "opening") }
	f.end(fmt.Sprintf("%032x", 1), approvedNow(h))
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	f.end(fmt.Sprintf("%032x", 2), approvedNow(h))
	s := h.sessionWith(h.issue(other.ClientID))
	waitFor(t, func() bool {
		_, errText := h.call(s, "approval_status", map[string]any{"approval_id": b["approval_id"]})
		return errText == "failed: already_in_state"
	})
	if calls := h.ha.recorded(); len(calls) != 1 {
		t.Errorf("the door was pulsed %d times", len(calls))
	}
	if e := h.lastEntry(); path(e, "result", "status") != "failed" || path(e, "result", "error") != "already_in_state" ||
		path(e, "approval", "outcome") != "approved" {
		t.Errorf("audit entry = %v", e)
	}
	// Asked to close it while open; someone stops it half way by hand: state_changed.
	h.setState("cover.garage_door", "open")
	c, _ := h.call(h.sessionWith(h.issue(other.ClientID)), "perform_action", map[string]any{"entity_id": "cover.garage_door", "action": "close"})
	if c["status"] != "pending" {
		t.Fatalf("close = %v", c)
	}
	h.setState("cover.garage_door", "stopped")
	f.end(fmt.Sprintf("%032x", 3), approvedNow(h))
	waitFor(t, func() bool {
		_, errText := h.call(s, "approval_status", map[string]any{"approval_id": c["approval_id"]})
		return errText == "failed: state_changed"
	})
}

// The idempotency key ties an agent's calls to one request, regardless of the window;
// the same key for another call is refused.
func TestIdempotencyKey(t *testing.T) {
	h, f := pendingHarness(t)
	call := func(entity, action, key string) (map[string]any, string) {
		return h.call(h.session(), "perform_action", map[string]any{"entity_id": entity, "action": action, "idempotency_key": key})
	}
	first, _ := call("lock.front_door", "unlock", "order-4711")
	if first["status"] != "pending" {
		t.Fatalf("first = %v", first)
	}
	if again, _ := call("lock.front_door", "unlock", "order-4711"); again["approval_id"] != first["approval_id"] || len(f.requests()) != 1 {
		t.Errorf("same key = %v", again)
	}
	if _, errText := call("lock.back_door", "unlock", "order-4711"); !strings.HasPrefix(errText, "invalid_params: idempotency_conflict") {
		t.Errorf("key for another call = %q", errText)
	}
	for _, bad := range []string{"short", strings.Repeat("k", 65), "with space!"} {
		if _, errText := call("lock.front_door", "unlock", bad); errText != "invalid_params" {
			t.Errorf("key %q = %q", bad, errText)
		}
	}
	f.end(f.heldIDs()[0], approvedNow(h))
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	// Beyond the replay window and after a restart, still the outcome of the first call.
	h.now.advance(time.Hour)
	h.url = h.serve(h.log)
	if out, errText := call("lock.front_door", "unlock", "order-4711"); out["status"] != "already_executed" || out["approval_id"] != first["approval_id"] {
		t.Errorf("after an hour = %v, %q", out, errText)
	}
	if len(f.requests()) != 1 || len(h.ha.recorded()) != 1 {
		t.Errorf("%d asked, %d executed", len(f.requests()), len(h.ha.recorded()))
	}
	if _, errText := call("lock.back_door", "unlock", "order-4711"); !strings.HasPrefix(errText, "invalid_params: idempotency_conflict") {
		t.Errorf("key for another call from the journal = %q", errText)
	}
	// Without the key the same call after the window is a new request.
	if out, _ := call("lock.front_door", "unlock", ""); out["status"] != "pending" || len(f.requests()) != 2 {
		t.Errorf("without the key = %v", out)
	}
}

// No human is asked for what is done already: the target state reached when the request
// would be made → failed already_in_state, nobody notified.
func TestNobodyIsAskedForWhatIsDoneAlready(t *testing.T) {
	h, f := pendingHarness(t)
	h.setState("lock.front_door", "unlocked")
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); errText != "failed: already_in_state" {
		t.Errorf("unlock an unlocked door = %q", errText)
	}
	if len(f.requests()) != 0 {
		t.Error("a human was asked")
	}
	if e := h.lastEntry(); path(e, "result", "error") != "already_in_state" || path(e, "approval") != nil {
		t.Errorf("audit entry = %v", e)
	}
}

// The replay window holds only while the earlier execution's effect does: the device
// back in another state (closed by hand), or another call of the agent executed since,
// makes the same call a new request.
func TestReplayEndsWhenTheEffectIsGone(t *testing.T) {
	h, f := pendingHarness(t)
	execute := func(id string) {
		t.Helper()
		f.end(id, approvedNow(h))
		waitFor(t, func() bool { state, _ := h.journalRow(id); return state == "ended" })
	}
	pendingUnlock(t, h, f)
	h.ha.onCall = func() { h.setState("lock.front_door", "unlocked") }
	execute(fmt.Sprintf("%032x", 1))
	h.setState("lock.front_door", "locked") // locked by hand
	if out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"}); out["status"] != "pending" {
		t.Errorf("after locking by hand = %v", out)
	}
	if len(f.requests()) != 2 {
		t.Errorf("%d asked", len(f.requests()))
	}
}

// One idempotency key for two devices at once: one request, the other call is a conflict
// (never journal_unavailable).
func TestOneKeyForTwoDevicesAtOnce(t *testing.T) {
	h, f := pendingHarness(t)
	var wg sync.WaitGroup
	answers := make(chan string, 2)
	for _, id := range []string{"lock.front_door", "lock.back_door"} {
		wg.Go(func() {
			out, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": id, "action": "unlock", "idempotency_key": "order-4711"})
			answers <- fmt.Sprint(out["status"]) + errText
		})
	}
	wg.Wait()
	close(answers)
	var pending, conflict int
	for a := range answers {
		switch {
		case a == "pending":
			pending++
		case strings.Contains(a, "idempotency_conflict"):
			conflict++
		default:
			t.Errorf("answer %q", a)
		}
	}
	if pending != 1 || conflict != 1 || len(f.requests()) != 1 {
		t.Errorf("pending %d, conflict %d, asked %d", pending, conflict, len(f.requests()))
	}
}

// A call that attaches while the request is still being started waits for it: it gets the
// request's expiry, and can withdraw it.
func TestAttachingWhileTheRequestStarts(t *testing.T) {
	h, f := pendingHarness(t)
	release := make(chan struct{})
	f.during = func() { <-release }
	first := make(chan map[string]any, 1)
	go func() {
		out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
		first <- out
	}()
	waitFor(t, func() bool { return len(f.requests()) == 1 })
	second := make(chan map[string]any, 1)
	go func() {
		out, _ := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
		second <- out
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	a, b := <-first, <-second
	if a["approval_id"] != b["approval_id"] || b["open_until"] == nil || strings.HasPrefix(fmt.Sprint(b["open_until"]), "0001") {
		t.Errorf("first %v, second %v", a, b)
	}
	if out, errText := h.call(h.session(), "approval_cancel", map[string]any{"approval_id": b["approval_id"]}); out["status"] != "withdrawn" {
		t.Errorf("cancel = %v, %q", out, errText)
	}
}

// noReadHarness: the agent may unlock with a confirmation but not read the lock.
func noReadHarness(t *testing.T) (*harness, *fakeApprover) {
	t.Helper()
	f := &fakeApprover{hold: true}
	h := newHarness(t, func(d map[string]any) {
		d["rules"] = append([]any{map[string]any{"id": "r-read-lock", "resource": map[string]any{"entity_id": "lock.front_door"},
			"actions": []any{"read"}, "decision": "ask"}}, d["rules"].([]any)...)
	})
	f.journal, h.approver, h.approvalWait = h.journal, f, 50*time.Millisecond
	h.url = h.serve(h.log)
	return h, f
}

// An agent that may not read the device learns nothing about its state: no shortcut
// (the human is asked and sees the state), and a refusal by the state check is a generic
// not_executed, also from the journal; the audit log keeps the precise code.
func TestNoStateOracleWithoutRead(t *testing.T) {
	h, f := noReadHarness(t)
	h.setState("lock.front_door", "unlocked")
	out, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "lock.front_door", "action": "unlock"})
	if errText != "" || out["status"] != "pending" || len(f.requests()) != 1 {
		t.Fatalf("unlock of an unlocked door without read = %v, %q", out, errText)
	}
	f.end(f.heldIDs()[0], approvedNow(h))
	ref := out["approval_id"]
	waitFor(t, func() bool {
		_, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref})
		return errText == "failed: not_executed"
	})
	if e := h.lastEntry(); path(e, "result", "error") != "already_in_state" {
		t.Errorf("audit entry = %v", e)
	}
	h.now.advance(resultKeep + time.Minute) // from the journal now
	if _, errText := h.call(h.session(), "approval_status", map[string]any{"approval_id": ref}); errText != "failed: not_executed" {
		t.Errorf("from the journal = %q", errText)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("executed")
	}
}

// An idempotency key whose request has ended gives the earlier outcome, marked as earlier:
// already_executed with its time for an execution, the refusal with "earlier result" for
// the rest; never a plain executed, nothing new asked.
func TestAnEndedKeyGivesTheEarlierOutcome(t *testing.T) {
	h, f := pendingHarness(t)
	h.ha.onCall = func() { h.setState("lock.front_door", "unlocked") }
	call := func(entity, key string) (map[string]any, string) {
		return h.call(h.session(), "perform_action", map[string]any{"entity_id": entity, "action": "unlock", "idempotency_key": key})
	}
	first, _ := call("lock.front_door", "order-0001")
	f.end(fmt.Sprintf("%032x", 1), approvedNow(h))
	waitFor(t, func() bool { return len(h.ha.recorded()) == 1 })
	for _, when := range []string{"kept", "from the journal"} {
		if when == "from the journal" {
			h.now.advance(time.Hour)
		}
		out, errText := call("lock.front_door", "order-0001")
		if errText != "" || out["status"] != "already_executed" || out["approval_id"] != first["approval_id"] || out["confirmed_at"] == nil {
			t.Errorf("%s: executed key = %v, %q", when, out, errText)
		}
	}
	pendingUnlock(t, h, f, "lock.back_door")
	call("lock.garden_gate", "order-0002")
	f.end(fmt.Sprintf("%032x", 3), approval.Result{Outcome: approval.OutcomeRejected, By: approverID, At: h.now.Now()})
	waitFor(t, func() bool { state, _ := h.journalRow(fmt.Sprintf("%032x", 3)); return state == "ended" })
	_, errText := call("lock.garden_gate", "order-0002")
	if !strings.HasPrefix(errText, "denied: approval_rejected") || !strings.Contains(errText, "earlier result of") || !strings.Contains(errText, "nothing new was asked") {
		t.Errorf("rejected key = %q", errText)
	}
	if len(f.requests()) != 3 {
		t.Errorf("%d asked", len(f.requests()))
	}
}

// A call whose fingerprint cannot be made is refused, never deduplicated on "".
func TestNoRequestWithoutAFingerprint(t *testing.T) {
	h, f := pendingHarness(t)
	snap, err := h.pdp.Snapshot(context.Background(), h.agent.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	d := snap.Decide("lock.front_door", "unlock", nil)
	call := ha.ServiceCall{Domain: "lock", Service: "unlock", EntityID: "lock.front_door", Data: map[string]any{"x": func() {}}}
	if _, _, err := h.gw.askHuman(context.Background(), h.agent, h.token, d, call, "", ""); err == nil || err.Error() != "failed" {
		t.Errorf("askHuman = %v", err)
	}
	if len(f.requests()) != 0 {
		t.Error("asked without a fingerprint")
	}
	if e := h.lastEntry(); path(e, "result", "error") != errFingerprint {
		t.Errorf("audit entry = %v", e)
	}
}

// While Home Assistant is disconnected there is no shortcut: the state is not trusted.
func TestNoShortcutWhileDisconnected(t *testing.T) {
	h, _ := pendingHarness(t)
	h.setState("lock.front_door", "unlocked")
	if h.gw.knownState("lock.front_door") == nil {
		t.Fatal("state unknown while connected")
	}
	h.ha.set(func(f *fakeHA) { f.connected = false })
	if h.gw.knownState("lock.front_door") != nil {
		t.Error("state trusted while disconnected")
	}
}
