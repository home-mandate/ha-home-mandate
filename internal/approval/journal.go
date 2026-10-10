// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// The approval journal (ARCHITECTURE section 7, issue #27) keeps every request outside
// the process, so that a restart ends the waiting ones visibly and never executes one
// afterwards (SPEC-v0 section 11.1 item 9). A request enters it open before anyone is
// notified; after a confirmation it is set to executing and committed before Home
// Assistant is called; it ends in the transaction of the audit entry that records the
// end (audit.Entry.Also), so that entry and end commit together. It never holds the
// nonce, a token or a credential.

// ErrJournal means the journal could not be written; a request that cannot be entered
// is denied, and a confirmed one is not executed.
var ErrJournal = errors.New("approval: journal unavailable")

// ErrorOutcomeUnknown is the audit error of a confirmed action whose execution was under
// way when the process ended (SPEC-v0 section 11.1 item 9).
const ErrorOutcomeUnknown = "outcome_unknown"

const (
	stateOpen      = "open"
	stateExecuting = "executing"
	stateEnded     = "ended"

	// keepEnded is how long an ended request stays in the journal (for the outcome an agent
	// may fetch later, issue #27); then it is deleted.
	keepEnded = 24 * time.Hour

	timeFormat = "2006-01-02T15:04:05.000Z07:00"
)

// Notified is one device a request was sent to, with the language of its approver, so
// that the notification can be cleared or replaced after a restart.
type Notified struct {
	UserID  string `json:"user_id"`
	Service string `json:"service"`
	Lang    string `json:"lang"`
}

// Record is what the journal keeps of a request beyond Request: the parts of its audit
// entry an entry written after a restart needs (agent, request, mandate and evaluation;
// the rest of Entry is ignored) and the digest of the effective call.
type Record struct {
	Entry        audit.Entry
	ParamsDigest string
	// AgentRef is the ID the agent knows the request by (issue #27): random, neither the
	// nonce, the request ID nor the tag; found only together with the agent's client_id.
	AgentRef string
}

// Opened is a request as it enters the journal.
type Opened struct {
	ID       string // the request's ID in the UI, never the nonce
	Tag      string // the notifications' tag
	Request  Request
	Created  time.Time
	Expires  time.Time
	Notified []Notified
}

// decision is the stored part of the audit entry, and the names shown after a restart.
type decision struct {
	Agent      *audit.Agent      `json:"agent"`
	Request    *audit.Request    `json:"request"`
	Mandate    *audit.Mandate    `json:"mandate,omitempty"`
	Evaluation *audit.Evaluation `json:"evaluation"`
	Device     string            `json:"device,omitempty"`
}

// Appender writes an audit entry within a transaction (audit.Log).
type Appender interface {
	AppendTx(ctx context.Context, tx *sql.Tx, e audit.Entry) (int64, error)
}

// Journal is the approval journal on the store's database.
type Journal struct {
	db     *sql.DB
	now    func() time.Time
	logger *slog.Logger
}

// NewJournal returns the journal on db (opened by internal/store).
func NewJournal(db *sql.DB, logger *slog.Logger) *Journal {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Journal{db: db, now: time.Now, logger: logger}
}

// SetClock replaces the clock, for tests.
func (j *Journal) SetClock(now func() time.Time) {
	j.now = now
}

// Open enters a request, open. Ended requests older than a day are deleted in the same
// transaction.
func (j *Journal) Open(ctx context.Context, o Opened) error {
	if o.Request.Record == nil {
		return fmt.Errorf("%w: request without a record", ErrJournal)
	}
	e := o.Request.Record.Entry
	stored, err := json.Marshal(decision{Agent: e.Agent, Request: e.Request, Mandate: e.Mandate, Evaluation: e.Evaluation, Device: o.Request.Device})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	notified, err := json.Marshal(append([]Notified{}, o.Notified...))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	return j.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := expire(ctx, tx, j.now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_journal (id, client_id, entity_id, action, params_digest, created_at,
			expires_at, state, tag, notified, decision, agent_ref) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			o.ID, o.Request.ClientID, o.Request.EntityID, o.Request.Action, o.Request.Record.ParamsDigest, format(o.Created),
			format(o.Expires), stateOpen, o.Tag, string(notified), string(stored), o.Request.Record.AgentRef)
		return err
	})
}

// Delivered replaces the devices of a request, entered as planned, by those it reached.
func (j *Journal) Delivered(ctx context.Context, id string, notified []Notified) error {
	data, err := json.Marshal(append([]Notified{}, notified...))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	if _, err := j.db.ExecContext(ctx, `UPDATE approval_journal SET notified = ? WHERE id = ?`, string(data), id); err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	return nil
}

// Executing marks an open request as confirmed and about to be executed, with the
// answer, and commits that before the caller calls Home Assistant: a crash during the
// call is then recognised as an outcome that is unknown.
func (j *Journal) Executing(ctx context.Context, id string, a audit.Approval) error {
	res, err := j.db.ExecContext(ctx, `UPDATE approval_journal SET state = ?, outcome = ?, answered_by = ?, answered_via = ?,
		answered_at = ? WHERE id = ? AND state = ?`, stateExecuting, a.Outcome, a.By, a.Via, format(a.At), id, stateOpen)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: the request is not open", ErrJournal)
	}
	return nil
}

// End returns the step that ends a request in the transaction of the audit entry that
// records its end (audit.Entry.Also), with the approval (nil without one) and the
// result. A request that is not in the journal or ended already is left alone: the
// entry must not be lost for it.
func (j *Journal) End(id string, a *audit.Approval, result audit.Result) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		return j.end(ctx, tx, id, a, result, "")
	}
}

// EndNow ends a request in a transaction of its own, for when ending it with its audit
// entry failed and the entry was written without.
func (j *Journal) EndNow(ctx context.Context, id string, a *audit.Approval, result audit.Result) error {
	return j.inTx(ctx, func(tx *sql.Tx) error {
		return j.end(ctx, tx, id, a, result, "")
	})
}

func (j *Journal) end(ctx context.Context, tx *sql.Tx, id string, a *audit.Approval, result audit.Result, notice string) error {
	stored, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	var outcome, cause string
	if a != nil {
		outcome, cause = a.Outcome, a.Cause
	}
	if _, err := tx.ExecContext(ctx, `UPDATE approval_journal SET state = ?, outcome = CASE WHEN ? = '' THEN outcome ELSE ? END,
		cause = ?, result = ?, notice = ?, ended_at = ? WHERE id = ? AND state <> ?`,
		stateEnded, outcome, outcome, cause, string(stored), notice, format(j.now()), id, stateEnded); err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	return nil
}

// Status is what the journal knows of a request for its agent.
type Status struct {
	State   string // open, executing or ended
	Expires time.Time
	Outcome string       // the approval outcome once known
	Cause   string       // with cancelled
	Result  audit.Result // once ended
}

// Ended tells whether the request has ended.
func (s Status) Ended() bool { return s.State == stateEnded }

// Lookup finds the request the agent clientID knows by ref. Unknown references, those of
// other agents and those of requests deleted after a day all give false, alike.
func (j *Journal) Lookup(ctx context.Context, ref, clientID string) (Status, bool, error) {
	if ref == "" {
		return Status{}, false, nil
	}
	var st Status
	var expires string
	var result sql.NullString
	err := j.db.QueryRowContext(ctx, `SELECT state, expires_at, outcome, cause, result FROM approval_journal
		WHERE agent_ref = ? AND client_id = ?`, ref, clientID).Scan(&st.State, &expires, &st.Outcome, &st.Cause, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, false, fmt.Errorf("%w: %w", ErrJournal, err)
	}
	if st.Expires, err = time.Parse(time.RFC3339Nano, expires); err != nil {
		return Status{}, false, fmt.Errorf("%w: expiry: %w", ErrJournal, err)
	}
	if result.Valid {
		if err := json.Unmarshal([]byte(result.String), &st.Result); err != nil {
			return Status{}, false, fmt.Errorf("%w: result: %w", ErrJournal, err)
		}
	}
	return st, true, nil
}

// Recovered counts what Recover ended.
type Recovered struct {
	Interrupted int // waiting requests, cancelled
	Unknown     int // executions under way, outcome unknown
}

// row is a request Recover ends.
type row struct {
	id, state, by, via, answeredAt, decision string
	clientID, entityID, action, createdAt    string
}

// Recover ends what a previous process left in the journal, before agents can call
// (SPEC-v0 section 11.1 item 9): every open request is recorded as cancelled with cause
// interrupted, every executing one as failed with outcome_unknown and the answer that
// confirmed it. Nothing is executed or reopened. Each entry commits together with the
// end of its request, so a crash during Recover leaves no duplicate. The approvers are
// told later (Announce), once Home Assistant can be reached.
func (j *Journal) Recover(ctx context.Context, log Appender) (Recovered, error) {
	rows, err := j.unended(ctx)
	if err != nil {
		return Recovered{}, err
	}
	var out Recovered
	for _, r := range rows {
		if err := j.recoverOne(ctx, log, r); err != nil {
			return out, err
		}
		if r.state == stateExecuting {
			out.Unknown++
		} else {
			out.Interrupted++
		}
	}
	return out, nil
}

func (j *Journal) unended(ctx context.Context) ([]row, error) {
	rs, err := j.db.QueryContext(ctx, `SELECT id, state, answered_by, answered_via, coalesce(answered_at, ''), decision,
		client_id, entity_id, action, created_at FROM approval_journal WHERE state <> ? ORDER BY created_at, id`, stateEnded)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJournal, err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.state, &r.by, &r.via, &r.answeredAt, &r.decision, &r.clientID, &r.entityID, &r.action, &r.createdAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrJournal, err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJournal, err)
	}
	return out, nil
}

// recoverOne writes the entry of r and ends it. If the stored entry cannot be read or
// is refused by the schema, a minimal entry from the row's columns is written instead
// (minimalEntry); if even that is refused, the row still ends without an entry, logged
// as an error, so that it cannot block the start.
func (j *Journal) recoverOne(ctx context.Context, log Appender, r row) error {
	now := j.now()
	e, notice, err := recoveryEntry(r, now)
	if err == nil {
		err = j.record(ctx, log, r.id, e, notice)
	}
	if err == nil || !unrecordable(err) {
		return err
	}
	j.logger.Error("approval request ended by the restart recorded with a minimal entry", "state", r.state, "error", err)
	if err = j.record(ctx, log, r.id, minimalEntry(r, now), notice); err == nil || !unrecordable(err) {
		return err
	}
	j.logger.Error("approval request ended by the restart cannot be recorded in the audit log", "state", r.state, "error", err)
	return j.inTx(ctx, func(tx *sql.Tx) error {
		return j.end(ctx, tx, r.id, nil, audit.Result{Status: audit.StatusFailed, Error: "unrecorded"}, notice)
	})
}

// record writes e and ends the request with it in one transaction.
func (j *Journal) record(ctx context.Context, log Appender, id string, e audit.Entry, notice string) error {
	return j.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := log.AppendTx(ctx, tx, e); err != nil {
			return err
		}
		return j.end(ctx, tx, id, e.Approval, *e.Result, notice)
	})
}

func unrecordable(err error) bool {
	return errors.Is(err, audit.ErrInvalidEntry) || errors.Is(err, errUnreadable)
}

// minimalEntry is the entry of a request the restart ended whose stored entry is
// unusable, built from the row's columns alone: the agent, the time the request was
// made, the resource and the action, without evaluation or approval. A waiting request
// is denied (approval_interrupted), an execution under way failed (outcome_unknown).
func minimalEntry(r row, now time.Time) audit.Entry {
	at, err := time.Parse(time.RFC3339Nano, r.createdAt)
	if err != nil {
		at = now
	}
	e := audit.Entry{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: r.clientID},
		Request: &audit.Request{Time: at, Resource: audit.Resource{EntityID: r.entityID}, Action: r.action},
		Result:  &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval, Error: "approval_interrupted"}}
	if r.state == stateExecuting {
		e.Result = &audit.Result{Status: audit.StatusFailed, Error: ErrorOutcomeUnknown}
	}
	return e
}

// errUnreadable means a journal row's stored entry cannot be read.
var errUnreadable = errors.New("approval: journal row unreadable")

// recoveryEntry is the audit entry of a request the restart ended, and the notice owed
// to its approvers.
func recoveryEntry(r row, now time.Time) (audit.Entry, string, error) {
	var d decision
	err := json.Unmarshal([]byte(r.decision), &d)
	e := audit.Entry{Event: audit.EventDecision, Agent: d.Agent, Request: d.Request, Mandate: d.Mandate, Evaluation: d.Evaluation}
	if r.state != stateExecuting {
		e.Approval = &audit.Approval{Outcome: OutcomeCancelled, Cause: audit.CauseInterrupted, At: now}
		e.Result = &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
		if err != nil {
			return e, noticeInterrupted, fmt.Errorf("%w: %w", errUnreadable, err)
		}
		return e, noticeInterrupted, nil
	}
	at, atErr := time.Parse(time.RFC3339Nano, r.answeredAt)
	e.Approval = &audit.Approval{Outcome: OutcomeApproved, By: r.by, Via: r.via, At: at}
	e.Result = &audit.Result{Status: audit.StatusFailed, Error: ErrorOutcomeUnknown}
	if err := errors.Join(err, atErr); err != nil {
		return e, noticeUnknown, fmt.Errorf("%w: %w", errUnreadable, err)
	}
	return e, noticeUnknown, nil
}

// Notices owed to the approvers after a restart, as in the notice column.
const (
	noticeInterrupted = "interrupted"
	noticeUnknown     = "outcome_unknown"
)

// Announce replaces the notifications of requests a restart ended, on every device they
// went to, by one without buttons and with the same tag: the request ended and nothing
// was executed, or for an execution under way that its outcome is unknown and the device
// should be checked. A notice that reached at least one device is not sent again; failed
// deliveries are logged.
func (j *Journal) Announce(ctx context.Context, n Notifier) (int, error) {
	rs, err := j.db.QueryContext(ctx, `SELECT id, tag, notified, decision, notice FROM approval_journal WHERE notice <> '' ORDER BY created_at, id`)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrJournal, err)
	}
	type owed struct{ id, tag, notified, decision, notice string }
	var list []owed
	for rs.Next() {
		var o owed
		if err := rs.Scan(&o.id, &o.tag, &o.notified, &o.decision, &o.notice); err != nil {
			rs.Close()
			return 0, fmt.Errorf("%w: %w", ErrJournal, err)
		}
		list = append(list, o)
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrJournal, err)
	}
	for _, o := range list {
		var devices []Notified
		var d decision
		if err := errors.Join(json.Unmarshal([]byte(o.notified), &devices), json.Unmarshal([]byte(o.decision), &d)); err != nil {
			j.logger.Error("notice after a restart not sent: journal row unreadable", "error", err)
		}
		delivered := len(devices) == 0
		for _, dev := range devices {
			if err := n.Notify(ctx, dev.Service, buildNotice(i18n.Pick(dev.Lang), o.notice, d, o.tag)); err != nil {
				j.logger.Warn("notice after a restart not delivered", "approver", dev.UserID, "notify_service", dev.Service, "error", err)
				continue
			}
			delivered = true
		}
		if !delivered {
			continue // reached nobody: tried again after the next start
		}
		if _, err := j.db.ExecContext(ctx, `UPDATE approval_journal SET notice = '' WHERE id = ?`, o.id); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrJournal, err)
		}
	}
	return len(list), nil
}

// buildNotice is the notification that replaces a request's after a restart.
func buildNotice(lang i18n.Lang, notice string, d decision, tag string) ha.Notification {
	agentName, device, action := "", "", ""
	if d.Agent != nil {
		agentName = sanitize(d.Agent.DisplayName, maxName)
	}
	if d.Request != nil {
		device, action = sanitize(cmp.Or(d.Device, d.Request.Resource.EntityID), maxName), i18n.ActionName(lang, d.Request.Action)
	}
	args := i18n.Args{"agent": agentName, "device": device, "action": action}
	if notice == noticeUnknown {
		return ha.Notification{Title: i18n.T(lang, i18n.ApprovalUnknownTitle, args), Message: i18n.T(lang, i18n.ApprovalUnknownMessage, args), Tag: tag}
	}
	return ha.Notification{Title: i18n.T(lang, i18n.ApprovalInterruptedTitle, args), Message: i18n.T(lang, i18n.ApprovalInterruptedMessage, args), Tag: tag}
}

// Expire deletes requests that ended more than a day ago.
func (j *Journal) Expire(ctx context.Context) (int64, error) {
	var n int64
	err := j.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = expire(ctx, tx, j.now())
		return err
	})
	return n, err
}

func expire(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM approval_journal WHERE state = ? AND ended_at < ?`, stateEnded, format(now.Add(-keepEnded)))
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrJournal, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (j *Journal) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrJournal, err)
	}
	return nil
}

// format writes a time as the audit log does: UTC with milliseconds, so text order is
// time order.
func format(t time.Time) string {
	return t.UTC().Format(timeFormat)
}
