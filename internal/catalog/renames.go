// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/ha"
)

// Resolutions of a rename.
const (
	// ResolutionApplied: the rules on the former IDs were changed to the current one.
	ResolutionApplied = "applied"
	// ResolutionDismissed: a human decided that the rules on the former IDs no longer
	// apply to the renamed entity.
	ResolutionDismissed = "dismissed"
)

// ErrRenamesChanged means the former IDs of an entity are not the ones the caller saw:
// another rename arrived meanwhile, nothing was resolved.
var ErrRenamesChanged = errors.New("catalog: the renames changed meanwhile")

// edge is one rename that is not resolved.
type edge struct {
	old, new string
}

// held is a rename held in memory and not stored yet; undo: it undoes the stored rename
// in the other direction instead of being one of its own.
type held struct {
	Rename
	undo bool
}

// Renames keeps the entity IDs Home Assistant renamed until a human resolves them. Rules
// name entities by ID; until then a rule on a former ID keeps applying to the renamed
// entity, and the stricter evaluation wins (fail closed). Renames are found by the
// rename event and, also after an outage, by comparing the registry IDs of Home
// Assistant with the entity IDs last seen for them.
//
// The renames form a graph: an entity can have several former IDs (renamed twice, or
// two entities merged into one ID), IDs can be swapped, and a chain can lead back to
// where it started. Former IDs are found by walking it backwards.
type Renames struct {
	db *sql.DB
	// rec records every rename found and resolved in the audit log, in the same
	// transaction; nil: none.
	rec Recorder
	// storeMu orders Store and Resolve, so that a rename is never written as open after
	// it was resolved. It is taken before mu.
	storeMu sync.Mutex

	mu sync.RWMutex
	// open are the unresolved renames, with the registry ID they were found by ("" for
	// one from the rename event).
	open map[edge]string
	// registry is the entity ID last seen per registry ID; nil until the first refresh
	// on a household that has none stored.
	registry      map[string]string
	registryDirty bool
	unsaved       []held
}

// LoadRenames reads the unresolved renames and the registry IDs from the database.
func LoadRenames(ctx context.Context, db *sql.DB) (*Renames, error) {
	r := &Renames{db: db, open: map[edge]string{}}
	rows, err := db.QueryContext(ctx, `SELECT old_id, new_id FROM entity_renames WHERE resolved_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("catalog: read renames: %w", err)
	}
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.old, &e.new); err != nil {
			rows.Close()
			return nil, fmt.Errorf("catalog: read renames: %w", err)
		}
		r.open[e] = ""
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

// SetRecorder makes every rename found and resolved an audit entry (SPEC-v0 section 11.4).
func (r *Renames) SetRecorder(rec Recorder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec = rec
}

// renameEntry is the audit entry of a rename change of entityID with its former ID.
func renameEntry(actor audit.Actor, change, entityID, former string) audit.Entry {
	return audit.Entry{Event: audit.EventDirectoryChanged, Actor: &actor,
		Directory: &audit.Directory{Change: change, EntityID: entityID, PreviousEntityID: former}}
}

// Hold takes a rename into effect in memory at once; Store stores it. It reports whether
// anything changed.
func (r *Renames) Hold(rn Rename) bool {
	if !opaque(rn.Old) || !opaque(rn.New) || rn.Old == rn.New {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := edge{rn.Old, rn.New}
	if registry, ok := r.open[e]; ok {
		if registry == "" && rn.Registry != "" {
			r.open[e] = rn.Registry
		}
		return false
	}
	// The same entity (same registry ID) gets its former ID back: the rename is undone.
	// Two entities that swap their IDs are two renames, never an undo.
	back := edge{rn.New, rn.Old}
	if registry, ok := r.open[back]; ok && rn.Registry != "" && registry == rn.Registry {
		delete(r.open, back)
		r.unsaved = append(r.unsaved, held{rn, true})
		return true
	}
	r.open[e] = rn.Registry
	r.unsaved = append(r.unsaved, held{Rename: rn})
	return true
}

// Formers returns the former IDs of an entity whose renames are not resolved: every ID
// from which an unresolved rename leads to it, nearest first.
func (r *Renames) Formers(entityID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return formersOf(r.open, entityID)
}

func formersOf(open map[edge]string, entityID string) []string {
	var out []string
	seen := map[string]bool{entityID: true}
	queue := []string{entityID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		var found []string
		for e := range open {
			if e.new == current && !seen[e.old] {
				found = append(found, e.old)
			}
		}
		slices.Sort(found)
		for _, id := range found {
			seen[id] = true
			out = append(out, id)
			queue = append(queue, id)
		}
	}
	return out
}

// Open lists every ID that unresolved renames lead to, with its former IDs. Which of them
// are entities now (not only a step in between) is for the caller to tell.
func (r *Renames) Open() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string][]string{}
	for e := range r.open {
		if _, done := out[e.new]; !done {
			out[e.new] = formersOf(r.open, e.new)
		}
	}
	return out
}

// Observe compares the entity registry with the entity IDs last seen per registry ID and
// returns the renames it finds, each with its registry ID. The first observation of a
// household only remembers. A registry ID missing from one listing is kept, so that a
// rename across that gap is still found. Entries without a registry ID (entities
// without a unique ID cannot be renamed) are skipped.
func (r *Renames) Observe(entries []ha.EntityEntry) []Rename {
	r.mu.Lock()
	defer r.mu.Unlock()
	first := r.registry == nil
	next := maps.Clone(r.registry)
	if next == nil {
		next = map[string]string{}
	}
	var found []Rename
	for _, e := range entries {
		if e.ID == "" || !opaque(e.ID) || !opaque(e.EntityID) {
			continue
		}
		if before, ok := next[e.ID]; ok && !first && before != e.EntityID {
			found = append(found, Rename{Old: before, New: e.EntityID, Registry: e.ID})
		}
		next[e.ID] = e.EntityID
	}
	slices.SortFunc(found, func(a, b Rename) int { return strings.Compare(a.Registry, b.Registry) })
	if !maps.Equal(r.registry, next) {
		r.registry, r.registryDirty = next, true
	}
	return found
}

// Store writes the renames held since the last call and the registry IDs. What cannot
// be written stays in force in memory and is written by the next call.
func (r *Renames) Store(ctx context.Context) error {
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	return r.flush(ctx)
}

// flush writes what is held; the caller holds storeMu.
func (r *Renames) flush(ctx context.Context) error {
	r.mu.Lock()
	unsaved, registry, dirty, rec := r.unsaved, maps.Clone(r.registry), r.registryDirty, r.rec
	r.unsaved, r.registryDirty = nil, false
	r.mu.Unlock()
	if len(unsaved) == 0 && !dirty {
		return nil
	}
	if err := r.store(ctx, rec, unsaved, registry, dirty); err != nil {
		r.mu.Lock()
		r.unsaved = append(unsaved, r.unsaved...)
		r.registryDirty = r.registryDirty || dirty
		r.mu.Unlock()
		return err
	}
	return nil
}

func (r *Renames) store(ctx context.Context, rec Recorder, unsaved []held, registry map[string]string, dirty bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: store renames: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, h := range unsaved {
		// Every rename Home Assistant made is recorded, a rename back as well.
		if rec != nil {
			if _, err := rec.AppendTx(ctx, tx, renameEntry(audit.Actor{Kind: audit.ActorSystem, ID: systemActor}, audit.DirectoryRenamed, h.New, h.Old)); err != nil {
				return err
			}
		}
		if h.undo {
			if _, err := tx.ExecContext(ctx, `UPDATE entity_renames SET resolved_at = ?, resolved_by = 'system', resolution = 'applied'
				WHERE old_id = ? AND new_id = ? AND resolved_at IS NULL`, now, h.New, h.Old); err != nil {
				return fmt.Errorf("catalog: store renames: %w", err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_renames (old_id, new_id, seen_at) VALUES (?, ?, ?)
			ON CONFLICT (old_id, new_id) DO UPDATE SET seen_at = excluded.seen_at, resolved_at = NULL, resolved_by = NULL, resolution = NULL`,
			h.Old, h.New, now); err != nil {
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
// longer apply to it. expected are the former IDs the human saw; if they changed
// meanwhile, nothing is resolved (ErrRenamesChanged). resolution says whether the rules
// were changed (applied) or a human decided to let them go (dismissed).
func (r *Renames) Resolve(ctx context.Context, entityID string, expected []string, resolution, by string) error {
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	// Renames only held in memory are stored first, so that resolving them sticks.
	if err := r.flush(ctx); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: resolve rename: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	done, err := r.ResolveTx(ctx, tx, entityID, expected, resolution, by)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog: resolve rename: %w", err)
	}
	done()
	return nil
}

// ResolveTx is Resolve inside tx, so that the mandates a rename is taken over into and
// the rename change together. Call Store before tx begins, so that held renames are
// stored; call done after tx committed.
func (r *Renames) ResolveTx(ctx context.Context, tx *sql.Tx, entityID string, expected []string, resolution, by string) (done func(), err error) {
	if resolution != ResolutionApplied && resolution != ResolutionDismissed {
		return nil, errors.New("catalog: unknown resolution")
	}
	if by == "" {
		return nil, errors.New("catalog: no user")
	}
	r.mu.RLock()
	rec := r.rec
	formers := formersOf(r.open, entityID)
	var edges []edge
	inside := map[string]bool{entityID: true}
	for _, f := range formers {
		inside[f] = true
	}
	for e := range r.open {
		if inside[e.old] && inside[e.new] && e.old != entityID {
			edges = append(edges, e)
		}
	}
	r.mu.RUnlock()
	if !sameSet(formers, expected) {
		return nil, ErrRenamesChanged
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if rec != nil {
		change := audit.DirectoryRenameApplied
		if resolution == ResolutionDismissed {
			change = audit.DirectoryRenameDismissed
		}
		for _, former := range formers {
			if _, err := rec.AppendTx(ctx, tx, renameEntry(audit.Actor{Kind: audit.ActorUser, ID: by}, change, entityID, former)); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range edges {
		if _, err := tx.ExecContext(ctx, `UPDATE entity_renames SET resolved_at = ?, resolved_by = ?, resolution = ?
			WHERE old_id = ? AND new_id = ? AND resolved_at IS NULL`, now, by, resolution, e.old, e.new); err != nil {
			return nil, fmt.Errorf("catalog: resolve rename: %w", err)
		}
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range edges {
			delete(r.open, e)
		}
	}, nil
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
