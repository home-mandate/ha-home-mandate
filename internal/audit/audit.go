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
	"github.com/home-mandate/spec/evaluator"
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
	EventMandateRemoved         = "mandate.removed"
	EventMandateProposed        = "mandate.proposed"
	EventMandateDiscarded       = "mandate.discarded"
	EventAgentRegistered        = "agent.registered"
	EventAgentRevoked           = "agent.revoked"
	EventAgentReconnected       = "agent.reconnected"
	EventAgentRemoved           = "agent.removed"
	EventEmergencyStopActivated = "emergency_stop.activated"
	EventEmergencyStopReleased  = "emergency_stop.released"
	EventAuthRejected           = "auth.rejected"
	EventLogTruncated           = "log.truncated"
	EventLogCheckpoint          = "log.checkpoint"
	EventDirectoryChanged       = "directory.changed"
	EventTemplateChanged        = "template.changed"
	EventApproverChanged        = "approver.changed"
	EventConfigChanged          = "config.changed"
	EventLogExported            = "log.exported"
	// Events of audit/v1 (SPEC-v0 sections 9.2 and 11.7).
	EventAgentBound           = "agent.bound"
	EventCredentialsWithdrawn = "credentials.withdrawn"
	EventCertUnexpected       = "cert.unexpected"
	EventCertAcknowledged     = "cert.acknowledged"
	EventCTStatus             = "ct.status"
)

// Reasons of auth.rejected (SPEC-v0 section 9.1, audit/v1).
const (
	AuthUnknown        = "unknown"
	AuthExpired        = "expired"
	AuthWithdrawn      = "withdrawn"
	AuthRevoked        = "revoked"
	AuthAudience       = "audience"
	AuthClient         = "client"
	AuthProof          = "proof"
	AuthReplay         = "replay"
	AuthUnboundRefused = "unbound_refused"
	AuthSecondKey      = "second_key"
	AuthNotApprover    = "not_approver"
	AuthOther          = "other"
)

// Scopes and causes of credentials.withdrawn (SPEC-v0 section 9.1).
const (
	CredentialsAll   = "all"
	CredentialsAgent = "agent"

	WithdrawnEmergencyStop = "emergency_stop"
	WithdrawnPublicOrigin  = "public_origin"
	WithdrawnUser          = "user"
	WithdrawnRefreshReuse  = "refresh_reuse"
	WithdrawnBound         = "bound"
)

// Statuses of cert.unexpected and of ct.status (SPEC-v0 section 11.7 item 7).
const (
	CertAlarm        = "alarm"
	CertNotYetValid  = "not_yet_valid"
	CertPastExposure = "past_exposure"

	CTOK          = "ok"
	CTLimited     = "limited"
	CTUnavailable = "unavailable"
)

// Settings of config.changed (SPEC-v0 section 9.1).
const (
	SettingTimezone      = "timezone"
	SettingClockCheck    = "clock_check"
	SettingCheckpointKey = "checkpoint_key"
	SettingPublicOrigin  = "public_origin"
	SettingRemoteAccess  = "remote_access"
	SettingUnboundTokens = "unbound_tokens"
	SettingServerKey     = "server_key"
	SettingTrustedProxy  = "trusted_proxy"
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
	// entryType is the version Home-Mandate writes (SPEC-v0 section 14); entryTypeV0 the one
	// it wrote up to 0.1, which an existing log still holds.
	entryType   = "https://home-mandate.org/audit/v1"
	entryTypeV0 = "https://home-mandate.org/audit/v0"
	timeFormat  = "2006-01-02T15:04:05.000Z07:00"
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
	// DeniedByAudit: the request was not executed because it could not be recorded
	// (SPEC-v0 section 9).
	DeniedByAudit = "audit"
)

// OutcomeCancelled is the outcome of an approval request that ended before anyone
// answered, with one of the causes (SPEC-v0 section 11.1 item 8).
const (
	OutcomeCancelled = "cancelled"

	CauseWithdrawn     = "withdrawn"
	CauseRevoked       = "revoked"
	CauseEmergencyStop = "emergency_stop"
	CauseInterrupted   = "interrupted"
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
	// Auth is why a sign-in, a token or an answer was rejected; required with auth.rejected.
	Auth        *Auth
	Binding     *Binding
	Credentials *Credentials
	Certificate *Certificate
	CT          *CT
	Config      *Config
	Exported    *Exported
	// checkpoint marks an entry of Checkpoint; its signature is made when the entry
	// gets its place in the chain.
	checkpoint bool
	// ApprovalID is the ID of the approval request (internal/approval) that this decision
	// entry ends. It is not part of the entry: it only tells the OnCommit hook which open
	// request in the UI the entry closes.
	ApprovalID string
	// Also, if set, runs in the transaction that writes the entry, before the action of
	// WithEntry: both commit or neither (the approval journal ends a request together with
	// the entry that records its end). It is not part of the entry.
	Also func(ctx context.Context, tx *sql.Tx) error
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
	// Areas is the set of areas of the resource (SPEC-v0 section 3.4).
	Areas []string `json:"areas,omitempty"`
	// Critical: the household had marked the entity as critical (SPEC-v0 section 4).
	Critical bool `json:"critical,omitempty"`
	// CriticalStated writes critical also when it is false, as an entry with a deny that
	// was not enforced must (SPEC-v0 section 9.1).
	CriticalStated bool `json:"-"`
}

// MarshalJSON writes critical: false only where it must be stated.
func (r Resource) MarshalJSON() ([]byte, error) {
	type plain Resource
	if !r.CriticalStated {
		return json.Marshal(plain(r))
	}
	return json.Marshal(struct {
		plain
		Critical bool `json:"critical"`
	}{plain(r), r.Critical})
}

// Request is the input of an evaluation.
type Request struct {
	Time     time.Time
	Timezone string
	Revoked  bool
	Resource Resource
	Action   string
	// Parameters of the action that were input to the evaluation (SPEC-v0 section 4.5):
	// integers or strings.
	Parameters map[string]evaluator.Value
}

// MarshalJSON writes the time in RFC 3339 with milliseconds.
func (r Request) MarshalJSON() ([]byte, error) {
	return json.Marshal(requestWire{r.Time.UTC().Format(timeFormat), r.Timezone, r.Revoked, r.Resource, r.Action, r.Parameters})
}

// UnmarshalJSON reads what MarshalJSON wrote (the approval journal keeps the request of
// an entry it may have to write after a restart).
func (r *Request) UnmarshalJSON(data []byte) error {
	var w requestWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, w.Time)
	if err != nil {
		return fmt.Errorf("audit: request time: %w", err)
	}
	*r = Request{Time: at.UTC(), Timezone: w.Timezone, Revoked: w.Revoked, Resource: w.Resource, Action: w.Action, Parameters: w.Parameters}
	return nil
}

type requestWire struct {
	Time       string                     `json:"time"`
	Timezone   string                     `json:"timezone,omitempty"`
	Revoked    bool                       `json:"revoked,omitempty"`
	Resource   Resource                   `json:"resource"`
	Action     string                     `json:"action"`
	Parameters map[string]evaluator.Value `json:"parameters,omitempty"`
}

// Mandate refers to a mandate version by digest, never by content.
type Mandate struct {
	ID             string `json:"id"`
	Digest         string `json:"digest"`
	PreviousDigest string `json:"previous_digest,omitempty"`
	// Cause tells why a proposal did not take effect; only with mandate.discarded.
	Cause string `json:"cause,omitempty"`
}

// Causes of mandate.discarded (SPEC-v0 sections 9.1 and 11.6 item 4).
const (
	DiscardRejected   = "rejected"
	DiscardSuperseded = "superseded"
	DiscardRevoked    = "revoked"
	DiscardExpired    = "expired"
)

// Evaluation is the result of SPEC-v0 section 4.1.
type Evaluation struct {
	Decision        string  `json:"decision"`
	Reason          string  `json:"reason"`
	RuleID          *string `json:"rule_id"` // null when no rule decided
	ApprovalTimeout string  `json:"approval_timeout,omitempty"`
	// NotEnforced: a deny by the rules of a mandate in audit mode that was executed
	// (SPEC-v0 section 4.6); written as enforced: false.
	NotEnforced bool `json:"-"`
	// Confirm: such a deny that required a confirmation because a matching rule decides
	// ask (SPEC-v0 sections 4.1 and 9.1); written as confirm: true, only with NotEnforced.
	Confirm bool `json:"-"`
}

// MarshalJSON writes enforced: false for a deny that was not enforced, with confirm: true
// if it required a confirmation, and nothing for every other result.
func (e Evaluation) MarshalJSON() ([]byte, error) {
	type plain Evaluation
	w := struct {
		plain
		Enforced *bool `json:"enforced,omitempty"`
		Confirm  *bool `json:"confirm,omitempty"`
	}{plain: plain(e)}
	if e.NotEnforced {
		w.Enforced = new(bool)
		if e.Confirm {
			w.Confirm = new(true)
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads what MarshalJSON wrote.
func (e *Evaluation) UnmarshalJSON(data []byte) error {
	type plain Evaluation
	var w struct {
		plain
		Enforced *bool `json:"enforced"`
		Confirm  *bool `json:"confirm"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*e = Evaluation(w.plain)
	e.NotEnforced = w.Enforced != nil && !*w.Enforced
	e.Confirm = e.NotEnforced && w.Confirm != nil && *w.Confirm
	return nil
}

// Approval is the outcome of an approval request; Via is the channel the answer came
// through (push or ui), empty for a timeout. Cause is set only for OutcomeCancelled: why
// the request ended without an answer (SPEC-v0 section 9.1).
type Approval struct {
	Outcome string
	By      string
	Via     string
	At      time.Time
	Cause   string
}

// MarshalJSON writes the time in RFC 3339 with milliseconds.
func (a Approval) MarshalJSON() ([]byte, error) {
	type wire struct {
		Outcome string `json:"outcome"`
		By      string `json:"by,omitempty"`
		Via     string `json:"via,omitempty"`
		At      string `json:"at"`
		Cause   string `json:"cause,omitempty"`
	}
	return json.Marshal(wire{a.Outcome, a.By, a.Via, a.At.UTC().Format(timeFormat), a.Cause})
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

// Auth is why a sign-in, a token or an answer was rejected (audit/v1).
type Auth struct {
	Reason string `json:"reason"`
}

// Binding is the key an agent's tokens are bound to (agent.bound), or the key refused for
// a second binding (auth.rejected, second_key); audit/v1.
type Binding struct {
	Method string `json:"method"`
	JKT    string `json:"jkt"`
}

// BindingDPoP is the only method of a Binding.
const BindingDPoP = "dpop"

// Credentials are the tokens that lost their validity together (credentials.withdrawn).
type Credentials struct {
	Scope string `json:"scope"`
	Cause string `json:"cause"`
}

// Certificate is a certificate for the host of the public origin (cert.unexpected): all
// members; for cert.acknowledged only Digest or only PublicKey. Names and Issuer come
// from the certificate and are recorded unchanged.
type Certificate struct {
	Digest    string    `json:"-"`
	Status    string    `json:"-"`
	Names     []string  `json:"-"`
	Issuer    string    `json:"-"`
	PublicKey string    `json:"-"`
	NotBefore time.Time `json:"-"`
	NotAfter  time.Time `json:"-"`
	Logged    time.Time `json:"-"`
}

// MarshalJSON writes the times in RFC 3339 with milliseconds and leaves out what is not set.
func (c Certificate) MarshalJSON() ([]byte, error) {
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(timeFormat)
	}
	return json.Marshal(struct {
		Digest    string   `json:"digest,omitempty"`
		Status    string   `json:"status,omitempty"`
		Names     []string `json:"names,omitempty"`
		Issuer    string   `json:"issuer,omitempty"`
		PublicKey string   `json:"public_key,omitempty"`
		NotBefore string   `json:"not_before,omitempty"`
		NotAfter  string   `json:"not_after,omitempty"`
		Logged    string   `json:"logged,omitempty"`
	}{c.Digest, c.Status, c.Names, c.Issuer, c.PublicKey, at(c.NotBefore), at(c.NotAfter), at(c.Logged)})
}

// CT is the state of Certificate Transparency monitoring (ct.status); PreviousStatus is
// empty for the first entry.
type CT struct {
	Status         string `json:"status"`
	PreviousStatus string `json:"previous_status,omitempty"`
}

// Config is a change of a setting (config.changed): Value empty if the setting was removed,
// PreviousValue empty if it had none.
type Config struct {
	Setting       string `json:"setting"`
	Value         string `json:"value,omitempty"`
	PreviousValue string `json:"previous_value,omitempty"`
}

// Exported is the range of entries an export contained (log.exported).
type Exported struct {
	FromSeq int64 `json:"from_seq"`
	ToSeq   int64 `json:"to_seq"`
}

// Truncated marks deleted entries (SPEC-v0 section 9.4).
type Truncated struct {
	UpToSeq    int64  `json:"up_to_seq"`
	LastDigest string `json:"last_digest"`
}

type wire struct {
	Type        string       `json:"type"`
	ID          string       `json:"id"`
	Seq         int64        `json:"seq"`
	RecordedAt  string       `json:"recorded_at"`
	Event       string       `json:"event"`
	Principal   string       `json:"principal"`
	Actor       *Actor       `json:"actor,omitempty"`
	Agent       *Agent       `json:"agent,omitempty"`
	Request     *Request     `json:"request,omitempty"`
	Mandate     *Mandate     `json:"mandate,omitempty"`
	Evaluation  *Evaluation  `json:"evaluation,omitempty"`
	Approval    *Approval    `json:"approval,omitempty"`
	Result      *Result      `json:"result,omitempty"`
	Truncated   *Truncated   `json:"truncated,omitempty"`
	Directory   *Directory   `json:"directory,omitempty"`
	Template    *Template    `json:"template,omitempty"`
	Approver    *Approver    `json:"approver,omitempty"`
	Auth        *Auth        `json:"auth,omitempty"`
	Binding     *Binding     `json:"binding,omitempty"`
	Credentials *Credentials `json:"credentials,omitempty"`
	Certificate *Certificate `json:"certificate,omitempty"`
	CT          *CT          `json:"ct,omitempty"`
	Config      *Config      `json:"config,omitempty"`
	Exported    *Exported    `json:"exported,omitempty"`
	Checkpoint  *checkpoint  `json:"checkpoint,omitempty"`
	Prev        *string      `json:"prev"`
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

	// version0 makes the log write audit/v0, for tests of a log that moves to version 1.
	version0 bool
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
	var head chainHead
	err := tx.QueryRowContext(ctx, `SELECT seq, digest, event, coalesce(json_extract(entry, '$.type'), '') FROM audit_log ORDER BY seq DESC LIMIT 1`).
		Scan(&head.seq, &head.digest, &head.event, &head.typ)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("audit: read chain head: %w", err)
	}
	typ := entryType
	if l.version0 {
		typ = entryTypeV0
	}
	// The move of an existing log to version 1 (SPEC-v0 section 14): with a signing key, a
	// checkpoint of version 0 is the last entry before the first of version 1. A checkpoint
	// due now is that one.
	if head.typ == entryTypeV0 && typ != entryTypeV0 && l.signer() != nil {
		switch {
		case e.checkpoint:
			typ = entryTypeV0
		case head.event != EventLogCheckpoint:
			seq, digest, err := l.appendTx(ctx, tx, Entry{Event: EventLogCheckpoint, checkpoint: true}, entryTypeV0, head.seq, head.digest)
			if err != nil {
				return 0, err
			}
			head.seq, head.digest = seq, digest
		}
	}
	seq, _, err := l.appendTx(ctx, tx, e, typ, head.seq, head.digest)
	return seq, err
}

// chainHead is the last entry of the log; the zero value for an empty log.
type chainHead struct {
	seq                int64
	digest, event, typ string
}

// appendTx writes e as an entry of type typ after the entry lastSeq with lastDigest and
// returns its seq and digest.
func (l *Log) appendTx(ctx context.Context, tx *sql.Tx, e Entry, typ string, lastSeq int64, lastDigest string) (int64, string, error) {
	var err error
	now := l.clock()
	w := wire{
		Type: typ, ID: newUUIDv7(now), Seq: lastSeq + 1, RecordedAt: now.UTC().Format(timeFormat),
		Event: e.Event, Principal: l.principal, Actor: e.Actor, Agent: e.Agent, Request: e.Request,
		Mandate: e.Mandate, Evaluation: e.Evaluation, Approval: e.Approval, Result: e.Result, Truncated: e.Truncated, Directory: e.Directory,
		Template: e.Template, Approver: e.Approver, Auth: e.Auth, Binding: e.Binding, Credentials: e.Credentials,
		Certificate: e.Certificate, CT: e.CT, Config: e.Config, Exported: e.Exported,
	}
	if lastSeq > 0 {
		w.Prev = &lastDigest
	}
	if e.checkpoint {
		if w.Checkpoint, err = l.sign(lastSeq, lastDigest, w.RecordedAt); err != nil {
			return 0, "", err
		}
	}
	canonical, digest, err := encode(w)
	if err != nil {
		return 0, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (seq, recorded_at, event, entry, digest) VALUES (?, ?, ?, ?, ?)`,
		w.Seq, w.RecordedAt, w.Event, string(canonical), digest); err != nil {
		return 0, "", fmt.Errorf("audit: insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_search (seq, text) VALUES (?, ?)`, w.Seq, searchText(e.Agent, e.Request)); err != nil {
		return 0, "", fmt.Errorf("audit: insert search text: %w", err)
	}
	if e.Also != nil {
		if err := e.Also(ctx, tx); err != nil {
			return 0, "", err
		}
	}
	return w.Seq, digest, nil
}

// searchText is what the search of the UI matches in an entry (decision B2): its
// entity_id, areas, agent name as the UI shows it and client ID, folded, one per line (a
// search text never has a line break, so it cannot match across fields). Device and
// area names come from the current catalog at search time, not from here.
func searchText(a *Agent, r *Request) string {
	var parts []string
	if r != nil {
		parts = append(parts, r.Resource.EntityID)
		parts = append(parts, r.Resource.Areas...)
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

// validate checks the rules for a new entry, which are stricter than what a verifier accepts
// in an existing log (SPEC-v0 section 9.1), and the schema with the reference verifier: a
// copy of the entry as the first entry of a log (seq 1, prev null) must be a valid log on
// its own.
func validate(w wire) error {
	entries := [][]byte{}
	w.Seq, w.Prev = 1, nil
	if w.Truncated != nil {
		// up_to_seq is smaller than the entry's own seq (SPEC-v0 section 9.4); Truncate
		// guarantees that, so the structural check runs with the smallest value behind a
		// placeholder instead of the real one.
		w.Truncated = &Truncated{UpToSeq: 1, LastDigest: w.Truncated.LastDigest}
	}
	if w.Exported != nil {
		// The range lies before the entry's own seq; Log.ExportRecorded guarantees that, so
		// the check runs with the smallest range behind a placeholder.
		w.Exported = &Exported{FromSeq: 1, ToSeq: 1}
	}
	if w.Checkpoint != nil || w.Truncated != nil || w.Exported != nil {
		// A checkpoint is never the first entry, a truncation or an export covers nothing
		// as the first: check them behind a placeholder.
		first := wire{Type: w.Type, ID: w.ID, Seq: 1, RecordedAt: w.RecordedAt, Event: EventEmergencyStopActivated,
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
	if err := specaudit.CheckNew(data); err != nil {
		return fmt.Errorf("%w: event %q: %w", ErrInvalidEntry, w.Event, err)
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
	err := l.each(ctx, func(_ int64, entry []byte) error {
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
	checkpoints, err := l.HasCheckpoints(ctx)
	if err != nil {
		return Verification{}, err
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
	_, err := l.export(ctx, w)
	return err
}

// ExportRecorded writes the log as Export does and records the export with log.exported
// and the range it contained, by the human by (SPEC-v0 sections 9.2 and 13). Entries
// written meanwhile are not in the range.
func (l *Log) ExportRecorded(ctx context.Context, w io.Writer, by Actor) error {
	r, err := l.export(ctx, w)
	if err != nil {
		return err
	}
	e := Entry{Event: EventLogExported, Actor: &by}
	if r.ToSeq > 0 {
		e.Exported = &r
	}
	_, err = l.Append(ctx, e)
	return err
}

// export writes every entry and returns the range it wrote; zero for an empty log.
func (l *Log) export(ctx context.Context, w io.Writer) (Exported, error) {
	var r Exported
	err := l.each(ctx, func(seq int64, entry []byte) error {
		if r.FromSeq == 0 {
			r.FromSeq = seq
		}
		r.ToSeq = seq
		if _, err := w.Write(append(entry, '\n')); err != nil {
			return fmt.Errorf("audit: export: %w", err)
		}
		return nil
	})
	return r, err
}

// each calls fn for every entry in order; one query, so that it reads one state of the log.
func (l *Log) each(ctx context.Context, fn func(int64, []byte) error) error {
	rows, err := l.db.QueryContext(ctx, `SELECT seq, entry FROM audit_log ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("audit: read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var entry string
		if err := rows.Scan(&seq, &entry); err != nil {
			return fmt.Errorf("audit: read: %w", err)
		}
		if err := fn(seq, []byte(entry)); err != nil {
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
		var err error
		removed, err = l.truncateTx(ctx, tx, cutoff, limit, actor)
		return err
	})
	if err != nil {
		return 0, err // rolled back: nothing was deleted
	}
	return removed, nil
}

// truncateTx is truncate within tx, so that a caller can combine it with other changes
// that must commit together or not at all.
func (l *Log) truncateTx(ctx context.Context, tx *sql.Tx, cutoff time.Time, limit int64, actor Actor) (int64, error) {
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
		return 0, fmt.Errorf("audit: find old entries: %w", err)
	}
	if !n.Valid || !lastDigest.Valid {
		return 0, nil
	}
	if _, err := l.AppendTx(ctx, tx, Entry{Event: EventLogTruncated, Actor: &actor,
		Truncated: &Truncated{UpToSeq: n.Int64, LastDigest: lastDigest.String}}); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE seq <= ?`, n.Int64)
	if err != nil {
		return 0, fmt.Errorf("audit: delete: %w", err)
	}
	removed, _ := res.RowsAffected()
	// The entry that accounts for the removed beginning must itself be anchored.
	if l.signer() != nil {
		if _, err := l.AppendTx(ctx, tx, Entry{Event: EventLogCheckpoint, checkpoint: true}); err != nil {
			return 0, err
		}
	}
	return removed, nil
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
