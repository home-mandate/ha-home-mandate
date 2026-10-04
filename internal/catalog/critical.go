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
)

// maxEntityIDLength is the limit of a resource identifier (SPEC-v0 section 3.4).
const maxEntityIDLength = 255

// Marks are the entities the household marked as critical (SPEC-v0 section 4, step 5):
// every action on them except read is critical, whatever their category. They are part
// of the resource directory and are kept in the database and in memory.
type Marks struct {
	db *sql.DB

	mu  sync.RWMutex
	set map[string]struct{}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if !critical {
		if _, err := m.db.ExecContext(ctx, `DELETE FROM critical_entities WHERE entity_id = ?`, entityID); err != nil {
			return fmt.Errorf("catalog: store critical entity: %w", err)
		}
		delete(m.set, entityID)
		return nil
	}
	if _, err := m.db.ExecContext(ctx, `INSERT INTO critical_entities (entity_id, marked_at, marked_by) VALUES (?, ?, ?)
		ON CONFLICT (entity_id) DO NOTHING`, entityID, time.Now().UTC().Format(time.RFC3339), by); err != nil {
		return fmt.Errorf("catalog: store critical entity: %w", err)
	}
	m.set[entityID] = struct{}{}
	return nil
}

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
