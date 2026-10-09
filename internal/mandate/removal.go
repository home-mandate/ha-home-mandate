// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// Removal (SPEC-v0 section 11.3, issue #21).
var (
	// ErrNotRevoked means the mandate is active: only a revoked mandate is removed.
	ErrNotRevoked = errors.New("mandate: not revoked")
	// ErrNotRemoved means the mandate was not removed: only the data of a removed
	// mandate is deleted.
	ErrNotRemoved = errors.New("mandate: not removed")
)

// RemoveTx removes a revoked mandate from the lists inside tx and records
// mandate.removed with the digest of its current version and by as actor (a human, or
// the system for the retention). It reports whether the mandate was removed now;
// removing a removed mandate is a no-op. The mandate stays revoked and its versions stay
// until PurgeTx, as long as audit entries refer to them (SPEC-v0 section 9.3).
func (s *Store) RemoveTx(ctx context.Context, tx *sql.Tx, id string, by audit.Actor) (bool, error) {
	var status, digest string
	var removed bool
	err := tx.QueryRowContext(ctx, `SELECT status, current_digest, removed_at IS NOT NULL FROM mandates WHERE id = ? AND purged_at IS NULL`, id).
		Scan(&status, &digest, &removed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrNotFound
	case err != nil:
		return false, fmt.Errorf("mandate: read: %w", err)
	case status != StatusRevoked:
		return false, ErrNotRevoked
	case removed:
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mandates SET removed_at = ?, removed_by = ? WHERE id = ?`,
		time.Now().UTC().Format(timeFormat), by.ID, id); err != nil {
		return false, fmt.Errorf("mandate: remove: %w", err)
	}
	if _, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventMandateRemoved, Actor: &by,
		Mandate: &audit.Mandate{ID: id, Digest: digest}}); err != nil {
		return false, err
	}
	return true, nil
}

// PurgeTx deletes the versions of a removed mandate inside tx. What stays is a
// tombstone: its ID, display name, agent and the highest version issued, so that no
// version of it is ever accepted again (SPEC-v0 section 3.5) and its name still
// resolves; Get, List and the evaluation leave it out. The caller makes sure that no
// audit entry refers to its versions any more. Purging a purged mandate is a no-op.
func (s *Store) PurgeTx(ctx context.Context, tx *sql.Tx, id string) error {
	var removed, purged bool
	err := tx.QueryRowContext(ctx, `SELECT removed_at IS NOT NULL, purged_at IS NOT NULL FROM mandates WHERE id = ?`, id).Scan(&removed, &purged)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("mandate: read: %w", err)
	case !removed:
		return ErrNotRemoved
	case purged:
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mandate_versions WHERE mandate_id = ?`, id); err != nil {
		return fmt.Errorf("mandate: purge versions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mandates SET removed_by = '', purged_at = ? WHERE id = ?`,
		time.Now().UTC().Format(timeFormat), id); err != nil {
		return fmt.Errorf("mandate: purge: %w", err)
	}
	return nil
}

// Name returns the display name of a mandate (the ID if it has none), also of a
// removed one whose versions were deleted.
func (s *Store) Name(ctx context.Context, id string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM mandates WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("mandate: read: %w", err)
	}
	if name == "" {
		return id, nil
	}
	return name, nil
}
