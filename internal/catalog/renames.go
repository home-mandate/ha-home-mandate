// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// maxFormerIDs bounds a chain of renames of one entity (a → b → c …).
const maxFormerIDs = 16

// Resolutions of a rename.
const (
	// ResolutionApplied: the rules on the former IDs were changed to the current one.
	ResolutionApplied = "applied"
	// ResolutionDismissed: a human decided that the rules on the former IDs no longer
	// apply to the renamed entity.
	ResolutionDismissed = "dismissed"
)

// Renames keeps the entity IDs Home Assistant renamed until a human resolves them. Rules
// name entities by ID; until then a rule on a former ID keeps applying to the renamed
// entity, and the stricter evaluation wins (fail closed). Renames are found by the
// rename event and, also after an outage, by comparing the registry IDs of Home
// Assistant with the entity IDs last seen for them.
type Renames struct {
	db *sql.DB

	mu sync.RWMutex
	// open maps an old ID to the new ID of a rename that is not resolved.
	open map[string]string
	// registry is the entity ID last seen per registry ID; nil until the first refresh
	// on a household that has none stored.
	registry map[string]string
	// unsaved are renames held in memory but not stored yet.
	unsaved []Rename
	// registryDirty: registry changed since it was stored.
	registryDirty bool
}

// LoadRenames reads the unresolved renames and the registry IDs from the database.
func LoadRenames(ctx context.Context, db *sql.DB) (*Renames, error) {
	r := &Renames{db: db, open: map[string]string{}}
	rows, err := db.QueryContext(ctx, `SELECT old_id, new_id FROM entity_renames WHERE resolved_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("catalog: read renames: %w", err)
	}
	for rows.Next() {
		var oldID, newID string
		if err := rows.Scan(&oldID, &newID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("catalog: read renames: %w", err)
		}
		r.open[oldID] = newID
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("catalog: read renames: %w", err)
	}
	rows, err = db.QueryContext(ctx, `SELECT registry_id, entity_id FROM entity_registry_ids`)
	if err != nil {
		return nil, fmt.Errorf("catalog: read registry IDs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var registryID, entityID string
		if err := rows.Scan(&registryID, &entityID); err != nil {
			return nil, fmt.Errorf("catalog: read registry IDs: %w", err)
		}
		if r.registry == nil {
			r.registry = map[string]string{}
		}
		r.registry[registryID] = entityID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: read registry IDs: %w", err)
	}
	return r, nil
}

// Hold takes a rename into effect in memory at once; Store stores it. It reports whether
// the rename is new.
func (r *Renames) Hold(rn Rename) bool {
	if !opaque(rn.Old) || !opaque(rn.New) || rn.Old == rn.New {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open[rn.Old] == rn.New {
		return false
	}
	// An entity that gets its former ID back is the same entity again: the rename that
	// took it away is undone, nothing is held.
	if r.open[rn.New] == rn.Old {
		delete(r.open, rn.New)
		r.unsaved = append(r.unsaved, rn)
		return true
	}
	r.open[rn.Old] = rn.New
	r.unsaved = append(r.unsaved, rn)
	return true
}

// Formers returns the former IDs of an entity whose renames are not resolved, nearest
// first.
func (r *Renames) Formers(entityID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.formersLocked(entityID)
}

func (r *Renames) formersLocked(entityID string) []string {
	var out []string
	current := entityID
	for len(out) < maxFormerIDs {
		found := ""
		for oldID, newID := range r.open {
			if newID == current && oldID != entityID && !slices.Contains(out, oldID) {
				found = oldID
				break
			}
		}
		if found == "" {
			break
		}
		out = append(out, found)
		current = found
	}
	return out
}

// Open lists the unresolved renames by current entity ID with its former IDs.
func (r *Renames) Open() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string][]string{}
	for _, newID := range r.open {
		if _, done := out[newID]; done {
			continue
		}
		// The current ID is the end of a chain: no open rename starts from it.
		if _, renamedAgain := r.open[newID]; renamedAgain {
			continue
		}
		out[newID] = r.formersLocked(newID)
	}
	return out
}

// Observe compares the entity registry with the entity IDs last seen per registry ID and
// returns the renames it finds. The first observation of a household only remembers.
// Entries without a registry ID (entities without a unique ID cannot be renamed) are
// skipped.
func (r *Renames) Observe(entries []ha.EntityEntry) []Rename {
	next := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.ID != "" && opaque(e.ID) && opaque(e.EntityID) {
			next[e.ID] = e.EntityID
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []Rename
	if r.registry != nil {
		for _, id := range slices.Sorted(maps.Keys(next)) {
			if before, ok := r.registry[id]; ok && before != next[id] {
				found = append(found, Rename{Old: before, New: next[id]})
			}
		}
	}
	if !maps.Equal(r.registry, next) {
		r.registry, r.registryDirty = next, true
	}
	return found
}

// Store writes the renames held since the last call and the registry IDs.
func (r *Renames) Store(ctx context.Context) error {
	r.mu.Lock()
	unsaved, registry, dirty := r.unsaved, maps.Clone(r.registry), r.registryDirty
	r.unsaved, r.registryDirty = nil, false
	r.mu.Unlock()
	err := r.store(ctx, unsaved, registry, dirty)
	if err != nil {
		r.mu.Lock() // try again with the next refresh
		r.unsaved = append(unsaved, r.unsaved...)
		r.registryDirty = r.registryDirty || dirty
		r.mu.Unlock()
	}
	return err
}

func (r *Renames) store(ctx context.Context, unsaved []Rename, registry map[string]string, dirty bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: store renames: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rn := range unsaved {
		// A rename back resolves the rename it undoes and is no rename of its own.
		res, err := tx.ExecContext(ctx, `UPDATE entity_renames SET resolved_at = ?, resolved_by = 'system', resolution = 'applied'
			WHERE old_id = ? AND new_id = ? AND resolved_at IS NULL`, now, rn.New, rn.Old)
		if err != nil {
			return fmt.Errorf("catalog: store renames: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_renames (old_id, new_id, seen_at) VALUES (?, ?, ?)
			ON CONFLICT (old_id, new_id) DO UPDATE SET seen_at = excluded.seen_at, resolved_at = NULL, resolved_by = NULL, resolution = NULL`,
			rn.Old, rn.New, now); err != nil {
			return fmt.Errorf("catalog: store renames: %w", err)
		}
	}
	if dirty {
		if _, err := tx.ExecContext(ctx, `DELETE FROM entity_registry_ids`); err != nil {
			return fmt.Errorf("catalog: store registry IDs: %w", err)
		}
		for id, entityID := range registry {
			if _, err := tx.ExecContext(ctx, `INSERT INTO entity_registry_ids (registry_id, entity_id) VALUES (?, ?)`, id, entityID); err != nil {
				return fmt.Errorf("catalog: store registry IDs: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog: store renames: %w", err)
	}
	return nil
}

// Resolve ends the renames that lead to entityID: from now on rules on its former IDs no
// longer apply to it. resolution says whether the rules were changed (applied) or a human
// decided to let them go (dismissed). It returns the former IDs it resolved.
func (r *Renames) Resolve(ctx context.Context, entityID, resolution, by string) ([]string, error) {
	if resolution != ResolutionApplied && resolution != ResolutionDismissed {
		return nil, errors.New("catalog: unknown resolution")
	}
	if by == "" {
		return nil, errors.New("catalog: no user")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	formers := r.formersLocked(entityID)
	if len(formers) == 0 {
		return nil, nil
	}
	// Renames only held in memory are stored first, so that resolving them sticks.
	if len(r.unsaved) > 0 || r.registryDirty {
		if err := r.store(ctx, r.unsaved, maps.Clone(r.registry), r.registryDirty); err != nil {
			return nil, err
		}
		r.unsaved, r.registryDirty = nil, false
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("catalog: resolve rename: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, oldID := range formers {
		if _, err := tx.ExecContext(ctx, `UPDATE entity_renames SET resolved_at = ?, resolved_by = ?, resolution = ?
			WHERE old_id = ? AND resolved_at IS NULL`, now, by, resolution, oldID); err != nil {
			return nil, fmt.Errorf("catalog: resolve rename: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("catalog: resolve rename: %w", err)
	}
	for _, oldID := range formers {
		delete(r.open, oldID)
	}
	return formers, nil
}
