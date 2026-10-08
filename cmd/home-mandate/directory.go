// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
)

// directoryTimeout bounds the work after one catalog refresh.
const directoryTimeout = 10 * time.Second

// carrier keeps the critical mark of a renamed entity (catalog.Marks).
type carrier interface {
	Carry(ctx context.Context, oldID, newID string) (bool, error)
}

// storer stores the renames held in memory and lists the unresolved ones (catalog.Renames).
type storer interface {
	Store(ctx context.Context) error
	Edges() []catalog.Rename
	RenamesLastHour() int
}

// floodNotice tells the administrators once an hour at most when Home Assistant renamed
// more than catalog.RenameFloodThreshold entities in the last hour: that hints at a broken
// integration. Everything is processed and recorded all the same.
type floodNotice struct {
	tell func(ctx context.Context, count int) error
	now  func() time.Time

	mu   sync.Mutex
	last time.Time
}

func (f *floodNotice) check(ctx context.Context, logger *slog.Logger, count int) {
	if f == nil || count <= catalog.RenameFloodThreshold {
		return
	}
	f.mu.Lock()
	now := f.now()
	if !f.last.IsZero() && now.Sub(f.last) < time.Hour {
		f.mu.Unlock()
		return
	}
	f.last = now
	f.mu.Unlock()
	logger.Warn("many renames in Home Assistant in the last hour; a broken integration?", "count", count)
	if err := f.tell(ctx, count); err != nil {
		logger.Warn("administrators not told about the many renames", "error", err)
	}
}

// directoryChanged runs after every catalog refresh. A rename is never rewritten in a
// mandate by itself: rules on the old ID keep applying until a human takes the rename
// over or dismisses it, and the UI shows it. The critical mark moves along, so a rename
// does not lower the protection. Renames and carried marks are audit entries
// (directory.changed), written when they are stored.
func directoryChanged(ctx context.Context, logger *slog.Logger, marks carrier, aliases storer, flood *floodNotice, notify func(), renames []catalog.Rename) {
	ctx, cancel := context.WithTimeout(ctx, directoryTimeout)
	defer cancel()
	if err := aliases.Store(ctx); err != nil {
		// They stay in force in memory and are stored with the next refresh.
		logger.Error("renames not stored", "error", err)
	}
	for _, r := range renames {
		logger.Warn("entity renamed in Home Assistant; rules naming the old ID keep applying until the rename is resolved", "old", r.Old, "new", r.New)
	}
	carryMarks(ctx, logger, marks, aliases.Edges())
	flood.check(ctx, logger, aliases.RenamesLastHour())
	notify()
}

// carryMarks stores the critical mark of every renamed entity whose former ID is marked.
// It runs after every refresh, the first one after a start included, over all unresolved
// renames: a mark that could not be stored once is stored with the next refresh, and a
// restart does not lose it. Storing a mark that is stored already changes nothing and
// records nothing. A chain a → b → c is followed until nothing changes.
func carryMarks(ctx context.Context, logger *slog.Logger, marks carrier, edges []catalog.Rename) {
	for range len(edges) {
		changed := false
		for _, r := range edges {
			carried, err := marks.Carry(ctx, r.Old, r.New)
			switch {
			case err != nil:
				logger.Error("critical mark not carried to the renamed entity, trying again with the next refresh", "old", r.Old, "new", r.New, "error", err)
			case carried:
				changed = true
				logger.Warn("critical mark carried to the renamed entity", "old", r.Old, "new", r.New)
			}
		}
		if !changed {
			return
		}
	}
}
