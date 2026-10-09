// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audit writes the hash-chained audit log of SPEC-v0 section 9 into the store
// and verifies it with the reference implementation from the specification. Entries never
// contain tokens, nonces, credentials or the content of a mandate or a template.
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
	"math"
	"strings"
	"sync"
	"time"

	specaudit "github.com/home-mandate/spec/audit"
	"github.com/home-mandate/spec/jcs"
	"github.com/home-mandate/spec/jws"

	"github.com/home-mandate/ha-home-mandate/internal/untrusted"
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
	EventLogCheckpoint          = "log.checkpoint"
	EventDirectoryChanged       = "directory.changed"
	EventTemplateChanged        = "template.changed"
	EventApproverChanged        = "approver.changed"
)

// Changes of the resource directory (SPEC-v0 sections 9.1 and 11.4).
const (
	DirectoryCriticalMarked   = "critical_marked"
	DirectoryCriticalUnmarked = "critical_unmarked"
	DirectoryRenamed          = "renamed"
	DirectoryRenameApplied    = "rename_applied"
	DirectoryRenameDismissed  = "rename_dismissed"
)

// Changes of a template (SPEC-v0 section 9.1).
const (
	TemplateStored  = "stored"
	TemplateRemoved = "removed"
	TemplateHidden  = "hidden"
	TemplateShown   = "shown"
)

// Changes of the approvers (SPEC-v0 sections 9.1 and 11.1).
const (
	ApproverAdded   = "added"
	ApproverRemoved = "removed"
)

const (
	entryType  = "https://home-mandate.org/audit/v0"
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
	Directory  *Directory
	Template   *Template
	Approver   *Approver
	// checkpoint marks an entry of Checkpoint; its signature is made when the entry
	// gets its place in the chain.
	checkpoint bool
	// ApprovalID is the ID of the approval request (internal/approval) that this decision
	// entry ends. It is not part of the entry: it only tells the OnCommit hook which open
	// request in the UI the entry closes.
	ApprovalID string
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
	// Critical: the household had marked the entity as critical (SPEC-v0 section 4).
	Critical bool `json:"critical,omitempty"`
}

// Request is the input of an evaluation.
type Request struct {
	Time     time.Time
	Timezone string
	Revoked  bool
	Resource Resource
	Action   string
	// Parameters of the action that were input to the evaluation (SPEC-v0 section 4.5).
	Parameters map[string]int64
}

// MarshalJSON writes the time in RFC 3339 with milliseconds.
func (r Request) MarshalJSON() ([]byte, error) {
	type wire struct {
		Time       string           `json:"time"`
		Timezone   string           `json:"timezone,omitempty"`
		Revoked    bool             `json:"revoked,omitempty"`
		Resource   Resource         `json:"resource"`
		Action     string           `json:"action"`
		Parameters map[string]int64 `json:"parameters,omitempty"`
	}
	return json.Marshal(wire{r.Time.UTC().Format(timeFormat), r.Timezone, r.Revoked, r.Resource, r.Action, r.Parameters})
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

// Approval is the outcome of an approval request; Via is the channel the answer came
// through (push or ui), empty for a timeout.
type Approval struct {
	Outcome string
	By      string
	Via     string
	At      time.Time
}

// MarshalJSON writes the time in RFC 3339 with milliseconds.
func (a Approval) MarshalJSON() ([]byte, error) {
	type wire struct {
		Outcome string `json:"outcome"`
		By      string `json:"by,omitempty"`
		Via     string `json:"via,omitempty"`
		At      string `json:"at"`
	}
	return json.Marshal(wire{a.Outcome, a.By, a.Via, a.At.UTC().Format(timeFormat)})
}

// Result is what happened with a request.
type Result struct {
	Status     string `json:"status"`
	DeniedBy   string `json:"denied_by,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// Directory is a change of the resource directory that affects the evaluation: a critical
// mark set or removed, a renamed entity found, or a rename resolved (SPEC-v0 section
// 11.4). PreviousEntityID is the former ID of a rename; empty for a critical mark.
type Directory struct {
	Change           string `json:"change"`
	EntityID         string `json:"entity_id"`
	PreviousEntityID string `json:"previous_entity_id,omitempty"`
}

// Template is a change of a mandate template: stored (created or changed), removed,
// hidden or shown again. Digest is that of the new content for stored, of the last
// content for removed, and optionally of the current content otherwise; PreviousDigest
// only for stored, when the template had other content before. Never the content itself.
type Template struct {
	Change         string `json:"change"`
	Name           string `json:"name"`
	Digest         string `json:"digest,omitempty"`
	PreviousDigest string `json:"previous_digest,omitempty"`
}

// Approver is a person added to or removed from those who answer approval requests;
// ID is their user ID in Home Assistant, as in approval.by.
type Approver struct {
	Change string `json:"change"`
	ID     string `json:"id"`
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
	Directory  *Directory  `json:"directory,omitempty"`
	Template   *Template   `json:"template,omitempty"`
	Approver   *Approver   `json:"approver,omitempty"`
	Checkpoint *checkpoint `json:"checkpoint,omitempty"`
	Prev       *string     `json:"prev"`
}

// Log is the audit log of one household.
type Log struct {
	db        *sql.DB
	principal string

	mu       sync.Mutex
	now      func() time.Time
	onCommit func(seq int64, e Entry)

	// checkpointSigner signs checkpoints; nil: the log writes none.
	signerMu         sync.Mutex
	checkpointSigner *Signer
}

// New returns the log for principal on db (opened by internal/store).
func New(db *sql.DB, principal string) *Log {
	return &Log{db: db, principal: principal, now: time.Now}
}

// ClockTolerance is how far the clock may lie behind the newest entry, e.g. after a
// small correction by NTP, before Home-Mandate stops deciding.
const ClockTolerance = time.Minute

// ClockBehind reports whether the clock lies more than ClockTolerance before the newest
// entry of the log: then it is wrong, and validity periods and time windows cannot be
// trusted (SPEC-v0 section 11.4).
func (l *Log) ClockBehind(ctx context.Context) (bool, error) {
	// The latest time, not the last entry: entries written while the clock was behind
	// must not hide it. timeFormat has a fixed width in UTC, so text order is time order.
	// Entries up to an accepted position (AcceptClock) do not count.
	// It runs for every request: the index on recorded_at is walked from the latest time
	// down to the first entry after the accepted position, mostly the very first one.
	var newest sql.NullString
	err := l.db.QueryRowContext(ctx, `SELECT recorded_at FROM audit_log INDEXED BY audit_log_recorded_at WHERE seq >
		coalesce((SELECT CAST(value AS INTEGER) FROM settings WHERE key = ?), 0) ORDER BY recorded_at DESC LIMIT 1`, settingClockAccepted).Scan(&newest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("audit: read: %w", err)
	}
	at, err := time.Parse(timeFormat, newest.String)
	if err != nil {
		return false, fmt.Errorf("audit: newest entry: %w", err)
	}
	return l.clock().Add(ClockTolerance).Before(at), nil
}

// settingClockAccepted holds the position up to which entries do not count for
// ClockBehind.
const settingClockAccepted = "audit_clock_accepted_seq"

// AcceptClock makes ClockBehind ignore every entry written so far. A human runs it after
// the clock ran ahead by mistake and was corrected: the entries with the future times
// would otherwise stop every decision until that time. It returns the position and the
// latest time it set aside.
func (l *Log) AcceptClock(ctx context.Context) (int64, string, error) {
	var seq sql.NullInt64
	var latest sql.NullString
	if err := l.db.QueryRowContext(ctx, `SELECT max(seq), max(recorded_at) FROM audit_log`).Scan(&seq, &latest); err != nil {
		return 0, "", fmt.Errorf("audit: read: %w", err)
	}
	if _, err := l.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		settingClockAccepted, fmt.Sprint(seq.Int64), l.clock().UTC().Format(timeFormat)); err != nil {
		return 0, "", fmt.Errorf("audit: accept clock: %w", err)
	}
	return seq.Int64, latest.String, nil
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

// OnCommit sets a function that is called after an entry written by Append or WithEntry
// was committed (not for AppendTx, whose transaction belongs to the caller). It must not
// block.
func (l *Log) OnCommit(fn func(seq int64, e Entry)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onCommit = fn
}

func (l *Log) committed(seq int64, e Entry) {
	l.mu.Lock()
	fn := l.onCommit
	l.mu.Unlock()
	if fn != nil {
		fn(seq, e)
	}
}

// Append writes e in its own transaction and returns its seq.
func (l *Log) Append(ctx context.Context, e Entry) (int64, error) {
	var seq int64
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		seq, err = l.AppendTx(ctx, tx, e)
		return err
	})
	if err == nil {
		l.committed(seq, e)
	}
	return seq, err
}

// ActionError wraps the error of the action passed to WithEntry.
type ActionError struct{ Err error }

func (e *ActionError) Error() string { return "audit: action failed: " + e.Err.Error() }
func (e *ActionError) Unwrap() error { return e.Err }

// WithEntry appends e and runs action in one transaction, so that nothing is executed
// without its entry: if the entry cannot be written, action does not run; if action
// fails, the entry is rolled back and the error is returned as *ActionError. The write
// lock is held while action runs.
func (l *Log) WithEntry(ctx context.Context, e Entry, action func() error) error {
	var seq int64
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if seq, err = l.AppendTx(ctx, tx, e); err != nil {
			return err
		}
		if err := action(); err != nil {
			return &ActionError{Err: err}
		}
		return nil
	})
	if err == nil {
		l.committed(seq, e)
	}
	return err
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
		Mandate: e.Mandate, Evaluation: e.Evaluation, Approval: e.Approval, Result: e.Result, Truncated: e.Truncated, Directory: e.Directory,
		Template: e.Template, Approver: e.Approver,
	}
	if lastSeq > 0 {
		w.Prev = &lastDigest
	}
	if e.checkpoint {
		if w.Checkpoint, err = l.sign(lastSeq, lastDigest); err != nil {
			return 0, err
		}
	}
	canonical, digest, err := encode(w)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (seq, recorded_at, event, entry, digest) VALUES (?, ?, ?, ?, ?)`,
		w.Seq, w.RecordedAt, w.Event, string(canonical), digest); err != nil {
		return 0, fmt.Errorf("audit: insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_search (seq, text) VALUES (?, ?)`, w.Seq, searchText(e.Agent, e.Request)); err != nil {
		return 0, fmt.Errorf("audit: insert search text: %w", err)
	}
	return w.Seq, nil
}

// searchText is what the search of the UI matches in an entry (decision B2): its
// entity_id, area, agent name as the UI shows it and client ID, folded, one per line (a
// search text never has a line break, so it cannot match across fields). Device and
// area names come from the current catalog at search time, not from here.
func searchText(a *Agent, r *Request) string {
	var parts []string
	if r != nil {
		parts = append(parts, r.Resource.EntityID, r.Resource.Area)
	}
	if a != nil {
		parts = append(parts, untrusted.Clean(a.DisplayName, untrusted.Max), a.ClientID)
	}
	for i, p := range parts {
		parts[i] = untrusted.Fold(p)
	}
	return strings.Join(parts, "\n")
}

// encode returns the canonical form (RFC 8785) and digest of w after checking it
// against the audit schema of the specification.
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

// canonicalJSON encodes v and canonicalizes it with spec/jcs, the same code
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
	entries := [][]byte{}
	w.Seq, w.Prev = 1, nil
	if w.Checkpoint != nil {
		// A checkpoint is never the first entry: check it behind a placeholder.
		first := wire{Type: entryType, ID: w.ID, Seq: 1, RecordedAt: w.RecordedAt, Event: EventEmergencyStopActivated,
			Principal: w.Principal, Actor: &Actor{Kind: ActorSystem, ID: "placeholder"}}
		data, err := canonicalJSON(first)
		digest, digestErr := specaudit.Digest(data)
		if err := errors.Join(err, digestErr); err != nil {
			return err
		}
		entries = append(entries, data)
		w.Seq, w.Prev = 2, &digest
	}
	data, err := canonicalJSON(w)
	if err != nil {
		return err
	}
	if r, err := specaudit.Verify(append(entries, data)); err != nil || !r.Valid {
		return fmt.Errorf("%w: event %q", ErrInvalidEntry, w.Event)
	}
	return nil
}

// How the beginning of a log that verifies with the specification is accounted for.
const (
	// TruncationNone: the log starts at seq 1.
	TruncationNone = "none"
	// TruncationAnchored: a verified checkpoint covers the log.truncated entry.
	TruncationAnchored = "anchored"
	// TruncationUnanchored: no checkpoint covers it, and the log has none at all.
	TruncationUnanchored = "unanchored"
	// TruncationTampered: no checkpoint covers it although the log has checkpoints;
	// Home-Mandate writes one after every truncation of its own.
	TruncationTampered = "tampered"
)

// Verification is the result of verifying the stored log. It is stricter than the specification,
// which accepts any log.truncated entry for a deleted beginning (SPEC-v0 section 9.4):
// that entry needs no key, so Home-Mandate counts a truncation as valid only if a
// verified checkpoint covers it. Otherwise Valid is false and BrokenAt is FirstSeq.
type Verification struct {
	specaudit.Result
	// Truncation is one of the Truncation constants; empty if the chain is broken.
	Truncation string
}

// Verify checks the whole stored log with the specification (SPEC-v0 section 9.4).
func (l *Log) Verify(ctx context.Context) (Verification, error) {
	r, _, err := l.Check(ctx)
	return r, err
}

// Check is Verify that also returns how many entries it checked.
func (l *Log) Check(ctx context.Context) (Verification, int, error) {
	var entries [][]byte
	err := l.each(ctx, func(entry []byte) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return Verification{}, 0, err
	}
	r, err := l.verifyEntries(entries)
	if err != nil || !r.Valid {
		return Verification{Result: r}, len(entries), err
	}
	strict, err := l.truncation(ctx, r)
	return strict, len(entries), err
}

func (l *Log) verifyEntries(entries [][]byte) (specaudit.Result, error) {
	if signer := l.signer(); signer != nil {
		return specaudit.VerifyAnchored(entries, specaudit.Anchor{
			Keys: jws.Keys{signer.KeyID: signer.Key.Public()}, LogID: signer.LogID})
	}
	return specaudit.Verify(entries)
}

// truncation applies Home-Mandate's own rule for a deleted beginning to a valid log.
func (l *Log) truncation(ctx context.Context, r specaudit.Result) (Verification, error) {
	switch {
	case r.FirstSeq <= 1:
		return Verification{Result: r, Truncation: TruncationNone}, nil
	case r.TruncationAnchored:
		return Verification{Result: r, Truncation: TruncationAnchored}, nil
	}
	var checkpoints bool
	if err := l.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM audit_log WHERE event = ?)`,
		EventLogCheckpoint).Scan(&checkpoints); err != nil {
		return Verification{}, fmt.Errorf("audit: read: %w", err)
	}
	out := Verification{Result: specaudit.Result{Index: 0, BrokenAt: r.FirstSeq, FirstSeq: r.FirstSeq, LogID: r.LogID},
		Truncation: TruncationUnanchored}
	if checkpoints {
		out.Truncation = TruncationTampered
	}
	return out, nil
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

// ErrClockBehind means the clock lies more than ClockTolerance before the newest entry.
var ErrClockBehind = errors.New("audit: clock behind the newest entry")

// expireShare: one run of Expire deletes at most this share of the entries (one in ten),
// so that a wrong clock cannot wipe the log at once.
const expireShare = 10

// Expire deletes the entries older than retention, as the retention of the gateway does.
// Age counts back from the newest entry Home-Mandate did not write for its own upkeep
// (log.truncated, log.checkpoint), or from the clock if that is earlier: a clock that
// jumped ahead neither ages the log by itself nor through the entries Expire writes. It
// deletes at most a tenth of the entries per run and nothing while ClockBehind holds
// (ErrClockBehind; the next run tries again).
func (l *Log) Expire(ctx context.Context, retention time.Duration, actor Actor) (int64, error) {
	behind, err := l.ClockBehind(ctx)
	if err != nil {
		return 0, err
	}
	if behind {
		return 0, ErrClockBehind
	}
	var first, last sql.NullInt64
	var newest sql.NullString
	if err := l.db.QueryRowContext(ctx, `SELECT min(seq), max(seq),
		(SELECT max(recorded_at) FROM audit_log WHERE event NOT IN (?, ?)) FROM audit_log`,
		EventLogTruncated, EventLogCheckpoint).Scan(&first, &last, &newest); err != nil {
		return 0, fmt.Errorf("audit: read: %w", err)
	}
	if !newest.Valid {
		return 0, nil
	}
	cutoff, err := time.Parse(timeFormat, newest.String)
	if err != nil {
		return 0, fmt.Errorf("audit: newest entry: %w", err)
	}
	if now := l.clock(); now.Before(cutoff) {
		cutoff = now
	}
	return l.truncate(ctx, cutoff.Add(-retention), (last.Int64-first.Int64+1)/expireShare, actor)
}

// Truncate deletes entries recorded before cutoff, after appending a log.truncated
// entry that keeps the log verifiable (SPEC-v0 section 9.4). The newest entry is never
// deleted. It returns the number of deleted entries.
func (l *Log) Truncate(ctx context.Context, cutoff time.Time, actor Actor) (int64, error) {
	return l.truncate(ctx, cutoff, math.MaxInt32, actor)
}

// truncate is Truncate that deletes at most limit entries.
func (l *Log) truncate(ctx context.Context, cutoff time.Time, limit int64, actor Actor) (int64, error) {
	var removed int64
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		// n: the newest old entry, but never the newest entry of the log, nor more than
		// limit entries.
		var n sql.NullInt64
		var lastDigest sql.NullString
		err := tx.QueryRowContext(ctx, `WITH bounds AS (
				SELECT min((SELECT max(seq) FROM audit_log WHERE recorded_at < ?), (SELECT max(seq) FROM audit_log) - 1,
					(SELECT min(seq) FROM audit_log) - 1 + ?) AS n)
			SELECT n, (SELECT digest FROM audit_log WHERE seq = n) FROM bounds`,
			cutoff.UTC().Format(timeFormat), limit).Scan(&n, &lastDigest)
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
		// The entry that accounts for the removed beginning must itself be anchored.
		if l.signer() != nil {
			if _, err := l.AppendTx(ctx, tx, Entry{Event: EventLogCheckpoint, checkpoint: true}); err != nil {
				return err
			}
		}
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
