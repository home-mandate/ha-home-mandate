// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/home-mandate/home-mandate/internal/catalog"
)

// directoryTimeout bounds the work after one catalog refresh.
const directoryTimeout = 10 * time.Second

// carrier keeps the critical mark of a renamed entity (catalog.Marks).
type carrier interface {
	Carry(ctx context.Context, oldID, newID string) (bool, error)
}

// storer stores the renames held in memory (catalog.Renames).
type storer interface {
	Store(ctx context.Context) error
}

// directoryChanged runs after every catalog refresh. A rename is never rewritten in a
// mandate by itself: rules on the old ID keep applying until a human takes the rename
// over or dismisses it, and the UI shows it. The critical mark moves along, so a rename
// does not lower the protection. Renames and carried marks are audit entries
// (directory.changed), written when they are stored.
func directoryChanged(ctx context.Context, logger *slog.Logger, marks carrier, aliases storer, notify func(), renames []catalog.Rename) {
	ctx, cancel := context.WithTimeout(ctx, directoryTimeout)
	defer cancel()
	if err := aliases.Store(ctx); err != nil {
		// They stay in force in memory and are stored with the next refresh.
		logger.Error("renames not stored", "error", err)
	}
	for _, r := range renames {
		logger.Warn("entity renamed in Home Assistant; rules naming the old ID keep applying until the rename is resolved", "old", r.Old, "new", r.New)
		carried, err := marks.Carry(ctx, r.Old, r.New)
		switch {
		case err != nil:
			logger.Error("critical mark not carried to the renamed entity", "old", r.Old, "new", r.New, "error", err)
		case carried:
			logger.Warn("critical mark carried to the renamed entity", "old", r.Old, "new", r.New)
		}
	}
	notify()
}
