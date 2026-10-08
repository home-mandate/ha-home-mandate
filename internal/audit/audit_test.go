// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	specaudit "github.com/home-mandate/spec/audit"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

const principal = "household:hm-0123456789ab"

func newLog(t *testing.T) (*audit.Log, *sql.DB) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return audit.New(s.DB(), principal), s.DB()
}

func str(s string) *string { return &s }

// samples covers every event this implementation writes, so that each shape is
// checked against the schema of the specification.
func samples() []audit.Entry {
	agent := &audit.Agent{ClientID: "hm-client:voice-7c21e9a4", DisplayName: "Voice assistant"}
	user := &audit.Actor{Kind: audit.ActorUser, ID: "user-1"}
	mandate := &audit.Mandate{ID: "m-voice", Digest: "sha256:" + string(bytes.Repeat([]byte("a"), 64))}
	req := &audit.Request{
		Time:     time.Date(2026, 10, 6, 19, 0, 0, 0, time.UTC),
		Timezone: "Europe/Berlin",
		Resource: audit.Resource{EntityID: "light.kitchen", Category: "light", Area: "kitchen"},
		Action:   "turn_on",
	}
	return []audit.Entry{
		{Event: audit.EventAgentRegistered, Actor: user, Agent: agent},
		{Event: audit.EventMandateCreated, Actor: user, Mandate: mandate},
		{Event: audit.EventDecision, Agent: agent, Request: req, Mandate: mandate,
			Evaluation: &audit.Evaluation{Decision: "allow", Reason: "rule", RuleID: str("r-lights")},
			Result:     &audit.Result{Status: audit.StatusExecuted, DurationMs: 12}},
		{Event: audit.EventDecision, Agent: agent, Request: req, Mandate: mandate,
			Evaluation: &audit.Evaluation{Decision: "deny", Reason: "no_match"},
			Result:     &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}},
		{Event: audit.EventDecision, Agent: agent, Request: req, Mandate: mandate,
			Evaluation: &audit.Evaluation{Decision: "ask", Reason: "rule", RuleID: str("r-locks"), ApprovalTimeout: "PT2M"},
			Result:     &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}},
		{Event: audit.EventDecision, Agent: agent, Request: req,
			Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByRateLimit}},
		{Event: audit.EventDecision, Agent: agent, Request: req, Mandate: mandate,
			Evaluation: &audit.Evaluation{Decision: "allow", Reason: "rule", RuleID: str("r-lights")},
			Result:     &audit.Result{Status: audit.StatusFailed, Error: "ha_unavailable"}},
		{Event: audit.EventMandateUpdated, Actor: user, Mandate: &audit.Mandate{ID: "m-voice", Digest: mandate.Digest, PreviousDigest: mandate.Digest}},
		{Event: audit.EventMandateRevoked, Actor: user, Mandate: mandate},
		{Event: audit.EventAgentRevoked, Actor: user, Agent: agent},
		{Event: audit.EventAuthRejected, Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication}},
	}
}

func appendAll(t *testing.T, l *audit.Log, entries []audit.Entry) {
	t.Helper()
	for _, e := range entries {
		if _, err := l.Append(context.Background(), e); err != nil {
			t.Fatalf("Append(%s): %v", e.Event, err)
		}
	}
}

func verify(t *testing.T, l *audit.Log) specaudit.Result {
	t.Helper()
	r, err := l.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEveryEventIsSchemaValidAndChained(t *testing.T) {
	l, _ := newLog(t)
	appendAll(t, l, samples())
	if r := verify(t, l); !r.Valid {
		t.Fatalf("Verify = %+v", r)
	}
}

func TestEntriesHaveTheSpecShape(t *testing.T) {
	l, _ := newLog(t)
	appendAll(t, l, samples()[:2])
	var buf bytes.Buffer
	if err := l.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var first, second map[string]any
	_ = json.Unmarshal(lines[0], &first)
	_ = json.Unmarshal(lines[1], &second)
	uuid7 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	id, _ := first["id"].(string)
	if first["seq"] != 1.0 || first["prev"] != nil || first["principal"] != principal || !uuid7.MatchString(id) {
		t.Errorf("first = %v", first)
	}
	wantPrev, _ := specaudit.Digest(lines[0])
	if second["seq"] != 2.0 || second["prev"] != wantPrev {
		t.Errorf("second = %v, want prev %s", second, wantPrev)
	}
}

func TestExportIsVerifiableWithSpec(t *testing.T) {
	l, _ := newLog(t)
	appendAll(t, l, samples())
	var buf bytes.Buffer
	if err := l.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	r, err := specaudit.VerifyJSONLines(&buf)
	if err != nil || !r.Valid {
		t.Errorf("VerifyJSONLines = %+v, %v", r, err)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		wantAt int64
	}{
		{"modified entry", `UPDATE audit_log SET entry = replace(entry, 'turn_on', 'unlock') WHERE seq = 3`, 4},
		{"deleted entry", `DELETE FROM audit_log WHERE seq = 5`, 6},
		{"deleted beginning", `DELETE FROM audit_log WHERE seq <= 2`, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, db := newLog(t)
			appendAll(t, l, samples())
			if _, err := db.Exec(tt.sql); err != nil {
				t.Fatal(err)
			}
			if r := verify(t, l); r.Valid || r.BrokenAt != tt.wantAt {
				t.Errorf("Verify = %+v, want broken at %d", r, tt.wantAt)
			}
		})
	}
}

func TestAppendTxRollsBackWithTheCaller(t *testing.T) {
	l, db := newLog(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendTx(ctx, tx, samples()[0]); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	appendAll(t, l, samples()[:1])
	var n, maxSeq int64
	if err := db.QueryRow(`SELECT count(*), max(seq) FROM audit_log`).Scan(&n, &maxSeq); err != nil {
		t.Fatal(err)
	}
	if n != 1 || maxSeq != 1 {
		t.Errorf("entries = %d, max seq = %d; want 1, 1", n, maxSeq)
	}
}

func TestAppendRejectsInvalidEntries(t *testing.T) {
	l, db := newLog(t)
	bad := []audit.Entry{
		{Event: "unknown.event"},
		{Event: audit.EventDecision}, // decision needs agent, request, result
		{Event: audit.EventAgentRegistered, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u"},
			Agent: &audit.Agent{ClientID: "hm-client:x", DisplayName: string(bytes.Repeat([]byte("x"), 81))}},
	}
	for _, e := range bad {
		if _, err := l.Append(context.Background(), e); err == nil {
			t.Errorf("Append(%+v) succeeded", e)
		}
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n)
	if n != 0 {
		t.Errorf("%d invalid entries were written", n)
	}
}

func TestConcurrentAppendsStayChained(t *testing.T) {
	l, _ := newLog(t)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := l.Append(context.Background(), samples()[0]); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify = %+v", r)
	}
}

func TestTruncateKeepsTheLogVerifiable(t *testing.T) {
	l, db := newLog(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now.Add(-40 * 24 * time.Hour) })
	appendAll(t, l, samples()[:3])
	l.SetClock(func() time.Time { return now })
	appendAll(t, l, samples()[3:5])

	removed, err := l.Truncate(ctx, now.Add(-30*24*time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"})
	if err != nil || removed != 3 {
		t.Fatalf("Truncate = %d, %v; want 3", removed, err)
	}
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify after truncation = %+v", r)
	}
	var minSeq, maxSeq int64
	_ = db.QueryRow(`SELECT min(seq), max(seq) FROM audit_log`).Scan(&minSeq, &maxSeq)
	if minSeq != 4 || maxSeq != 6 {
		t.Errorf("seq range = %d..%d, want 4..6", minSeq, maxSeq)
	}
	if removed, err := l.Truncate(ctx, now.Add(-30*24*time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil || removed != 0 {
		t.Errorf("second Truncate = %d, %v; want no-op", removed, err)
	}
}

func TestVerifyEmptyLog(t *testing.T) {
	l, _ := newLog(t)
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify(empty) = %+v", r)
	}
}

// SPEC-v0 section 11.4: a clock before the newest entry of the log is wrong.
func TestClockBehindTheNewestEntry(t *testing.T) {
	l, _ := newLog(t)
	ctx := context.Background()
	if behind, err := l.ClockBehind(ctx); err != nil || behind {
		t.Fatalf("empty log: %v, %v", behind, err)
	}
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return at })
	if _, err := l.Append(ctx, audit.Entry{Event: audit.EventEmergencyStopActivated, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		now    time.Time
		behind bool
	}{
		{at, false},
		{at.Add(-audit.ClockTolerance), false}, // a small correction is tolerated
		{at.Add(-audit.ClockTolerance - time.Second), true},
		{at.Add(-48 * time.Hour), true}, // a host without a clock battery after a power cut
		{at.Add(time.Hour), false},
	} {
		l.SetClock(func() time.Time { return tc.now })
		if behind, err := l.ClockBehind(ctx); err != nil || behind != tc.behind {
			t.Errorf("clock at %v: behind = %v, %v; want %v", tc.now, behind, err, tc.behind)
		}
	}
	// An entry written while the clock is behind does not hide that it is.
	l.SetClock(func() time.Time { return at.Add(-48 * time.Hour) })
	if _, err := l.Append(ctx, audit.Entry{Event: audit.EventEmergencyStopReleased, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if behind, err := l.ClockBehind(ctx); err != nil || !behind {
		t.Errorf("after an entry with the wrong time: behind = %v, %v", behind, err)
	}
	// The clock had run ahead and is right now: a human sets the entries so far aside.
	seq, latest, err := l.AcceptClock(ctx)
	if err != nil || seq != 2 || latest != "2026-10-13T12:00:00.000Z" {
		t.Fatalf("AcceptClock = %d, %q, %v", seq, latest, err)
	}
	if behind, err := l.ClockBehind(ctx); err != nil || behind {
		t.Errorf("after accepting: behind = %v, %v", behind, err)
	}
	// Later entries count again.
	l.SetClock(func() time.Time { return at })
	if _, err := l.Append(ctx, audit.Entry{Event: audit.EventEmergencyStopActivated, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	l.SetClock(func() time.Time { return at.Add(-time.Hour) })
	if behind, err := l.ClockBehind(ctx); err != nil || !behind {
		t.Errorf("entries after accepting: behind = %v, %v", behind, err)
	}
}

func TestClockChecksReportDatabaseErrors(t *testing.T) {
	l, db := newLog(t)
	ctx := context.Background()
	if _, err := l.Append(ctx, audit.Entry{Event: audit.EventEmergencyStopActivated, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	// A time that cannot be read is an error, never "the clock is fine".
	if _, err := db.Exec(`UPDATE audit_log SET recorded_at = 'yesterday'`); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ClockBehind(ctx); err == nil {
		t.Error("ClockBehind read an unreadable time")
	}
	_ = db.Close()
	if _, err := l.ClockBehind(ctx); err == nil {
		t.Error("ClockBehind without a database")
	}
	if _, _, err := l.AcceptClock(ctx); err == nil {
		t.Error("AcceptClock without a database")
	}
}

// SPEC-v0 sections 9.1 and 11.4: changes of the resource directory are recorded, by a
// user or the system, never by an agent; a rename names its former ID, a mark does not.
func TestDirectoryChanges(t *testing.T) {
	l, _ := newLog(t)
	ctx := context.Background()
	user := &audit.Actor{Kind: audit.ActorUser, ID: "user-1"}
	system := &audit.Actor{Kind: audit.ActorSystem, ID: "directory"}
	for _, e := range []audit.Entry{
		{Event: audit.EventDirectoryChanged, Actor: user, Directory: &audit.Directory{Change: audit.DirectoryCriticalMarked, EntityID: "lock.cellar"}},
		{Event: audit.EventDirectoryChanged, Actor: system, Directory: &audit.Directory{Change: audit.DirectoryRenamed, EntityID: "lock.cellar_door", PreviousEntityID: "lock.cellar"}},
		{Event: audit.EventDirectoryChanged, Actor: user, Directory: &audit.Directory{Change: audit.DirectoryRenameApplied, EntityID: "lock.cellar_door", PreviousEntityID: "lock.cellar"}},
		{Event: audit.EventDirectoryChanged, Actor: user, Directory: &audit.Directory{Change: audit.DirectoryRenameDismissed, EntityID: "lock.cellar_door", PreviousEntityID: "lock.cellar"}},
		{Event: audit.EventDirectoryChanged, Actor: system, Directory: &audit.Directory{Change: audit.DirectoryCriticalUnmarked, EntityID: "lock.cellar"}},
	} {
		if _, err := l.Append(ctx, e); err != nil {
			t.Fatalf("%s: %v", e.Directory.Change, err)
		}
	}
	for name, e := range map[string]audit.Entry{
		"by an agent":           {Event: audit.EventDirectoryChanged, Actor: &audit.Actor{Kind: audit.ActorAgent, ID: "hm-client:x"}, Directory: &audit.Directory{Change: audit.DirectoryCriticalUnmarked, EntityID: "lock.cellar"}},
		"without directory":     {Event: audit.EventDirectoryChanged, Actor: user},
		"rename without former": {Event: audit.EventDirectoryChanged, Actor: system, Directory: &audit.Directory{Change: audit.DirectoryRenamed, EntityID: "lock.b"}},
		"mark with former":      {Event: audit.EventDirectoryChanged, Actor: user, Directory: &audit.Directory{Change: audit.DirectoryCriticalMarked, EntityID: "lock.b", PreviousEntityID: "lock.a"}},
		"renamed to itself":     {Event: audit.EventDirectoryChanged, Actor: system, Directory: &audit.Directory{Change: audit.DirectoryRenamed, EntityID: "lock.a", PreviousEntityID: "lock.a"}},
		"ID with a space":       {Event: audit.EventDirectoryChanged, Actor: user, Directory: &audit.Directory{Change: audit.DirectoryCriticalMarked, EntityID: "lock. a"}},
		"on another event":      {Event: audit.EventEmergencyStopActivated, Actor: user, Directory: &audit.Directory{Change: audit.DirectoryCriticalMarked, EntityID: "lock.a"}},
	} {
		if _, err := l.Append(ctx, e); !errors.Is(err, audit.ErrInvalidEntry) {
			t.Errorf("%s: err = %v, want ErrInvalidEntry", name, err)
		}
	}
	if r, err := l.Verify(ctx); err != nil || !r.Valid || r.Entries != 5 {
		t.Errorf("log = %+v, %v", r, err)
	}
}
