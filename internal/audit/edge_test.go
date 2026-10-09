// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

func TestApprovedAskIsRecordedWithApproval(t *testing.T) {
	l, _ := newLog(t)
	e := samples()[4] // ask, denied by approval
	e.Result = &audit.Result{Status: audit.StatusExecuted, DurationMs: 900}
	e.Approval = &audit.Approval{Outcome: "approved", By: "user-1", At: time.Date(2026, 10, 6, 19, 1, 0, 0, time.UTC)}
	appendAll(t, l, []audit.Entry{e})

	timeout := e
	timeout.Result = &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
	timeout.Approval = &audit.Approval{Outcome: "timeout", At: time.Date(2026, 10, 6, 19, 2, 0, 0, time.UTC)}
	appendAll(t, l, []audit.Entry{timeout})

	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify = %+v", r)
	}
	// Executed after ask without approval violates the schema and is refused.
	bad := samples()[4]
	bad.Result = &audit.Result{Status: audit.StatusExecuted}
	if _, err := l.Append(context.Background(), bad); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Errorf("Append(executed ask without approval) = %v, want ErrInvalidEntry", err)
	}
}

// Decision F2: the channel of the answer is recorded (approval.via); F1: a request ended
// by the emergency stop or a revocation has no approval, only the denial.
func TestApprovalChannelAndCancellation(t *testing.T) {
	l, _ := newLog(t)
	at := time.Date(2026, 10, 6, 19, 1, 0, 0, time.UTC)
	ui := samples()[4]
	ui.Result = &audit.Result{Status: audit.StatusExecuted, DurationMs: 900}
	ui.Approval = &audit.Approval{Outcome: "approved", By: "user-1", Via: "ui", At: at}
	push := samples()[4]
	push.Result = &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
	push.Approval = &audit.Approval{Outcome: "rejected", By: "user-2", Via: "push", At: at}
	stopped := samples()[4]
	stopped.Approval = nil
	stopped.Result = &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}
	revoked := stopped
	revoked.Result = &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication}
	appendAll(t, l, []audit.Entry{ui, push, stopped, revoked})
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify = %+v", r)
	}
	var buf strings.Builder
	if err := l.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"via":"ui"`) || !strings.Contains(buf.String(), `"via":"push"`) {
		t.Errorf("via missing in export:\n%s", buf.String())
	}
	// via without a person who answered violates the schema.
	bad := samples()[4]
	bad.Approval = &audit.Approval{Outcome: "timeout", Via: "push", At: at}
	if _, err := l.Append(context.Background(), bad); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Errorf("Append(via on a timeout) = %v, want ErrInvalidEntry", err)
	}
}

func TestDatabaseErrorsAreReported(t *testing.T) {
	l, db := newLog(t)
	appendAll(t, l, samples()[:2])
	db.Close()
	ctx := context.Background()

	if _, err := l.Append(ctx, samples()[0]); err == nil {
		t.Error("Append on a closed database succeeded")
	}
	if _, err := l.Verify(ctx); err == nil {
		t.Error("Verify on a closed database succeeded")
	}
	if err := l.Export(ctx, iotest.TruncateWriter(nil, 0)); err == nil {
		t.Error("Export on a closed database succeeded")
	}
	if _, err := l.Truncate(ctx, time.Now(), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err == nil {
		t.Error("Truncate on a closed database succeeded")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestExportReportsWriteErrors(t *testing.T) {
	l, _ := newLog(t)
	appendAll(t, l, samples()[:1])
	if err := l.Export(context.Background(), failingWriter{}); err == nil {
		t.Error("Export to a failing writer succeeded")
	}
}

func TestTruncateNeverDeletesTheNewestEntry(t *testing.T) {
	l, db := newLog(t)
	ctx := context.Background()
	old := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return old })
	appendAll(t, l, samples()[:1])

	// A single old entry: nothing can be deleted without deleting the newest one.
	if n, err := l.Truncate(ctx, old.Add(time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil || n != 0 {
		t.Fatalf("Truncate(single) = %d, %v; want 0", n, err)
	}
	appendAll(t, l, samples()[:2])
	l.SetSigner(signer())
	n, err := l.Truncate(ctx, old.Add(time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"})
	if err != nil || n != 2 {
		t.Fatalf("Truncate(all old) = %d, %v; want 2", n, err)
	}
	var left int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&left)
	if left != 3 { // newest original entry + log.truncated + log.checkpoint
		t.Errorf("entries left = %d, want 3", left)
	}
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify = %+v", r)
	}
}

// failOn installs a trigger that aborts the given statement on audit_log.
func failOn(t *testing.T, db *sql.DB, op string) {
	t.Helper()
	stmt := "CREATE TRIGGER fail_" + op + " BEFORE " + op + " ON audit_log BEGIN SELECT RAISE(ABORT, 'injected'); END"
	if _, err := db.Exec(stmt); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFailuresAreReported(t *testing.T) {
	ctx := context.Background()
	actor := audit.Actor{Kind: audit.ActorSystem, ID: "retention"}

	l, db := newLog(t)
	failOn(t, db, "INSERT")
	if _, err := l.Append(ctx, samples()[0]); err == nil {
		t.Error("Append succeeded although the insert failed")
	}

	l, db = newLog(t)
	old := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return old })
	appendAll(t, l, samples()[:3])
	failOn(t, db, "DELETE")
	if _, err := l.Truncate(ctx, old.Add(time.Hour), actor); err == nil {
		t.Error("Truncate succeeded although the delete failed")
	}
	failOn(t, db, "INSERT")
	if _, err := l.Truncate(ctx, old.Add(time.Hour), actor); err == nil {
		t.Error("Truncate succeeded although the log.truncated entry failed")
	}
	if r := verify(t, l); !r.Valid {
		t.Errorf("failed truncations changed the log: %+v", r)
	}
}

func TestAppendTxOnAFinishedTransaction(t *testing.T) {
	l, db := newLog(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if _, err := l.AppendTx(context.Background(), tx, samples()[0]); err == nil {
		t.Error("AppendTx on a rolled-back transaction succeeded")
	}
}

func TestWithEntry(t *testing.T) {
	l, db := newLog(t)
	ctx := context.Background()
	count := func() (n int) {
		_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n)
		return n
	}

	ran := false
	if err := l.WithEntry(ctx, samples()[2], func() error { ran = true; return nil }); err != nil || !ran || count() != 1 {
		t.Fatalf("success: err %v, ran %v, entries %d", err, ran, count())
	}

	boom := errors.New("lock jammed")
	err := l.WithEntry(ctx, samples()[2], func() error { return boom })
	var actionErr *audit.ActionError
	if !errors.As(err, &actionErr) || !errors.Is(err, boom) || count() != 1 {
		t.Errorf("failed action: err %v, entries %d (the entry must be rolled back)", err, count())
	}

	ran = false
	if err := l.WithEntry(ctx, audit.Entry{Event: "unknown.event"}, func() error { ran = true; return nil }); err == nil || ran {
		t.Errorf("invalid entry: err %v, ran %v (the action must not run)", err, ran)
	}
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify = %+v", r)
	}
}

// retentionLog holds n entries a day apart, the last at last.
func retentionLog(t *testing.T, n int, last time.Time) (*audit.Log, *sql.DB) {
	t.Helper()
	l, db := newLog(t)
	for i := range n {
		at := last.Add(-time.Duration(n-1-i) * 24 * time.Hour)
		l.SetClock(func() time.Time { return at })
		appendAll(t, l, samples()[:1])
	}
	l.SetSigner(signer())
	return l, db
}

func firstSeq(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var first int64
	if err := db.QueryRow(`SELECT min(seq) FROM audit_log`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	return first
}

// The age of an entry counts from the newest entry when the clock lies beyond it: a clock
// that jumped years ahead does not make the whole log old.
func TestExpireCountsFromTheNewestEntry(t *testing.T) {
	last := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	l, db := retentionLog(t, 50, last)
	ctx := context.Background()
	actor := audit.Actor{Kind: audit.ActorSystem, ID: "retention"}
	l.SetClock(func() time.Time { return last.AddDate(10, 0, 0) })
	// 19 entries are more than 30 days older than the newest; a tenth of 50 per run.
	if n, err := l.Expire(ctx, 30*24*time.Hour, actor); err != nil || n != 5 {
		t.Fatalf("Expire = %d, %v; want 5", n, err)
	}
	for range 10 {
		if _, err := l.Expire(ctx, 30*24*time.Hour, actor); err != nil {
			t.Fatal(err)
		}
	}
	if first := firstSeq(t, db); first != 20 {
		t.Errorf("first seq = %d, want 20", first)
	}
	if r := verify(t, l); !r.Valid || r.Truncation != audit.TruncationAnchored {
		t.Errorf("Verify = %+v", r)
	}
}

// One run deletes at most a tenth of the entries, so that a wrong clock cannot wipe the
// log at once.
func TestExpireDeletesAtMostATenthPerRun(t *testing.T) {
	last := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	actor := audit.Actor{Kind: audit.ActorSystem, ID: "retention"}
	l, db := retentionLog(t, 40, last)
	l.SetClock(func() time.Time { return last })
	if n, err := l.Expire(ctx, time.Hour, actor); err != nil || n != 4 {
		t.Fatalf("Expire = %d, %v; want 4", n, err)
	}
	if first := firstSeq(t, db); first != 5 {
		t.Errorf("first seq = %d, want 5", first)
	}
	// Too few entries for a tenth: nothing is deleted.
	small, _ := retentionLog(t, 9, last)
	small.SetClock(func() time.Time { return last })
	if n, err := small.Expire(ctx, time.Hour, actor); err != nil || n != 0 {
		t.Errorf("Expire on 9 entries = %d, %v; want 0", n, err)
	}
	empty, _ := newLog(t)
	if n, err := empty.Expire(ctx, time.Hour, actor); err != nil || n != 0 {
		t.Errorf("Expire on an empty log = %d, %v", n, err)
	}
}

// While the clock lies behind the newest entry, nothing is deleted: the next run tries
// again.
func TestExpireWaitsWhileTheClockIsBehind(t *testing.T) {
	last := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	l, db := retentionLog(t, 40, last)
	actor := audit.Actor{Kind: audit.ActorSystem, ID: "retention"}
	l.SetClock(func() time.Time { return last.Add(-2 * audit.ClockTolerance) })
	if n, err := l.Expire(context.Background(), time.Hour, actor); !errors.Is(err, audit.ErrClockBehind) || n != 0 {
		t.Errorf("Expire = %d, %v; want ErrClockBehind", n, err)
	}
	if first := firstSeq(t, db); first != 1 {
		t.Errorf("first seq = %d, want 1", first)
	}
	_ = db.Close()
	if _, err := l.Expire(context.Background(), time.Hour, actor); err == nil {
		t.Error("Expire on a closed database succeeded")
	}
}
