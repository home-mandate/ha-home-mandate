// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

func askEntry(approval *audit.Approval, result audit.Result) audit.Entry {
	return audit.Entry{Event: audit.EventDecision,
		Agent: &audit.Agent{ClientID: "hm-client:voice-7c21e9a4", DisplayName: "Voice assistant"},
		Request: &audit.Request{Time: time.Date(2026, 10, 6, 19, 0, 0, 0, time.UTC), Timezone: "Europe/Berlin",
			Resource: audit.Resource{EntityID: "lock.front_door", Category: "lock"}, Action: "unlock"},
		Mandate:    &audit.Mandate{ID: "m-voice", Digest: "sha256:" + string(bytes.Repeat([]byte("a"), 64))},
		Evaluation: &audit.Evaluation{Decision: "ask", Reason: "rule", RuleID: str("r-locks"), ApprovalTimeout: "PT2M"},
		Approval:   approval, Result: &result}
}

// SPEC-v0 section 9.1 (spec v0.1.0-alpha.4): a request ended without an answer is
// cancelled with its cause and the matching denied_by; anything else is refused.
func TestCancelledApprovalsWithTheirCause(t *testing.T) {
	at := time.Date(2026, 10, 6, 19, 1, 0, 0, time.UTC)
	cancelled := func(cause string) *audit.Approval {
		return &audit.Approval{Outcome: audit.OutcomeCancelled, Cause: cause, At: at}
	}
	denied := func(by string) audit.Result { return audit.Result{Status: audit.StatusDenied, DeniedBy: by} }
	for name, tc := range map[string]struct {
		approval *audit.Approval
		result   audit.Result
		valid    bool
	}{
		"revoked agent":       {cancelled(audit.CauseRevoked), denied(audit.DeniedByAuthentication), true},
		"revoked mandate":     {cancelled(audit.CauseRevoked), denied(audit.DeniedByMandate), true},
		"emergency stop":      {cancelled(audit.CauseEmergencyStop), denied(audit.DeniedByEmergencyStop), true},
		"interrupted":         {cancelled(audit.CauseInterrupted), denied(audit.DeniedByApproval), true},
		"withdrawn":           {cancelled(audit.CauseWithdrawn), denied(audit.DeniedByApproval), true},
		"outcome unknown":     {&audit.Approval{Outcome: "approved", By: "user-1", Via: "push", At: at}, audit.Result{Status: audit.StatusFailed, Error: "outcome_unknown"}, true},
		"without cause":       {cancelled(""), denied(audit.DeniedByApproval), false},
		"cause mismatch":      {cancelled(audit.CauseEmergencyStop), denied(audit.DeniedByApproval), false},
		"with a responder":    {&audit.Approval{Outcome: audit.OutcomeCancelled, Cause: audit.CauseRevoked, By: "user-1", At: at}, denied(audit.DeniedByAuthentication), false},
		"cause with approved": {&audit.Approval{Outcome: "approved", Cause: audit.CauseRevoked, By: "user-1", At: at}, denied(audit.DeniedByAuthentication), false},
		"failed":              {cancelled(audit.CauseInterrupted), audit.Result{Status: audit.StatusFailed, Error: "x"}, false},
	} {
		l, _ := newLog(t)
		_, err := l.Append(context.Background(), askEntry(tc.approval, tc.result))
		if tc.valid && err != nil || !tc.valid && !errors.Is(err, audit.ErrInvalidEntry) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestApprovalJSONHasTheCause(t *testing.T) {
	at := time.Date(2026, 10, 6, 19, 1, 0, 0, time.UTC)
	data, err := json.Marshal(audit.Approval{Outcome: audit.OutcomeCancelled, Cause: audit.CauseInterrupted, At: at})
	if err != nil || string(data) != `{"outcome":"cancelled","at":"2026-10-06T19:01:00.000Z","cause":"interrupted"}` {
		t.Errorf("approval = %s, %v", data, err)
	}
}

// The approval journal keeps the request of a decision entry and reads it back.
func TestRequestRoundTrip(t *testing.T) {
	in := audit.Request{Time: time.Date(2026, 10, 6, 19, 0, 0, 123e6, time.UTC), Timezone: "Europe/Berlin", Revoked: true,
		Resource: audit.Resource{EntityID: "light.kitchen", Category: "light", Area: "kitchen", Critical: true},
		Action:   "turn_on", Parameters: map[string]int64{"brightness_pct": 50}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out audit.Request
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Time.Equal(in.Time) || out.Time.Location() != time.UTC {
		t.Errorf("time = %v", out.Time)
	}
	out.Time = in.Time
	if !reflect.DeepEqual(out, in) {
		t.Errorf("request = %+v", out)
	}
	for _, bad := range []string{`{"time":"yesterday"}`, `[]`} {
		if err := json.Unmarshal([]byte(bad), &out); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

// Also runs in the transaction that writes the entry: both commit or neither.
func TestAlsoRunsInTheEntrysTransaction(t *testing.T) {
	ctx := context.Background()
	l, db := newLog(t)
	if _, err := db.Exec(`CREATE TABLE marks (n INTEGER) STRICT`); err != nil {
		t.Fatal(err)
	}
	mark := func(n int) func(context.Context, *sql.Tx) error {
		return func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO marks (n) VALUES (?)`, n)
			return err
		}
	}
	e := samples()[0]
	e.Also = mark(1)
	if _, err := l.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.Also = mark(2)
	if err := l.WithEntry(ctx, e, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	e.Also = mark(3)
	if err := l.WithEntry(ctx, e, func() error { return errors.New("ha down") }); err == nil {
		t.Fatal("failed action committed")
	}
	boom := errors.New("journal broken")
	e.Also = func(context.Context, *sql.Tx) error { return boom }
	if _, err := l.Append(ctx, e); !errors.Is(err, boom) {
		t.Errorf("failing Also: %v", err)
	}
	ran := false
	if err := l.WithEntry(ctx, e, func() error { ran = true; return nil }); !errors.Is(err, boom) || ran {
		t.Errorf("failing Also before the action: %v, ran %v", err, ran)
	}
	var marks, entries int
	_ = db.QueryRow(`SELECT count(*) FROM marks`).Scan(&marks)
	_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&entries)
	if marks != 2 || entries != 2 {
		t.Errorf("marks %d, entries %d", marks, entries)
	}
}
