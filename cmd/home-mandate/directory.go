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

// directoryChanged runs after every catalog refresh. A rename is reported, never
// rewritten in a mandate (decision H-E1): the UI shows the rules that name the old ID.
// The critical mark moves along, so a rename does not lower the protection. The audit
// log of the specification has no event for directory changes, so the server log keeps them.
func directoryChanged(ctx context.Context, logger *slog.Logger, marks carrier, notify func(), renames []catalog.Rename) {
	ctx, cancel := context.WithTimeout(ctx, directoryTimeout)
	defer cancel()
	for _, r := range renames {
		logger.Warn("entity renamed in Home Assistant; rules naming the old ID no longer apply to it", "old", r.Old, "new", r.New)
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
