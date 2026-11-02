// SPDX-License-Identifier: AGPL-3.0-or-later

// Package intent keeps the intent of every direct execution outside the process (SPEC-v0
// section 9, recording before executing): a row is committed before Home Assistant is
// called and deleted in the transaction of the audit entry that records the result. A row
// a previous process left is recorded at start as failed/outcome_unknown and deleted: the
// command may have reached Home Assistant. Confirmed actions keep their intent in the
// approval journal (state executing, internal/approval).
package intent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// ErrStore is the error of an intent the store could not write or end.
var ErrStore = errors.New("intent: store unavailable")

// ErrorOutcomeUnknown is the result error of an execution whose outcome the process that
// started it did not record (SPEC-v0 section 11.1 item 9).
const ErrorOutcomeUnknown = "outcome_unknown"

const (
	idBytes    = 16
	timeFormat = "2006-01-02T15:04:05.000Z07:00"
)

// Appender writes an audit entry within a transaction (audit.Log).
type Appender interface {
	AppendTx(ctx context.Context, tx *sql.Tx, e audit.Entry) (int64, error)
}

// decision is the stored part of the audit entry.
type decision struct {
	Agent      *audit.Agent      `json:"agent"`
	Request    *audit.Request    `json:"request"`
	Mandate    *audit.Mandate    `json:"mandate,omitempty"`
	Evaluation *audit.Evaluation `json:"evaluation,omitempty"`
}

// Store is the intent store on the store's database.
type Store struct {
	db     *sql.DB
	now    func() time.Time
	logger *slog.Logger
}

// New returns the store on db (opened by internal/store).
func New(db *sql.DB, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Store{db: db, now: time.Now, logger: logger}
}

// SetClock replaces the clock, for tests.
func (s *Store) SetClock(now func() time.Time) {
	s.now = now
}

// Begin stores the intent of e (its agent, request, mandate and evaluation) durably and
// returns its ID. Nothing is dispatched unless Begin returned without an error.
func (s *Store) Begin(ctx context.Context, e audit.Entry) (string, error) {
	stored, err := json.Marshal(decision{Agent: e.Agent, Request: e.Request, Mandate: e.Mandate, Evaluation: e.Evaluation})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrStore, err)
	}
	id := newID()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO execution_intents (id, created_at, decision) VALUES (?, ?, ?)`,
		id, s.now().UTC().Format(timeFormat), string(stored)); err != nil {
		return "", fmt.Errorf("%w: %w", ErrStore, err)
	}
	return id, nil
}

// End returns the step that ends the intent in the transaction of the audit entry that
// records the result (audit.Entry.Also): both commit or neither. An intent that is
// already gone is left alone, the entry must not be lost for it.
func (s *Store) End(id string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM execution_intents WHERE id = ?`, id); err != nil {
			return fmt.Errorf("%w: %w", ErrStore, err)
		}
		return nil
	}
}

// EndNow ends the intent in a transaction of its own, for when ending it with its audit
// entry failed and the entry was written without.
func (s *Store) EndNow(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM execution_intents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("%w: %w", ErrStore, err)
	}
	return nil
}

// Count is the number of intents in the store, for tests.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM execution_intents`).Scan(&n); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrStore, err)
	}
	return n, nil
}

type row struct {
	id, createdAt, decision string
}

// Recover records every intent a previous process left as failed/outcome_unknown and
// deletes it, each entry in one transaction with the deletion, so that a crash during
// Recover leaves no duplicate (SPEC-v0 sections 9 and 11.1 item 9). It returns how many
// it recorded, also when it stops at an error. A row whose stored decision cannot be read or is refused by the schema is
// recorded with a minimal entry from what the row still tells; if even that is refused,
// the row is deleted and the loss logged, so that it cannot block the start.
func (s *Store) Recover(ctx context.Context, log Appender) (int, error) {
	rows, err := s.unended(ctx)
	if err != nil {
		return 0, err
	}
	for n, r := range rows {
		if err := s.recoverOne(ctx, log, r); err != nil {
			return n, err
		}
	}
	return len(rows), nil
}

func (s *Store) unended(ctx context.Context) ([]row, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT id, created_at, decision FROM execution_intents ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStore, err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.createdAt, &r.decision); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStore, err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStore, err)
	}
	return out, nil
}

func (s *Store) recoverOne(ctx context.Context, log Appender, r row) error {
	e, err := recoveryEntry(r)
	if err == nil {
		err = s.record(ctx, log, r.id, e)
	}
	if err == nil || !unrecordable(err) {
		return err
	}
	s.logger.Error("execution ended by the restart recorded with a minimal entry", "error", err)
	if err = s.record(ctx, log, r.id, minimalEntry(r, s.now())); err == nil || !unrecordable(err) {
		return err
	}
	s.logger.Error("execution ended by the restart cannot be recorded in the audit log", "error", err)
	return s.EndNow(ctx, r.id)
}

// record writes e and deletes the intent in one transaction.
func (s *Store) record(ctx context.Context, log Appender, id string, e audit.Entry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStore, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := log.AppendTx(ctx, tx, e); err != nil {
		return err
	}
	if err := s.End(id)(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrStore, err)
	}
	return nil
}

// errUnreadable means a row's stored decision cannot be read.
var errUnreadable = errors.New("intent: row unreadable")

func unrecordable(err error) bool {
	return errors.Is(err, audit.ErrInvalidEntry) || errors.Is(err, errUnreadable)
}

// recoveryEntry is the audit entry of an execution the restart ended: the stored
// decision with the result failed/outcome_unknown.
func recoveryEntry(r row) (audit.Entry, error) {
	var d decision
	if err := json.Unmarshal([]byte(r.decision), &d); err != nil {
		return audit.Entry{}, fmt.Errorf("%w: %w", errUnreadable, err)
	}
	if d.Agent == nil || d.Request == nil {
		return audit.Entry{}, fmt.Errorf("%w: agent or request missing", errUnreadable)
	}
	return audit.Entry{Event: audit.EventDecision, Agent: d.Agent, Request: d.Request, Mandate: d.Mandate, Evaluation: d.Evaluation,
		Result: &audit.Result{Status: audit.StatusFailed, Error: ErrorOutcomeUnknown}}, nil
}

// minimalEntry is the entry of an execution whose stored decision is unusable: the time
// the intent was stored and what can still be read of agent and request, without
// evaluation. An entry without an agent or a resource is refused by the schema and ends
// in the deletion of the row.
func minimalEntry(r row, now time.Time) audit.Entry {
	at, err := time.Parse(time.RFC3339Nano, r.createdAt)
	if err != nil {
		at = now
	}
	var d decision
	_ = json.Unmarshal([]byte(r.decision), &d)
	e := audit.Entry{Event: audit.EventDecision, Agent: d.Agent, Request: &audit.Request{Time: at},
		Result: &audit.Result{Status: audit.StatusFailed, Error: ErrorOutcomeUnknown}}
	if d.Request != nil {
		e.Request.Resource.EntityID, e.Request.Action = d.Request.Resource.EntityID, d.Request.Action
	}
	return e
}

func newID() string {
	var b [idBytes]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return hex.EncodeToString(b[:])
}
