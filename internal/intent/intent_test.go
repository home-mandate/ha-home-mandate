// SPDX-License-Identifier: AGPL-3.0-or-later

package intent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

const household = "household:hm-0123456789ab"

type env struct {
	t     *testing.T
	db    *sql.DB
	log   *audit.Log
	store *Store
	logs  bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{t: t, db: st.DB(), log: audit.New(st.DB(), household)}
	e.store = New(st.DB(), slog.New(slog.NewTextHandler(&e.logs, nil)))
	return e
}

func entry() audit.Entry {
	rule := "r-lights"
	return audit.Entry{Event: audit.EventDecision,
		Agent:      &audit.Agent{ClientID: "hm-client:voice", DisplayName: "Voice assistant"},
		Request:    &audit.Request{Time: time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC), Timezone: "Europe/Berlin", Resource: audit.Resource{EntityID: "light.kitchen", Category: "light", Areas: []string{"kitchen"}}, Action: "turn_on"},
		Mandate:    &audit.Mandate{ID: "m-voice", Digest: "sha256:" + strings.Repeat("a", 64)},
		Evaluation: &audit.Evaluation{Decision: "allow", Reason: "rule", RuleID: &rule},
		Result:     &audit.Result{Status: audit.StatusExecuted}}
}

func (e *env) count() int {
	e.t.Helper()
	n, err := e.store.Count(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) entries() []map[string]any {
	e.t.Helper()
	var buf bytes.Buffer
	if err := e.log.Export(context.Background(), &buf); err != nil {
		e.t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// An intent is stored before the call and ends in the transaction of the entry that
// records the result: nothing of it remains, and the entry is written.
func TestIntentEndsWithTheEntry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, err := e.store.Begin(ctx, entry())
	if err != nil || len(id) != 32 {
		t.Fatalf("Begin = %q, %v", id, err)
	}
	if e.count() != 1 {
		t.Fatalf("intents = %d, want 1", e.count())
	}
	en := entry()
	en.Also = e.store.End(id)
	if _, err := e.log.Append(ctx, en); err != nil {
		t.Fatal(err)
	}
	if e.count() != 0 || len(e.entries()) != 1 {
		t.Errorf("intents = %d, entries = %d", e.count(), len(e.entries()))
	}
	// Ending an intent that is gone changes nothing and fails nothing.
	if err := e.store.EndNow(ctx, id); err != nil {
		t.Error(err)
	}
}

// Recover records every intent a previous process left as failed/outcome_unknown with the
// stored decision, deletes it, and does nothing the second time.
func TestRecoverRecordsEveryIntentOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for range 2 {
		if _, err := e.store.Begin(ctx, entry()); err != nil {
			t.Fatal(err)
		}
	}
	n, err := e.store.Recover(ctx, e.log)
	if err != nil || n != 2 {
		t.Fatalf("Recover = %d, %v", n, err)
	}
	entries := e.entries()
	if len(entries) != 2 || e.count() != 0 {
		t.Fatalf("entries = %d, intents = %d", len(entries), e.count())
	}
	for _, en := range entries {
		result := en["result"].(map[string]any)
		eval := en["evaluation"].(map[string]any)
		req := en["request"].(map[string]any)
		if result["status"] != "failed" || result["error"] != ErrorOutcomeUnknown || eval["decision"] != "allow" ||
			req["resource"].(map[string]any)["entity_id"] != "light.kitchen" || en["mandate"] == nil {
			t.Errorf("entry = %v", en)
		}
	}
	if n, err := e.store.Recover(ctx, e.log); err != nil || n != 0 {
		t.Errorf("second Recover = %d, %v", n, err)
	}
	if len(e.entries()) != 2 {
		t.Error("the second Recover wrote entries")
	}
}

// A row whose stored decision is unusable is recorded with a minimal entry from what the
// row still tells; one that tells nothing usable is deleted and logged.
func TestRecoverWritesAMinimalEntryForACorruptRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, err := e.store.Begin(ctx, entry())
	if err != nil {
		t.Fatal(err)
	}
	// The evaluation of the stored decision is broken, agent and request are readable.
	if _, err := e.db.ExecContext(ctx, `UPDATE execution_intents SET decision = json_set(decision, '$.evaluation', json('{"decision": 7}')) WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Begin(ctx, entry()); err != nil {
		t.Fatal(err)
	}
	// Nothing of the second row can be read.
	if _, err := e.db.ExecContext(ctx, `UPDATE execution_intents SET decision = '{}' WHERE id <> ?`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := e.store.Recover(ctx, e.log); err != nil || n != 2 {
		t.Fatalf("Recover = %d, %v", n, err)
	}
	entries := e.entries()
	if len(entries) != 1 || e.count() != 0 {
		t.Fatalf("entries = %d, intents = %d", len(entries), e.count())
	}
	en := entries[0]
	if en["evaluation"] != nil || en["request"].(map[string]any)["resource"].(map[string]any)["entity_id"] != "light.kitchen" ||
		en["result"].(map[string]any)["error"] != ErrorOutcomeUnknown {
		t.Errorf("minimal entry = %v", en)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "recorded with a minimal entry") || !strings.Contains(logs, "cannot be recorded in the audit log") {
		t.Errorf("log = %s", logs)
	}
}

// Without the table nothing is stored or ended, and every error names the store.
func TestEverythingFailsWithoutTheStore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.store.SetClock(func() time.Time { return time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC) })
	id, err := e.store.Begin(ctx, entry())
	if err != nil {
		t.Fatal(err)
	}
	var created string
	if err := e.db.QueryRowContext(ctx, `SELECT created_at FROM execution_intents WHERE id = ?`, id).Scan(&created); err != nil || created != "2026-10-01T09:30:00.000Z" {
		t.Errorf("created_at = %q, %v", created, err)
	}
	if _, err := e.db.ExecContext(ctx, `DROP TABLE execution_intents`); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"Begin":  second(e.store.Begin(ctx, entry())),
		"EndNow": e.store.EndNow(ctx, id),
		"Count":  second(e.store.Count(ctx)),
		"Record": e.store.record(ctx, e.log, id, entry()),
	} {
		if err == nil || !errors.Is(err, ErrStore) {
			t.Errorf("%s = %v, want ErrStore", name, err)
		}
	}
	if _, err := e.store.Recover(ctx, e.log); !errors.Is(err, ErrStore) {
		t.Errorf("Recover = %v", err)
	}
	// End within a transaction fails the same way, and the entry with it.
	en := entry()
	en.Also = e.store.End(id)
	if _, err := e.log.Append(ctx, en); !errors.Is(err, ErrStore) {
		t.Errorf("Append with End = %v", err)
	}
	// A store on a closed database cannot even begin a transaction.
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	closed := New(st.DB(), nil)
	_ = st.Close()
	if err := closed.record(ctx, e.log, id, entry()); !errors.Is(err, ErrStore) {
		t.Errorf("record on a closed database = %v", err)
	}
}

func second[T any](_ T, err error) error { return err }

// A row whose time cannot be read gets the time of the recovery.
func TestMinimalEntryFallsBackToNow(t *testing.T) {
	now := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	e := minimalEntry(row{id: strings.Repeat("a", 32), createdAt: "yesterday", decision: "not json"}, now)
	if !e.Request.Time.Equal(now) || e.Agent != nil || e.Request.Resource.EntityID != "" || e.Result.Error != ErrorOutcomeUnknown {
		t.Errorf("minimal entry = %+v", e)
	}
}
