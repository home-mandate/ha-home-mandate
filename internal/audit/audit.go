// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audit writes the hash-chained audit log of SPEC-v0 section 9 into the store
// and verifies it with the reference implementation from mandate-spec. Entries never
// contain tokens, nonces, credentials or the content of a mandate.
package audit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	specaudit "github.com/mandate-spec/mandate-spec/audit"
	"github.com/mandate-spec/mandate-spec/jcs"
)

// ErrInvalidEntry means an entry does not conform to the audit schema; it is not written.
var ErrInvalidEntry = errors.New("audit: invalid entry")

// Events (SPEC-v0 section 9.2).
const (
	EventDecision               = "decision"
	EventMandateCreated         = "mandate.created"
	EventMandateUpdated         = "mandate.updated"
	EventMandateRevoked         = "mandate.revoked"
	EventAgentRegistered        = "agent.registered"
	EventAgentRevoked           = "agent.revoked"
	EventEmergencyStopActivated = "emergency_stop.activated"
	EventEmergencyStopReleased  = "emergency_stop.released"
	EventAuthRejected           = "auth.rejected"
	EventLogTruncated           = "log.truncated"
)

const (
	entryType  = "https://mandate-spec.org/audit/v0"
	timeFormat = "2006-01-02T15:04:05.000Z07:00"
)

// Actor kinds.
const (
	ActorUser   = "user"
	ActorAgent  = "agent"
	ActorSystem = "system"
)

// Result statuses and denial sources.
const (
	StatusExecuted = "executed"
	StatusDenied   = "denied"
	StatusFailed   = "failed"

	DeniedByMandate        = "mandate"
	DeniedByApproval       = "approval"
	DeniedByRateLimit      = "rate_limit"
	DeniedByEmergencyStop  = "emergency_stop"
	DeniedByAuthentication = "authentication"
)

// Entry is what a caller records; type, id, seq, recorded_at, principal and prev are
// added by the log.
type Entry struct {
	Event      string
	Actor      *Actor
	Agent      *Agent
	Request    *Request
	Mandate    *Mandate
	Evaluation *Evaluation
	Approval   *Approval
	Result     *Result
	Truncated  *Truncated
}

// Actor is who triggered a change.
type Actor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Agent identifies the agent of an entry.
type Agent struct {
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name,omitempty"`
}

// Resource is the resource as requested or resolved.
type Resource struct {
	EntityID string `json:"entity_id"`
	Category string `json:"category,omitempty"`
	Area     string `json:"area,omitempty"`
}

// Request is the input of an evaluation.
type Request struct {
	Time     time.Time
	Timezone string
	Revoked  bool
	Resource Resource
	Action   string
}

// MarshalJSON writes the time in RFC 3339 with milliseconds.
func (r Request) MarshalJSON() ([]byte, error) {
	type wire struct {
		Time     string   `json:"time"`
		Timezone string   `json:"timezone,omitempty"`
		Revoked  bool     `json:"revoked,omitempty"`
		Resource Resource `json:"resource"`
		Action   string   `json:"action"`
	}
	return json.Marshal(wire{r.Time.UTC().Format(timeFormat), r.Timezone, r.Revoked, r.Resource, r.Action})
}

// Mandate refers to a mandate version by digest, never by content.
type Mandate struct {
	ID             string `json:"id"`
	Digest         string `json:"digest"`
	PreviousDigest string `json:"previous_digest,omitempty"`
}

// Evaluation is the result of SPEC-v0 section 4.1.
type Evaluation struct {
	Decision        string  `json:"decision"`
	Reason          string  `json:"reason"`
	RuleID          *string `json:"rule_id"` // null when no rule decided
	ApprovalTimeout string  `json:"approval_timeout,omitempty"`
}

// Approval is the outcome of an approval request.
type Approval struct {
	Outcome string
	By      string
	At      time.Time
}

// MarshalJSON writes the time in RFC 3339 with milliseconds.
func (a Approval) MarshalJSON() ([]byte, error) {
	type wire struct {
		Outcome string `json:"outcome"`
		By      string `json:"by,omitempty"`
		At      string `json:"at"`
	}
	return json.Marshal(wire{a.Outcome, a.By, a.At.UTC().Format(timeFormat)})
}

// Result is what happened with a request.
type Result struct {
	Status     string `json:"status"`
	DeniedBy   string `json:"denied_by,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// Truncated marks deleted entries (SPEC-v0 section 9.4).
type Truncated struct {
	UpToSeq    int64  `json:"up_to_seq"`
	LastDigest string `json:"last_digest"`
}

type wire struct {
	Type       string      `json:"type"`
	ID         string      `json:"id"`
	Seq        int64       `json:"seq"`
	RecordedAt string      `json:"recorded_at"`
	Event      string      `json:"event"`
	Principal  string      `json:"principal"`
	Actor      *Actor      `json:"actor,omitempty"`
	Agent      *Agent      `json:"agent,omitempty"`
	Request    *Request    `json:"request,omitempty"`
	Mandate    *Mandate    `json:"mandate,omitempty"`
	Evaluation *Evaluation `json:"evaluation,omitempty"`
	Approval   *Approval   `json:"approval,omitempty"`
	Result     *Result     `json:"result,omitempty"`
	Truncated  *Truncated  `json:"truncated,omitempty"`
	Prev       *string     `json:"prev"`
}

// Log is the audit log of one household.
type Log struct {
	db        *sql.DB
	principal string

	mu  sync.Mutex
	now func() time.Time
}

// New returns the log for principal on db (opened by internal/store).
func New(db *sql.DB, principal string) *Log {
	return &Log{db: db, principal: principal, now: time.Now}
}

// SetClock replaces the clock, for tests.
func (l *Log) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

func (l *Log) clock() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now()
}

// Append writes e in its own transaction and returns its seq.
func (l *Log) Append(ctx context.Context, e Entry) (int64, error) {
	var seq int64
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		seq, err = l.AppendTx(ctx, tx, e)
		return err
	})
	return seq, err
}

func (l *Log) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}

// AppendTx writes e within tx, so that a change and its entry commit together. The
// transaction must hold the write lock (the store opens all transactions IMMEDIATE),
// which serializes the chain.
func (l *Log) AppendTx(ctx context.Context, tx *sql.Tx, e Entry) (int64, error) {
	var lastSeq int64
	var lastDigest string
	err := tx.QueryRowContext(ctx, `SELECT seq, digest FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&lastSeq, &lastDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("audit: read chain head: %w", err)
	}
	now := l.clock()
	w := wire{
		Type: entryType, ID: newUUIDv7(now), Seq: lastSeq + 1, RecordedAt: now.UTC().Format(timeFormat),
		Event: e.Event, Principal: l.principal, Actor: e.Actor, Agent: e.Agent, Request: e.Request,
		Mandate: e.Mandate, Evaluation: e.Evaluation, Approval: e.Approval, Result: e.Result, Truncated: e.Truncated,
	}
	if lastSeq > 0 {
		w.Prev = &lastDigest
	}
	canonical, digest, err := encode(w)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (seq, recorded_at, event, entry, digest) VALUES (?, ?, ?, ?, ?)`,
		w.Seq, w.RecordedAt, w.Event, string(canonical), digest); err != nil {
		return 0, fmt.Errorf("audit: insert: %w", err)
	}
	return w.Seq, nil
}

// encode returns the canonical form (RFC 8785) and digest of w after checking it
// against the audit schema of mandate-spec.
func encode(w wire) ([]byte, string, error) {
	canonical, err := canonicalJSON(w)
	if err == nil {
		err = validate(w)
	}
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalJSON encodes v and canonicalizes it with mandate-spec/jcs, the same code
// that computes digests during verification.
func canonicalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var generic any
		if err = dec.Decode(&generic); err == nil {
			return jcs.Canonicalize(generic)
		}
	}
	return nil, fmt.Errorf("audit: encode: %w", err)
}

// validate checks the schema with the reference verifier: a copy of the entry as the
// first entry of a log (seq 1, prev null) must be a valid log on its own.
func validate(w wire) error {
	w.Seq, w.Prev = 1, nil
	data, err := canonicalJSON(w)
	if err != nil {
		return err
	}
	if r, err := specaudit.Verify([][]byte{data}); err != nil || !r.Valid {
		return fmt.Errorf("%w: event %q", ErrInvalidEntry, w.Event)
	}
	return nil
}

// Verify checks the whole stored log with mandate-spec (SPEC-v0 section 9.4).
func (l *Log) Verify(ctx context.Context) (specaudit.Result, error) {
	var entries [][]byte
	err := l.each(ctx, func(entry []byte) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return specaudit.Result{}, err
	}
	return specaudit.Verify(entries)
}

// Export writes the log as JSON Lines, the exchange format of SPEC-v0 section 9.4.
func (l *Log) Export(ctx context.Context, w io.Writer) error {
	return l.each(ctx, func(entry []byte) error {
		if _, err := w.Write(append(entry, '\n')); err != nil {
			return fmt.Errorf("audit: export: %w", err)
		}
		return nil
	})
}

func (l *Log) each(ctx context.Context, fn func([]byte) error) error {
	rows, err := l.db.QueryContext(ctx, `SELECT entry FROM audit_log ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("audit: read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			return fmt.Errorf("audit: read: %w", err)
		}
		if err := fn([]byte(entry)); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Truncate deletes entries recorded before cutoff, after appending a log.truncated
// entry that keeps the log verifiable (SPEC-v0 section 9.4). The newest entry is never
// deleted. It returns the number of deleted entries.
func (l *Log) Truncate(ctx context.Context, cutoff time.Time, actor Actor) (int64, error) {
	var removed int64
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		// n: the newest old entry, but never the newest entry of the log.
		var n sql.NullInt64
		var lastDigest sql.NullString
		err := tx.QueryRowContext(ctx, `WITH bounds AS (
				SELECT min((SELECT max(seq) FROM audit_log WHERE recorded_at < ?), (SELECT max(seq) FROM audit_log) - 1) AS n)
			SELECT n, (SELECT digest FROM audit_log WHERE seq = n) FROM bounds`,
			cutoff.UTC().Format(timeFormat)).Scan(&n, &lastDigest)
		if err != nil {
			return fmt.Errorf("audit: find old entries: %w", err)
		}
		if !n.Valid || !lastDigest.Valid {
			return nil
		}
		if _, err := l.AppendTx(ctx, tx, Entry{Event: EventLogTruncated, Actor: &actor,
			Truncated: &Truncated{UpToSeq: n.Int64, LastDigest: lastDigest.String}}); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE seq <= ?`, n.Int64)
		if err != nil {
			return fmt.Errorf("audit: delete: %w", err)
		}
		removed, _ = res.RowsAffected()
		return nil
	})
	return removed, err
}

// newUUIDv7 returns a UUIDv7 (RFC 9562): 48-bit Unix milliseconds, version, variant,
// and random bits from crypto/rand.
func newUUIDv7(t time.Time) string {
	var b [16]byte
	_, _ = rand.Read(b[6:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(t.UnixMilli()))
	copy(b[0:6], ms[2:8])
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
