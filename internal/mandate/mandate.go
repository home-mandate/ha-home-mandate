// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mandate stores and versions mandates. A mandate is accepted only if the
// reference evaluator of the specification accepts it (SPEC-v0 section 3.1), it belongs to
// this household and to an active agent. Every version is kept unchanged; changes are
// written to the audit log in the same transaction, by digest only.
package mandate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

var (
	// ErrInvalid means the mandate is invalid or does not belong to this household or to
	// an active agent.
	ErrInvalid = errors.New("mandate: invalid")
	// ErrNotFound means there is no such mandate.
	ErrNotFound = errors.New("mandate: not found")
	// ErrConflict means the change conflicts with stored mandates: a second mandate for
	// an agent, a mandate moved to another agent, a change to a revoked mandate, or an
	// edit that started from a version that is no longer the current one.
	ErrConflict = errors.New("mandate: conflict")
	// ErrCriticalConfirmation means the new version lets a rule allow critical actions
	// without approval that the current version does not have in this form, and the
	// separate confirmation for that is missing.
	ErrCriticalConfirmation = errors.New("mandate: allow_critical needs the separate confirmation")
)

// Mandate statuses.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

const timeFormat = time.RFC3339Nano

// Info describes the current version of a mandate.
type Info struct {
	ID string
	// Name is the display name (decision D2): metadata next to the document, so that
	// renaming changes neither the document nor its digest. Empty for mandates stored
	// before names existed and for imports; the UI then shows the ID.
	Name              string
	ClientID          string
	Status            string
	Digest            string
	MaxActionsPerHour int
	UpdatedAt         time.Time
	// version is the version the document carries (SPEC-v0 section 3.5); 0 without.
	version int64
}

// Version is one stored version of a mandate. Versions are told apart by Number, which
// counts from 1 for the oldest: the digest is a hash of the content, and before mandates
// carried a version (SPEC-v0 section 3.5) a version that restored an earlier one repeated
// its digest. Number counts the stored rows; the version inside the document can be
// higher (a document offered with its own, later version).
type Version struct {
	Number    int
	Digest    string
	CreatedAt time.Time
	CreatedBy string
}

// Loaded is the current version of an agent's mandate, ready for evaluation.
type Loaded struct {
	Info    Info
	Mandate *evaluator.Mandate
	Status  evaluator.MandateStatus
}

// Store manages the mandates of one household.
type Store struct {
	db        *sql.DB
	log       *audit.Log
	principal string
	// issuer is this installation (SPEC-v0 section 3.5): every version it stores carries
	// it, with a version higher than every earlier one of the mandate.
	issuer string

	mu     sync.Mutex
	parsed map[string]*evaluator.Mandate // by digest; versions never change
	stored map[string]cached             // active candidates by digest and content
}

// New returns the Store for principal; issuer is the URI of this installation.
func New(db *sql.DB, log *audit.Log, principal, issuer string) *Store {
	return &Store{db: db, log: log, principal: principal, issuer: issuer, parsed: map[string]*evaluator.Mandate{},
		stored: map[string]cached{}}
}

// meta holds the fields the evaluator does not expose.
type meta struct {
	Principal string `json:"principal"`
	Agent     struct {
		ClientID string `json:"client_id"`
	} `json:"agent"`
	Limits struct {
		MaxActionsPerHour int `json:"max_actions_per_hour"`
	} `json:"limits"`
}

// Put stores document as a new mandate or as a new version of an existing one.
// Unchanged content (same digest) creates no version.
func (s *Store) Put(ctx context.Context, document []byte, by audit.Actor) (Info, error) {
	var info Info
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		info, err = s.PutTx(ctx, tx, document, by)
		return err
	})
	if err != nil {
		return Info{}, err
	}
	return s.Get(ctx, info.ID)
}

// PutTx is Put inside tx, so that admitting an agent and storing its mandate commit
// together. It returns the stored information as written.
func (s *Store) PutTx(ctx context.Context, tx *sql.Tx, document []byte, by audit.Actor) (Info, error) {
	info, err := s.check(document)
	if err != nil {
		return Info{}, err
	}
	if err := s.put(ctx, tx, info, document, by); err != nil {
		return Info{}, err
	}
	return info, nil
}

// check accepts document only if the reference evaluator accepts it and it belongs to
// this household; it returns what would be stored.
func (s *Store) check(document []byte) (Info, error) {
	m, err := evaluator.Parse(document)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var md meta
	if err := json.Unmarshal(document, &md); err != nil {
		return Info{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if md.Principal != s.principal {
		return Info{}, fmt.Errorf("%w: principal is not this household", ErrInvalid)
	}
	if err := placeholderApprover(document); err != nil {
		return Info{}, err
	}
	// Mandates issued elsewhere (signed ones, SPEC-v0 section 7) cannot be imported yet.
	if m.Issuer() != "" && m.Issuer() != s.issuer {
		return Info{}, fmt.Errorf("%w: issued by %q, not by this installation", ErrInvalid, m.Issuer())
	}
	return Info{ID: m.ID(), ClientID: md.Agent.ClientID, Status: StatusActive, Digest: m.Digest(),
		MaxActionsPerHour: md.Limits.MaxActionsPerHour, UpdatedAt: time.Now().UTC(), version: m.Version()}, nil
}

// issue makes document the next version of a mandate (SPEC-v0 section 3.5): current is
// its current version (nil for a new mandate) with digest currentDigest, highest the
// highest version issued for it so far, also before a revocation. A document without
// version gets this installation as issuer and the version after highest, unless it has
// the content of the current version: then it is that version, unchanged. A document
// with a version (check accepts only this issuer) must come after highest: an older
// version is never accepted again.
func (s *Store) issue(document []byte, current *evaluator.Mandate, currentDigest string, highest int64) ([]byte, Info, error) {
	offered, err := evaluator.Parse(document)
	if err != nil {
		return nil, Info{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if offered.Version() != 0 {
		if current != nil && offered.Digest() == currentDigest {
			info, err := s.check(document) // the current version itself, offered again: unchanged
			return document, info, err
		}
		if offered.Version() <= highest {
			return nil, Info{}, fmt.Errorf("%w: version %d does not follow version %d", ErrConflict, offered.Version(), highest)
		}
	} else {
		if current != nil {
			// The content of the current version, offered again without its version.
			same, err := withVersion(document, s.issuer, current.Version())
			if err != nil {
				return nil, Info{}, err
			}
			if m, err := evaluator.Parse(same); err == nil && m.Digest() == currentDigest {
				info, err := s.check(same)
				return same, info, err
			}
		}
		if document, err = withVersion(document, s.issuer, highest+1); err != nil {
			return nil, Info{}, err
		}
	}
	info, err := s.check(document)
	if err != nil {
		return nil, Info{}, err
	}
	if current != nil {
		next, err := evaluator.Parse(document)
		if err != nil {
			return nil, Info{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if err := evaluator.CheckSuccessor(current, next); err != nil {
			return nil, Info{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	return document, info, nil
}

// withVersion returns document with issuer and version set; version 0 removes both
// (a mandate stored before versions existed).
func withVersion(document []byte, issuer string, version int64) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	delete(fields, "issuer")
	delete(fields, "version")
	if version > 0 {
		fields["issuer"], _ = json.Marshal(issuer)
		fields["version"], _ = json.Marshal(version)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("mandate: issue: %w", err)
	}
	return out, nil
}

func (s *Store) put(ctx context.Context, tx *sql.Tx, info Info, document []byte, by audit.Actor) error {
	var agentStatus string
	err := tx.QueryRowContext(ctx, `SELECT status FROM agents WHERE client_id = ?`, info.ClientID).Scan(&agentStatus)
	if errors.Is(err, sql.ErrNoRows) || err == nil && agentStatus != StatusActive {
		return fmt.Errorf("%w: agent unknown or revoked", ErrInvalid)
	}
	if err != nil {
		return fmt.Errorf("mandate: read agent: %w", err)
	}
	// One active mandate per agent: another active one for the agent is a conflict, so is
	// a document that moves a mandate to another agent or changes a revoked one.
	var other string
	err = tx.QueryRowContext(ctx, `SELECT id FROM mandates WHERE client_id = ? AND status = ? AND id <> ?`,
		info.ClientID, StatusActive, info.ID).Scan(&other)
	switch {
	case err == nil:
		return fmt.Errorf("%w: agent already has the active mandate %s", ErrConflict, other)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("mandate: read: %w", err)
	}
	var existing struct {
		clientID, status, digest string
		document                 sql.NullString // NULL: the row of the current version is missing
		highest                  int64
	}
	err = tx.QueryRowContext(ctx, `SELECT m.client_id, m.status, m.current_digest, m.highest_version, v.document FROM mandates m
		LEFT JOIN mandate_versions v ON v.mandate_id = m.id AND v.digest = m.current_digest
		WHERE m.id = ? ORDER BY v.version DESC LIMIT 1`, info.ID).
		Scan(&existing.clientID, &existing.status, &existing.digest, &existing.highest, &existing.document)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		document, info, err = s.issue(document, nil, "", 0)
		if err != nil {
			return err
		}
		return s.create(ctx, tx, info, document, by)
	case err != nil:
		return fmt.Errorf("mandate: read: %w", err)
	case existing.clientID != info.ClientID || existing.status != StatusActive:
		return fmt.Errorf("%w: mandate %s belongs to another agent or is revoked", ErrConflict, info.ID)
	}
	// A current version that no longer parses (the specification became stricter) is
	// replaced like a new one; the highest version still rules out older ones.
	var current *evaluator.Mandate
	if existing.document.Valid {
		if m, err := s.parse(existing.digest, existing.document.String); err == nil {
			current = m
		}
	}
	document, info, err = s.issue(document, current, existing.digest, existing.highest)
	if err != nil || info.Digest == existing.digest {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mandates SET current_digest = ?, max_actions_per_hour = ?, updated_at = ?,
		highest_version = max(highest_version, ?) WHERE id = ?`,
		info.Digest, info.MaxActionsPerHour, info.UpdatedAt.Format(timeFormat), info.version, info.ID); err != nil {
		return fmt.Errorf("mandate: update: %w", err)
	}
	return s.addVersion(ctx, tx, info, document, by, audit.EventMandateUpdated, existing.digest)
}

func (s *Store) create(ctx context.Context, tx *sql.Tx, info Info, document []byte, by audit.Actor) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at, highest_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, info.ID, info.ClientID, StatusActive, info.Digest, info.MaxActionsPerHour,
		info.UpdatedAt.Format(timeFormat), info.UpdatedAt.Format(timeFormat), info.version); err != nil {
		return fmt.Errorf("mandate: insert: %w", err)
	}
	return s.addVersion(ctx, tx, info, document, by, audit.EventMandateCreated, "")
}

func (s *Store) addVersion(ctx context.Context, tx *sql.Tx, info Info, document []byte, by audit.Actor, event, previous string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO mandate_versions (mandate_id, digest, document, created_at, created_by) VALUES (?, ?, ?, ?, ?)`,
		info.ID, info.Digest, string(document), info.UpdatedAt.Format(timeFormat), by.ID); err != nil {
		return fmt.Errorf("mandate: insert version: %w", err)
	}
	_, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: event, Actor: &by,
		Mandate: &audit.Mandate{ID: info.ID, Digest: info.Digest, PreviousDigest: previous}})
	return err
}

// Revoke revokes a mandate; every later evaluation is deny with reason revoked.
// Revoking a revoked mandate is a no-op.
func (s *Store) Revoke(ctx context.Context, id string, by audit.Actor) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		return s.revokeTx(ctx, tx, `id = ?`, id, by, true)
	})
}

// RevokeAgentTx revokes the mandate of an agent inside tx, if it has an active one, so
// that revoking an agent ends its mandate in the same transaction.
func (s *Store) RevokeAgentTx(ctx context.Context, tx *sql.Tx, clientID string, by audit.Actor) error {
	return s.revokeTx(ctx, tx, `client_id = ? AND status = 'active'`, clientID, by, false)
}

func (s *Store) revokeTx(ctx context.Context, tx *sql.Tx, where, key string, by audit.Actor, mustExist bool) error {
	var id, status, digest string
	err := tx.QueryRowContext(ctx, `SELECT id, status, current_digest FROM mandates WHERE `+where, key).Scan(&id, &status, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		if mustExist {
			return ErrNotFound
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("mandate: read: %w", err)
	}
	if status == StatusRevoked {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mandates SET status = ?, updated_at = ? WHERE id = ?`,
		StatusRevoked, time.Now().UTC().Format(timeFormat), id); err != nil {
		return fmt.Errorf("mandate: revoke: %w", err)
	}
	_, err = s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventMandateRevoked, Actor: &by,
		Mandate: &audit.Mandate{ID: id, Digest: digest}})
	return err
}

// SetNameTx sets the display name of a mandate inside tx. It stores no version and
// writes no audit entry: the name is not part of the mandate.
func (s *Store) SetNameTx(ctx context.Context, tx *sql.Tx, id, name string) error {
	res, err := tx.ExecContext(ctx, `UPDATE mandates SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("mandate: rename: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetName is SetNameTx in its own transaction.
func (s *Store) SetName(ctx context.Context, id, name string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return s.SetNameTx(ctx, tx, id, name) })
}

// ForAgent returns the current mandate of an agent for evaluation: the active one, or
// the newest revoked one (which denies everything).
func (s *Store) ForAgent(ctx context.Context, clientID string) (Loaded, error) {
	var info Info
	var updatedAt, document, agentStatus string
	err := s.db.QueryRowContext(ctx, `SELECT m.id, m.client_id, m.status, m.current_digest, m.max_actions_per_hour, m.updated_at, v.document, a.status
		FROM mandates m JOIN mandate_versions v ON v.mandate_id = m.id AND v.digest = m.current_digest
		JOIN agents a ON a.client_id = m.client_id
		WHERE m.client_id = ? ORDER BY m.status = 'active' DESC, m.rowid DESC, v.version DESC LIMIT 1`, clientID).
		Scan(&info.ID, &info.ClientID, &info.Status, &info.Digest, &info.MaxActionsPerHour, &updatedAt, &document, &agentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return Loaded{}, ErrNotFound
	}
	if err != nil {
		return Loaded{}, fmt.Errorf("mandate: read: %w", err)
	}
	info.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	m, err := s.parse(info.Digest, document)
	if err != nil {
		return Loaded{}, err
	}
	// A revoked agent revokes its mandate for every evaluation, also over the PDP endpoint.
	status := evaluator.StatusActive
	if info.Status != StatusActive || agentStatus != StatusActive {
		status = evaluator.StatusRevoked
	}
	return Loaded{Info: info, Mandate: m, Status: status}, nil
}

// Candidate is a stored mandate that the selection of SPEC-v0 section 4.3 considers.
type Candidate struct {
	Info   Info
	Stored evaluator.Stored
	// Entities are the entity IDs its rules name.
	Entities map[string]bool
}

// cached is a parsed active version.
type cached struct {
	stored   evaluator.Stored
	entities map[string]bool
}

// Candidates returns the mandates of an agent that the selection considers: the active
// ones, and only while the agent is active. A revoked mandate and the mandates of a
// revoked agent are no candidates (SPEC-v0 section 4.3, step 1), so the agent then has
// no mandate. A current version that no longer parses, or whose content no longer has
// its digest, stays a candidate and denies with invalid_mandate.
func (s *Store) Candidates(ctx context.Context, clientID string) ([]Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.client_id, m.status, m.current_digest, m.max_actions_per_hour, m.updated_at, v.document
		FROM mandates m JOIN agents a ON a.client_id = m.client_id
		LEFT JOIN mandate_versions v ON v.mandate_id = m.id AND v.version = (SELECT max(version) FROM mandate_versions
			WHERE mandate_id = m.id AND digest = m.current_digest)
		WHERE m.client_id = ? AND m.status = 'active' AND a.status = 'active' ORDER BY m.rowid`, clientID)
	if err != nil {
		return nil, fmt.Errorf("mandate: read: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var updatedAt string
		var document sql.NullString // NULL: the row of the current version is missing
		if err := rows.Scan(&c.Info.ID, &c.Info.ClientID, &c.Info.Status, &c.Info.Digest, &c.Info.MaxActionsPerHour, &updatedAt, &document); err != nil {
			return nil, fmt.Errorf("mandate: read: %w", err)
		}
		c.Info.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		if document.Valid {
			c.Stored, c.Entities = s.candidate(c.Info.Digest, document.String)
		} else {
			c.Stored = evaluator.NewStored(nil, evaluator.StatusActive) // denies with invalid_mandate
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// candidate parses an active stored version once. A version whose content does not have
// the digest it was stored with was changed outside Home-Mandate: it is no valid mandate.
func (s *Store) candidate(digest, document string) (evaluator.Stored, map[string]bool) {
	// The key covers the stored text too: a row changed after it was first read is checked anew.
	sum := sha256.Sum256([]byte(document))
	key := digest + " " + hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.stored[key]; ok {
		return c.stored, c.entities
	}
	if m, err := evaluator.Parse([]byte(document)); err == nil && m.Digest() != digest {
		return evaluator.NewStored(nil, evaluator.StatusActive), nil // not cached: the row may be repaired
	}
	c := cached{stored: evaluator.NewStored([]byte(document), evaluator.StatusActive), entities: NamedEntities([]byte(document))}
	s.stored[key] = c
	return c.stored, c.entities
}

// Named returns a function that tells whether a rule of an active mandate names an
// entity ID.
func (s *Store) Named(ctx context.Context) (func(entityID string) bool, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	named := map[string]bool{}
	for _, info := range list {
		if info.Status != StatusActive {
			continue
		}
		_, doc, err := s.Current(ctx, info.ID)
		if err != nil {
			return nil, err
		}
		for id := range NamedEntities(doc) {
			named[id] = true
		}
	}
	return func(entityID string) bool { return named[entityID] }, nil
}

// NamedEntities returns the entity IDs the rules of a document name.
func NamedEntities(document []byte) map[string]bool {
	var doc struct {
		Rules []struct {
			Resource struct {
				EntityID string `json:"entity_id"`
			} `json:"resource"`
		} `json:"rules"`
	}
	if json.Unmarshal(document, &doc) != nil {
		return nil
	}
	out := map[string]bool{}
	for _, r := range doc.Rules {
		if r.Resource.EntityID != "" {
			out[r.Resource.EntityID] = true
		}
	}
	return out
}

// parse parses a stored version once; a version that no longer parses (e.g. after an
// evaluator update) is an error, so the request is denied.
func (s *Store) parse(digest, document string) (*evaluator.Mandate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.parsed[digest]; ok {
		return m, nil
	}
	m, err := evaluator.Parse([]byte(document))
	if err != nil || m.Digest() != digest {
		return nil, fmt.Errorf("%w: stored version %s no longer parses", ErrInvalid, digest)
	}
	s.parsed[digest] = m
	return m, nil
}

// Get returns the current version of a mandate.
func (s *Store) Get(ctx context.Context, id string) (Info, error) {
	list, err := s.query(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Info{}, err
	}
	if len(list) == 0 {
		return Info{}, ErrNotFound
	}
	return list[0], nil
}

// List returns all mandates in creation order.
func (s *Store) List(ctx context.Context) ([]Info, error) {
	return s.query(ctx, ``)
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Info, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, client_id, status, current_digest, max_actions_per_hour, updated_at FROM mandates `+
		where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, fmt.Errorf("mandate: query: %w", err)
	}
	defer rows.Close()
	var list []Info
	for rows.Next() {
		var i Info
		var updatedAt string
		if err := rows.Scan(&i.ID, &i.Name, &i.ClientID, &i.Status, &i.Digest, &i.MaxActionsPerHour, &updatedAt); err != nil {
			return nil, fmt.Errorf("mandate: query: %w", err)
		}
		i.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		list = append(list, i)
	}
	return list, rows.Err()
}

// Current returns the current version of a mandate with its document.
func (s *Store) Current(ctx context.Context, id string) (Info, []byte, error) {
	info, err := s.Get(ctx, id)
	if err != nil {
		return Info{}, nil, err
	}
	var document string
	err = s.db.QueryRowContext(ctx, `SELECT document FROM mandate_versions WHERE mandate_id = ? AND digest = ? ORDER BY version DESC LIMIT 1`,
		id, info.Digest).Scan(&document)
	if err != nil {
		return Info{}, nil, fmt.Errorf("mandate: current version of %s: %w", id, err)
	}
	return info, []byte(document), nil
}

// Versions returns all versions of a mandate, oldest first, numbered from 1.
func (s *Store) Versions(ctx context.Context, id string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT digest, created_at, created_by FROM mandate_versions WHERE mandate_id = ? ORDER BY version`, id)
	if err != nil {
		return nil, fmt.Errorf("mandate: versions: %w", err)
	}
	defer rows.Close()
	var list []Version
	for rows.Next() {
		var v Version
		var createdAt string
		if err := rows.Scan(&v.Digest, &createdAt, &v.CreatedBy); err != nil {
			return nil, fmt.Errorf("mandate: versions: %w", err)
		}
		v.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		v.Number = len(list) + 1
		list = append(list, v)
	}
	return list, rows.Err()
}

// VersionDocument returns the stored document of version number of a mandate.
func (s *Store) VersionDocument(ctx context.Context, id string, number int) ([]byte, Version, error) {
	if number < 1 {
		return nil, Version{}, ErrNotFound
	}
	v := Version{Number: number}
	var document, createdAt string
	// Versions are never deleted, so the position in creation order is the number.
	err := s.db.QueryRowContext(ctx, `SELECT digest, document, created_at, created_by FROM mandate_versions
		WHERE mandate_id = ? ORDER BY version LIMIT 1 OFFSET ?`, id, number-1).Scan(&v.Digest, &document, &createdAt, &v.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Version{}, ErrNotFound
	}
	if err != nil {
		return nil, Version{}, fmt.Errorf("mandate: version: %w", err)
	}
	v.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	return []byte(document), v, nil
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mandate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mandate: commit: %w", err)
	}
	return nil
}

// Invalid is a stored mandate that the evaluator does not accept.
type Invalid struct {
	ID       string
	ClientID string
	Status   string
	// Problem says in one line why the current version is not a valid mandate.
	Problem string
}

// Invalid lists the mandates whose current version is not a valid mandate, for example
// because the specification became stricter since it was stored. Such a mandate denies
// every request until a human stores a new version.
func (s *Store) Invalid(ctx context.Context) ([]Invalid, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Invalid
	for _, info := range list {
		_, document, err := s.Current(ctx, info.ID)
		if err != nil {
			return nil, err
		}
		if _, err := evaluator.Parse(document); err != nil {
			out = append(out, Invalid{ID: info.ID, ClientID: info.ClientID, Status: info.Status, Problem: firstLine(err)})
		}
	}
	return out, nil
}

// firstLine keeps the summary of a validation error; the schema validator appends the
// path of every violated keyword on further lines.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
