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

	"github.com/home-mandate/home-mandate/internal/audit"
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
	n, err := l.Truncate(ctx, old.Add(time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"})
	if err != nil || n != 2 {
		t.Fatalf("Truncate(all old) = %d, %v; want 2", n, err)
	}
	var left int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&left)
	if left != 2 { // newest original entry + log.truncated
		t.Errorf("entries left = %d, want 2", left)
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
