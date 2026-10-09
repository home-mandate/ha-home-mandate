// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// maxEntityIDLength is the limit of a resource identifier (SPEC-v0 section 3.4).
const maxEntityIDLength = 255

// Marks are the entities the household marked as critical (SPEC-v0 section 4, step 5):
// every action on them except read is critical, whatever their category. They are part
// of the resource directory and are kept in the database and in memory.
type Marks struct {
	db *sql.DB
	// rec records every change in the audit log, in the same transaction; nil: none.
	rec Recorder

	// writeMu orders the writers; the transaction runs without mu, so that reading the
	// marks (every decision) never waits for the database.
	writeMu sync.Mutex
	mu      sync.RWMutex
	set     map[string]struct{}
}

// LoadMarks reads the marks from the database.
func LoadMarks(ctx context.Context, db *sql.DB) (*Marks, error) {
	rows, err := db.QueryContext(ctx, `SELECT entity_id FROM critical_entities`)
	if err != nil {
		return nil, fmt.Errorf("catalog: read critical entities: %w", err)
	}
	defer rows.Close()
	m := &Marks{db: db, set: map[string]struct{}{}}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("catalog: read critical entities: %w", err)
		}
		m.set[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: read critical entities: %w", err)
	}
	return m, nil
}

// Recorder writes audit entries within a transaction (audit.Log).
type Recorder interface {
	AppendTx(ctx context.Context, tx *sql.Tx, e audit.Entry) (int64, error)
}

// SetRecorder makes every change of a mark an audit entry (SPEC-v0 section 11.4).
func (m *Marks) SetRecorder(rec Recorder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rec = rec
}

// change runs query in a transaction and records entry with it if it changed a row; the
// caller holds writeMu, not mu. It reports whether a row changed.
func (m *Marks) change(ctx context.Context, entry audit.Entry, query string, args ...any) (bool, error) {
	m.mu.RLock()
	rec := m.rec
	m.mu.RUnlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("catalog: store critical entity: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("catalog: store critical entity: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("catalog: store critical entity: %w", err)
	}
	if n > 0 && rec != nil {
		if _, err := rec.AppendTx(ctx, tx, entry); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("catalog: store critical entity: %w", err)
	}
	return n > 0, nil
}

// markEntry is the audit entry of a change of the mark of entityID.
func markEntry(actor audit.Actor, change, entityID string) audit.Entry {
	return audit.Entry{Event: audit.EventDirectoryChanged, Actor: &actor, Directory: &audit.Directory{Change: change, EntityID: entityID}}
}

// Critical reports whether the entity is marked.
func (m *Marks) Critical(entityID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.set[entityID]
	return ok
}

// All returns the marked entities, sorted.
func (m *Marks) All() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.set))
	for id := range m.set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Set marks an entity as critical or removes the mark. The change takes effect only if
// it was stored. by is the user who made it.
func (m *Marks) Set(ctx context.Context, entityID string, critical bool, by string) error {
	if !opaque(entityID) {
		return errors.New("catalog: not a valid entity ID")
	}
	if by == "" {
		return errors.New("catalog: no user")
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	user := audit.Actor{Kind: audit.ActorUser, ID: by}
	if !critical {
		if _, err := m.change(ctx, markEntry(user, audit.DirectoryCriticalUnmarked, entityID),
			`DELETE FROM critical_entities WHERE entity_id = ?`, entityID); err != nil {
			return err
		}
		m.mu.Lock()
		delete(m.set, entityID)
		m.mu.Unlock()
		return nil
	}
	if _, err := m.change(ctx, markEntry(user, audit.DirectoryCriticalMarked, entityID), `INSERT INTO critical_entities
		(entity_id, marked_at, marked_by) VALUES (?, ?, ?) ON CONFLICT (entity_id) DO NOTHING`,
		entityID, time.Now().UTC().Format(time.RFC3339), by); err != nil {
		return err
	}
	m.mu.Lock()
	m.set[entityID] = struct{}{}
	m.mu.Unlock()
	return nil
}

// Hold marks the new ID of a renamed entity in memory if its old ID is marked, at once
// and without waiting for the database, so that the renamed entity is never without its
// mark (it runs when the rename event arrives). Carry stores it. The old ID keeps its
// mark: an entity that takes it later is protected too. It reports whether it marked newID.
func (m *Marks) Hold(oldID, newID string) bool {
	if !opaque(newID) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.set[oldID]; !ok {
		return false
	}
	if _, ok := m.set[newID]; ok {
		return false
	}
	m.set[newID] = struct{}{}
	return true
}

// Carry stores the mark of the new ID of a renamed entity if its old ID is marked (see
// Hold). It reports whether the database had no mark for newID before.
func (m *Marks) Carry(ctx context.Context, oldID, newID string) (bool, error) {
	if !opaque(newID) {
		return false, errors.New("catalog: not a valid entity ID")
	}
	if !m.Critical(oldID) {
		return false, nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	changed, err := m.change(ctx, markEntry(audit.Actor{Kind: audit.ActorSystem, ID: systemActor}, audit.DirectoryCriticalMarked, newID),
		`INSERT INTO critical_entities (entity_id, marked_at, marked_by) VALUES (?, ?, ?) ON CONFLICT (entity_id) DO NOTHING`,
		newID, time.Now().UTC().Format(time.RFC3339), systemActor)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	m.set[newID] = struct{}{}
	m.mu.Unlock()
	return changed, nil
}

// systemActor is marked_by for marks Home-Mandate sets itself.
const systemActor = "system"

// opaque reports whether s is a resource identifier: printable ASCII without space.
func opaque(s string) bool {
	if s == "" || len(s) > maxEntityIDLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
