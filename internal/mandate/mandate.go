// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mandate stores and versions mandates. A mandate is accepted only if the
// reference evaluator of mandate-spec accepts it (SPEC-v0 section 3.1), it belongs to
// this household and to an active agent. Every version is kept unchanged; changes are
// written to the audit log in the same transaction, by digest only.
package mandate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/audit"
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
	ID                string
	ClientID          string
	Status            string
	Digest            string
	MaxActionsPerHour int
	UpdatedAt         time.Time
}

// Version is one stored version of a mandate. Versions are told apart by Number, which
// counts from 1 for the oldest: the digest is a hash of the content, and a version that
// restores an earlier one repeats its digest.
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

	mu     sync.Mutex
	parsed map[string]*evaluator.Mandate // by digest; versions never change
}

// New returns the Store for principal.
func New(db *sql.DB, log *audit.Log, principal string) *Store {
	return &Store{db: db, log: log, principal: principal, parsed: map[string]*evaluator.Mandate{}}
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
	return Info{ID: m.ID(), ClientID: md.Agent.ClientID, Status: StatusActive, Digest: m.Digest(),
		MaxActionsPerHour: md.Limits.MaxActionsPerHour, UpdatedAt: time.Now().UTC()}, nil
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
	var existing struct{ id, clientID, status, digest string }
	err = tx.QueryRowContext(ctx, `SELECT id, client_id, status, current_digest FROM mandates WHERE id = ? OR client_id = ?`,
		info.ID, info.ClientID).Scan(&existing.id, &existing.clientID, &existing.status, &existing.digest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s.create(ctx, tx, info, document, by)
	case err != nil:
		return fmt.Errorf("mandate: read: %w", err)
	case existing.id != info.ID || existing.clientID != info.ClientID || existing.status != StatusActive:
		return fmt.Errorf("%w: agent already has mandate %s, or the mandate belongs to another agent or is revoked", ErrConflict, existing.id)
	case existing.digest == info.Digest:
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mandates SET current_digest = ?, max_actions_per_hour = ?, updated_at = ? WHERE id = ?`,
		info.Digest, info.MaxActionsPerHour, info.UpdatedAt.Format(timeFormat), info.ID); err != nil {
		return fmt.Errorf("mandate: update: %w", err)
	}
	return s.addVersion(ctx, tx, info, document, by, audit.EventMandateUpdated, existing.digest)
}

func (s *Store) create(ctx context.Context, tx *sql.Tx, info Info, document []byte, by audit.Actor) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, info.ID, info.ClientID, StatusActive, info.Digest, info.MaxActionsPerHour,
		info.UpdatedAt.Format(timeFormat), info.UpdatedAt.Format(timeFormat)); err != nil {
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
		var status, digest string
		err := tx.QueryRowContext(ctx, `SELECT status, current_digest FROM mandates WHERE id = ?`, id).Scan(&status, &digest)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
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
	})
}

// ForAgent returns the current mandate of an agent for evaluation.
func (s *Store) ForAgent(ctx context.Context, clientID string) (Loaded, error) {
	var info Info
	var updatedAt, document, agentStatus string
	err := s.db.QueryRowContext(ctx, `SELECT m.id, m.client_id, m.status, m.current_digest, m.max_actions_per_hour, m.updated_at, v.document, a.status
		FROM mandates m JOIN mandate_versions v ON v.mandate_id = m.id AND v.digest = m.current_digest
		JOIN agents a ON a.client_id = m.client_id
		WHERE m.client_id = ? ORDER BY v.version DESC LIMIT 1`, clientID).
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, client_id, status, current_digest, max_actions_per_hour, updated_at FROM mandates `+
		where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, fmt.Errorf("mandate: query: %w", err)
	}
	defer rows.Close()
	var list []Info
	for rows.Next() {
		var i Info
		var updatedAt string
		if err := rows.Scan(&i.ID, &i.ClientID, &i.Status, &i.Digest, &i.MaxActionsPerHour, &updatedAt); err != nil {
			return nil, fmt.Errorf("mandate: query: %w", err)
		}
		i.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		list = append(list, i)
	}
	return list, rows.Err()
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
