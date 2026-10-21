// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// ErrMoveChanged means the move of an entity is not the one the caller saw: it moved
// again meanwhile or was dismissed already; nothing was dismissed.
var ErrMoveChanged = errors.New("catalog: the move changed meanwhile")

// AreaMove is an entity whose set of areas changed since a human last looked at it.
// Rules name an area; a rule by area stops applying to an entity that leaves it and
// starts applying to one that enters it, so the household is told which mandates that
// affects (SPEC-v0 section 3.4).
type AreaMove struct {
	EntityID string
	// Previous is the set when the move was found, Current the set now; both sorted.
	Previous, Current []string
	MovedAt           time.Time
}

// Changed returns the areas the entity left or entered, sorted.
func (m AreaMove) Changed() []string {
	var out []string
	for _, a := range m.Previous {
		if !slices.Contains(m.Current, a) {
			out = append(out, a)
		}
	}
	for _, a := range m.Current {
		if !slices.Contains(m.Previous, a) {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

// AreaMoves finds entities that changed their areas, by comparing every refresh of the
// catalog with the sets last seen, which it keeps in the database so that a move during
// an outage or a restart is found as well. A new entity enters its areas; one that
// disappears moves nowhere.
type AreaMoves struct {
	db  *sql.DB
	now func() time.Time

	mu sync.RWMutex
	// seen is the set of areas last seen per entity; empty before the first observation.
	seen map[string][]string
	open map[string]AreaMove
}

// LoadAreaMoves reads the sets last seen and the open moves.
func LoadAreaMoves(ctx context.Context, db *sql.DB) (*AreaMoves, error) {
	m := &AreaMoves{db: db, now: time.Now, seen: map[string][]string{}, open: map[string]AreaMove{}}
	rows, err := db.QueryContext(ctx, `SELECT entity_id, areas FROM directory_areas`)
	if err != nil {
		return nil, fmt.Errorf("catalog: read areas: %w", err)
	}
	for rows.Next() {
		var id, areas string
		if err := rows.Scan(&id, &areas); err != nil {
			rows.Close()
			return nil, fmt.Errorf("catalog: read areas: %w", err)
		}
		if m.seen[id], err = areaList(areas); err != nil {
			rows.Close()
			return nil, err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("catalog: read areas: %w", err)
	}
	rows, err = db.QueryContext(ctx, `SELECT entity_id, previous, current, moved_at FROM area_moves`)
	if err != nil {
		return nil, fmt.Errorf("catalog: read moves: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mv AreaMove
		var previous, current, at string
		if err := rows.Scan(&mv.EntityID, &previous, &current, &at); err != nil {
			return nil, fmt.Errorf("catalog: read moves: %w", err)
		}
		if mv.Previous, err = areaList(previous); err != nil {
			return nil, err
		}
		if mv.Current, err = areaList(current); err != nil {
			return nil, err
		}
		if mv.MovedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("catalog: read moves: %w", err)
		}
		m.open[mv.EntityID] = mv
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: read moves: %w", err)
	}
	return m, nil
}

func areaList(text string) ([]string, error) {
	out := []string{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("catalog: stored areas: %w", err)
	}
	return out, nil
}

// sortedAreas returns a sorted copy of areas, never nil.
func sortedAreas(areas []string) []string {
	out := append([]string{}, areas...)
	slices.Sort(out)
	return out
}

func encodeAreas(areas []string) string {
	data, _ := json.Marshal(areas) // a list of strings always encodes
	return string(data)
}

// Observe compares the devices of a refresh with the sets last seen and stores what
// moved. The first observation of a household only records the sets. A device missing
// from a refresh (disabled, or Home Assistant still starting) keeps its last set and an
// open move, so that it neither enters its areas anew nor loses a move when it returns.
func (m *AreaMoves) Observe(ctx context.Context, devices []Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC().Truncate(time.Second)
	seen := maps.Clone(m.seen)
	open := maps.Clone(m.open)
	first := len(m.seen) == 0
	for _, d := range devices {
		if len(d.EntityID) == 0 || len(d.EntityID) > maxEntityID {
			continue // no entity ID of the vocabulary; the catalog never decides on it
		}
		areas := sortedAreas(d.Areas)
		before, known := m.seen[d.EntityID]
		seen[d.EntityID] = areas
		if first || (known && slices.Equal(before, areas)) {
			continue
		}
		mv, moving := open[d.EntityID]
		if !moving {
			mv = AreaMove{EntityID: d.EntityID, Previous: sortedAreas(before)}
		}
		mv.Current, mv.MovedAt = areas, now
		if slices.Equal(mv.Previous, mv.Current) {
			delete(open, d.EntityID) // moved back
			continue
		}
		open[d.EntityID] = mv
	}
	if err := m.store(ctx, seen, open); err != nil {
		return err
	}
	m.seen, m.open = seen, open
	return nil
}

// maxEntityID is the length limit of an entity ID (SPEC-v0 section 3.4).
const maxEntityID = 255

func sameAreaMove(a, b AreaMove) bool {
	return slices.Equal(a.Previous, b.Previous) && slices.Equal(a.Current, b.Current) && a.MovedAt.Equal(b.MovedAt)
}

// store writes what differs between the sets and moves in memory and seen and open, in
// one transaction; nothing if nothing differs, as after most refreshes.
func (m *AreaMoves) store(ctx context.Context, seen map[string][]string, open map[string]AreaMove) error {
	var changedSets []string
	for id, areas := range seen {
		if before, ok := m.seen[id]; !ok || !slices.Equal(before, areas) {
			changedSets = append(changedSets, id)
		}
	}
	var changedMoves, endedMoves []string
	for id, mv := range open {
		if before, ok := m.open[id]; !ok || !sameAreaMove(before, mv) {
			changedMoves = append(changedMoves, id)
		}
	}
	for id := range m.open {
		if _, ok := open[id]; !ok {
			endedMoves = append(endedMoves, id)
		}
	}
	if len(changedSets)+len(changedMoves)+len(endedMoves) == 0 {
		return nil
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: store areas: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := execEach(ctx, tx, `INSERT INTO directory_areas (entity_id, areas) VALUES (?, ?)
		ON CONFLICT (entity_id) DO UPDATE SET areas = excluded.areas`, changedSets, func(id string) []any {
		return []any{id, encodeAreas(seen[id])}
	}); err != nil {
		return err
	}
	if err := execEach(ctx, tx, `INSERT INTO area_moves (entity_id, previous, current, moved_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (entity_id) DO UPDATE SET previous = excluded.previous, current = excluded.current, moved_at = excluded.moved_at`,
		changedMoves, func(id string) []any {
			mv := open[id]
			return []any{id, encodeAreas(mv.Previous), encodeAreas(mv.Current), mv.MovedAt.Format(time.RFC3339)}
		}); err != nil {
		return err
	}
	if err := execEach(ctx, tx, `DELETE FROM area_moves WHERE entity_id = ?`, endedMoves, func(id string) []any {
		return []any{id}
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog: store areas: %w", err)
	}
	return nil
}

// execEach runs one prepared statement for every ID with the arguments args gives.
func execEach(ctx context.Context, tx *sql.Tx, query string, ids []string, args func(id string) []any) error {
	if len(ids) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("catalog: store areas: %w", err)
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, args(id)...); err != nil {
			return fmt.Errorf("catalog: store areas of %s: %w", id, err)
		}
	}
	return nil
}

// Open returns the moves no human dismissed yet, sorted by entity ID.
func (m *AreaMoves) Open() []AreaMove {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AreaMove, 0, len(m.open))
	for _, id := range slices.Sorted(maps.Keys(m.open)) {
		mv := m.open[id]
		mv.Previous, mv.Current = slices.Clone(mv.Previous), slices.Clone(mv.Current)
		out = append(out, mv)
	}
	return out
}

// Dismiss ends the move of entityID after a human saw it with current as its areas;
// ErrMoveChanged if that is not its move now.
func (m *AreaMoves) Dismiss(ctx context.Context, entityID string, current []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mv, ok := m.open[entityID]
	if !ok || !slices.Equal(mv.Current, sortedAreas(current)) {
		return ErrMoveChanged
	}
	if _, err := m.db.ExecContext(ctx, `DELETE FROM area_moves WHERE entity_id = ?`, entityID); err != nil {
		return fmt.Errorf("catalog: dismiss move: %w", err)
	}
	delete(m.open, entityID)
	return nil
}
