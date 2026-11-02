// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/intent"
)

func (h *harness) intentCount() int {
	h.t.Helper()
	n, err := h.intents.Count(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// A direct action enters the intent store before Home Assistant is called and leaves it
// with the entry that records the result, executed or failed (SPEC-v0 section 9).
func TestADirectActionLeavesNoIntentBehind(t *testing.T) {
	h := newHarness(t, nil)
	h.ha.set(func(f *fakeHA) {
		f.onCall = func() {
			if h.intentCount() != 1 {
				t.Error("Home Assistant called without an intent in the store")
			}
		}
	})
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "" {
		t.Fatalf("perform_action: %q", errText)
	}
	if h.intentCount() != 0 || path(h.lastEntry(), "result", "status") != "executed" {
		t.Errorf("intents = %d, entry = %v", h.intentCount(), h.lastEntry())
	}
	h.ha.set(func(f *fakeHA) { f.err, f.onCall = ha.ErrDisconnected, nil })
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "failed" {
		t.Fatalf("perform_action with Home Assistant down: %q", errText)
	}
	if h.intentCount() != 0 || path(h.lastEntry(), "result", "error") != "ha_unavailable" {
		t.Errorf("intents = %d, entry = %v", h.intentCount(), h.lastEntry())
	}
}

// failingIntents cannot store anything.
type failingIntents struct{}

func (failingIntents) Begin(context.Context, audit.Entry) (string, error) {
	return "", errors.New("intent: store unavailable: disk full")
}
func (failingIntents) End(string) func(context.Context, *sql.Tx) error {
	return func(context.Context, *sql.Tx) error { return nil }
}
func (failingIntents) EndNow(context.Context, string) error { return nil }

// Without an intent nothing is dispatched: the request is denied by the audit log, with
// the evaluation that allowed it, and the agent learns that Home-Mandate is unavailable.
func TestNothingIsDispatchedWithoutAnIntent(t *testing.T) {
	h := newHarness(t, nil)
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	h.intentsOverride = failingIntents{}
	h.url = h.serve(h.log)
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "unavailable" {
		t.Fatalf("perform_action: %q, want unavailable", errText)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("Home Assistant was called without an intent")
	}
	e := h.lastEntry()
	if path(e, "result", "status") != "denied" || path(e, "result", "denied_by") != "audit" || path(e, "result", "error") != "log_unwritable" ||
		path(e, "evaluation", "decision") != "allow" {
		t.Errorf("audit entry = %v", e)
	}
	if !strings.Contains(logs.String(), "intent not stored, action not executed") {
		t.Errorf("log = %s", logs.String())
	}
}

// flakyAuditor is the real log that refuses every write while down is set.
type flakyAuditor struct {
	log  *audit.Log
	down atomic.Bool
}

func (f *flakyAuditor) Append(ctx context.Context, e audit.Entry) (int64, error) {
	if f.down.Load() {
		return 0, errors.New("audit: insert: disk full")
	}
	return f.log.Append(ctx, e)
}

func (f *flakyAuditor) WithEntry(ctx context.Context, e audit.Entry, action func() error) error {
	if f.down.Load() {
		return errors.New("audit: insert: disk full")
	}
	return f.log.WithEntry(ctx, e, action)
}

// A denial the log could not record is kept and written before the next entry once the
// log is back, with its intent ended by it (SPEC-v0 section 9).
func TestDenialsTheLogCouldNotRecordAreWrittenLater(t *testing.T) {
	h := newHarness(t, nil)
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	auditor := &flakyAuditor{log: h.log}
	h.url = h.serve(auditor)
	before := len(h.entries())
	auditor.down.Store(true)
	for range 2 {
		if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "unavailable" {
			t.Fatalf("perform_action with the log down: %q", errText)
		}
	}
	if len(h.ha.recorded()) != 0 {
		t.Fatal("Home Assistant was called while the log was down")
	}
	if n := len(h.entries()); n != before {
		t.Fatalf("%d entries written while the log was down", n-before)
	}
	if h.intentCount() != 2 {
		t.Errorf("intents = %d, want 2 until their denials are recorded", h.intentCount())
	}
	if !strings.Contains(logs.String(), "denial kept until it can be written") {
		t.Errorf("log = %s", logs.String())
	}

	auditor.down.Store(false)
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_off"}); errText != "" {
		t.Fatalf("perform_action with the log back: %q", errText)
	}
	entries := h.entries()[before:]
	if len(entries) != 3 {
		t.Fatalf("entries after the log came back = %d, want the two denials and the execution", len(entries))
	}
	for _, e := range entries[:2] {
		if path(e, "result", "denied_by") != "audit" || path(e, "result", "error") != "log_unwritable" || path(e, "request", "action") != "turn_on" {
			t.Errorf("late denial = %v", e)
		}
	}
	if path(entries[2], "result", "status") != "executed" || path(entries[2], "request", "action") != "turn_off" {
		t.Errorf("execution = %v", entries[2])
	}
	if h.intentCount() != 0 {
		t.Errorf("intents = %d after the denials were recorded", h.intentCount())
	}
	if !strings.Contains(logs.String(), "denial recorded late") {
		t.Errorf("log = %s", logs.String())
	}
}

// brokenEndIntents is the real store whose End within a transaction fails.
type brokenEndIntents struct{ *intent.Store }

func (b brokenEndIntents) End(string) func(context.Context, *sql.Tx) error {
	return func(context.Context, *sql.Tx) error { return fmt.Errorf("%w: locked", intent.ErrStore) }
}

// A kept denial whose intent cannot be ended in the entry's transaction is written
// without it, its intent ended on its own, and the denials behind it follow (SPEC-v0
// section 9: the log must not lose the entry).
func TestAKeptDenialNeverBlocksTheOnesBehindIt(t *testing.T) {
	h := newHarness(t, nil)
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	h.intentsOverride = brokenEndIntents{h.intents}
	auditor := &flakyAuditor{log: h.log}
	h.url = h.serve(auditor)
	before := len(h.entries())
	auditor.down.Store(true)
	for range 2 {
		if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}); errText != "unavailable" {
			t.Fatalf("perform_action with the log down: %q", errText)
		}
	}
	auditor.down.Store(false)
	// An execution whose intent cannot be ended in the entry's transaction fails closed, as
	// a confirmed one with a broken journal always has: not executed, denied by the log,
	// the denial written without the hook and the intent ended on its own.
	if _, errText := h.call(h.session(), "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_off"}); errText != "unavailable" {
		t.Fatalf("perform_action with a broken End: %q", errText)
	}
	if len(h.ha.recorded()) != 0 {
		t.Error("Home Assistant was called although the entry could not be committed")
	}
	entries := h.entries()[before:]
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want the two kept denials and the new one", len(entries))
	}
	for i, e := range entries {
		if path(e, "result", "denied_by") != "audit" {
			t.Errorf("entry %d = %v", i, e)
		}
	}
	if path(entries[2], "request", "action") != "turn_off" {
		t.Errorf("order: last entry = %v", entries[2])
	}
	if h.intentCount() != 0 {
		t.Errorf("intents = %d, want every one ended on its own", h.intentCount())
	}
	if !strings.Contains(logs.String(), "denial recorded late") {
		t.Errorf("log = %s", logs.String())
	}
}

// The oldest kept denial gives way when more than maxDeferred wait for the log.
func TestKeptDenialsAreBounded(t *testing.T) {
	// More requests than the example mandate permits per hour: the limit would deny them
	// before any intent.
	h := newHarness(t, func(doc map[string]any) { doc["limits"] = map[string]any{"max_actions_per_hour": 1000} })
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	auditor := &flakyAuditor{log: h.log}
	h.url = h.serve(auditor)
	auditor.down.Store(true)
	s := h.session()
	for range maxDeferred + 1 {
		h.call(s, "perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"})
	}
	h.gw.deferMu.Lock()
	kept := len(h.gw.deferred)
	h.gw.deferMu.Unlock()
	if kept != maxDeferred {
		t.Errorf("kept = %d, want %d", kept, maxDeferred)
	}
	if !strings.Contains(logs.String(), "the oldest denial kept for it is lost") {
		t.Errorf("log = %s", logs.String())
	}
}
