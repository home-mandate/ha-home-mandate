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

var (
	// ErrAuditModeProposed means the new version switches audit mode on: it was not stored
	// but waits until a human confirms exactly this version (SPEC-v0 section 11.6). The
	// current version stays in force.
	ErrAuditModeProposed = errors.New("mandate: audit mode waits for a confirmation")
	// ErrAuditModeNeedsConfirmation means a version that switches audit mode on arrived on
	// a path that cannot ask for the confirmation (a new mandate, a template); it is
	// refused.
	ErrAuditModeNeedsConfirmation = errors.New("mandate: switching audit mode on needs a confirmation")
)

// proposal is a version that switches audit mode on, ready to wait for its confirmation.
type proposal struct {
	document []byte
	info     Info
	base     string // the digest of the version in force
}

func (p *proposal) Error() string { return ErrAuditModeNeedsConfirmation.Error() }
func (p *proposal) Unwrap() error { return ErrAuditModeNeedsConfirmation }

// Pending is a version of a mandate that waits for the confirmation of audit mode.
type Pending struct {
	Digest     string
	Document   []byte
	BaseDigest string
	ProposedBy string
	ProposedAt time.Time
}

// proposeTx keeps p as the version of mandate id that waits for a confirmation, in place
// of one that waited before.
func (s *Store) proposeTx(ctx context.Context, tx *sql.Tx, p *proposal, by audit.Actor) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO mandate_pending (mandate_id, digest, document, base_digest, proposed_by, proposed_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (mandate_id) DO UPDATE SET digest = excluded.digest, document = excluded.document,
		base_digest = excluded.base_digest, proposed_by = excluded.proposed_by, proposed_at = excluded.proposed_at`,
		p.info.ID, p.info.Digest, string(p.document), p.base, by.ID, time.Now().UTC().Format(timeFormat))
	if err != nil {
		return fmt.Errorf("mandate: propose: %w", err)
	}
	return nil
}

// dropPendingTx ends the proposal of mandate id, if there is one: another version took
// effect, the mandate was revoked, or a human discarded it.
func dropPendingTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM mandate_pending WHERE mandate_id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("mandate: drop proposal: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Pending returns the version of mandate id that waits for the confirmation of audit
// mode; false if none waits.
func (s *Store) Pending(ctx context.Context, id string) (Pending, bool, error) {
	all, err := s.pending(ctx, `WHERE mandate_id = ?`, id)
	if err != nil || len(all) == 0 {
		return Pending{}, false, err
	}
	return all[id], true, nil
}

// PendingAll returns the waiting versions by mandate ID.
func (s *Store) PendingAll(ctx context.Context) (map[string]Pending, error) {
	return s.pending(ctx, ``)
}

func (s *Store) pending(ctx context.Context, where string, args ...any) (map[string]Pending, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mandate_id, digest, document, base_digest, proposed_by, proposed_at FROM mandate_pending `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("mandate: read proposals: %w", err)
	}
	defer rows.Close()
	out := map[string]Pending{}
	for rows.Next() {
		var id, document, at string
		var p Pending
		if err := rows.Scan(&id, &p.Digest, &document, &p.BaseDigest, &p.ProposedBy, &at); err != nil {
			return nil, fmt.Errorf("mandate: read proposals: %w", err)
		}
		p.Document = []byte(document)
		p.ProposedAt, _ = time.Parse(timeFormat, at)
		out[id] = p
	}
	return out, rows.Err()
}

// ConfirmAuditMode stores the waiting version of mandate id after a human of the
// household confirmed it explicitly (SPEC-v0 section 11.6 item 4). digest names exactly
// the version the human saw and counts for no other: ErrConflict if another version
// waits now, or if another version took effect since it was proposed (the proposal is
// then dropped). ErrNotFound if none waits.
func (s *Store) ConfirmAuditMode(ctx context.Context, id, digest string, by audit.Actor) (Info, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var document, base, current string
		err := tx.QueryRowContext(ctx, `SELECT p.document, p.base_digest, m.current_digest FROM mandate_pending p
			JOIN mandates m ON m.id = p.mandate_id WHERE p.mandate_id = ? AND p.digest = ?`, id, digest).Scan(&document, &base, &current)
		if errors.Is(err, sql.ErrNoRows) {
			var waiting int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mandate_pending WHERE mandate_id = ?`, id).Scan(&waiting); err != nil {
				return fmt.Errorf("mandate: read proposal: %w", err)
			}
			if waiting > 0 {
				return fmt.Errorf("%w: another version waits for the confirmation", ErrConflict)
			}
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("mandate: read proposal: %w", err)
		}
		if base != current {
			return errStale
		}
		info, err := s.check([]byte(document))
		if err != nil {
			return err
		}
		err = s.put(ctx, tx, info, []byte(document), Origin{Kind: OriginEdit}, by, digest)
		if errors.Is(err, ErrAuditModeNeedsConfirmation) {
			// The version would be another than the one confirmed.
			return fmt.Errorf("%w: the version changed since it was proposed", ErrConflict)
		}
		return err
	})
	if errors.Is(err, errStale) {
		// Another version took effect: the proposal no longer applies to the mandate.
		if dropErr := s.inTx(ctx, func(tx *sql.Tx) error { _, err := dropPendingTx(ctx, tx, id); return err }); dropErr != nil {
			return Info{}, dropErr
		}
		return Info{}, fmt.Errorf("%w: another version took effect since the proposal", ErrConflict)
	}
	if err != nil {
		return Info{}, err
	}
	return s.Get(ctx, id)
}

var errStale = errors.New("mandate: the proposal is stale")

// DiscardAuditMode drops the waiting version of mandate id; ErrNotFound if none waits.
func (s *Store) DiscardAuditMode(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		dropped, err := dropPendingTx(ctx, tx, id)
		if err == nil && !dropped {
			return ErrNotFound
		}
		return err
	})
}
