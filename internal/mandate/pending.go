// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
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
	// ErrOwnProposal means the human who proposed audit mode tried to confirm it while
	// another administrator exists, who has to (SPEC-v0 section 11.6 item 4).
	ErrOwnProposal = errors.New("mandate: another administrator confirms this proposal")
)

// ProposalLifetime is how long a version that switches audit mode on waits for its
// confirmation (SPEC-v0 section 11.6 item 4 RECOMMENDS 24 hours): later, an old
// notification cannot adopt it.
const ProposalLifetime = 24 * time.Hour

// expiry is the actor of a proposal that ended because it expired.
var expiry = audit.Actor{Kind: audit.ActorSystem, ID: "proposals"}

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
	ExpiresAt  time.Time
}

// proposeTx keeps p as the version of mandate id that waits for a confirmation, in place
// of one that waited before, which is superseded. Both are recorded (SPEC-v0 section 9.2).
func (s *Store) proposeTx(ctx context.Context, tx *sql.Tx, p *proposal, by audit.Actor) error {
	if _, err := s.dropPendingTx(ctx, tx, p.info.ID, audit.DiscardSuperseded, by); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO mandate_pending (mandate_id, digest, document, base_digest, proposed_by, proposed_at)
		VALUES (?, ?, ?, ?, ?, ?)`, p.info.ID, p.info.Digest, string(p.document), p.base, by.ID, time.Now().UTC().Format(timeFormat))
	if err != nil {
		return fmt.Errorf("mandate: propose: %w", err)
	}
	_, err = s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventMandateProposed, Actor: &by,
		Mandate: &audit.Mandate{ID: p.info.ID, Digest: p.info.Digest, PreviousDigest: p.base}})
	return err
}

// dropPendingTx ends the proposal of mandate id, if there is one, and records why with
// mandate.discarded: another version took effect or another proposal replaced it
// (superseded), the mandate was revoked, a human discarded it (rejected), or it expired.
func (s *Store) dropPendingTx(ctx context.Context, tx *sql.Tx, id, cause string, by audit.Actor) (bool, error) {
	return s.endPendingTx(ctx, tx, id, "", cause, by)
}

// endPendingTx is dropPendingTx for a version that takes effect: if adopted is the waiting
// version, it was confirmed, not discarded, and no mandate.discarded is written.
func (s *Store) endPendingTx(ctx context.Context, tx *sql.Tx, id, adopted, cause string, by audit.Actor) (bool, error) {
	var digest, base string
	err := tx.QueryRowContext(ctx, `DELETE FROM mandate_pending WHERE mandate_id = ? RETURNING digest, base_digest`, id).Scan(&digest, &base)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mandate: drop proposal: %w", err)
	}
	if adopted != "" && digest == adopted {
		return true, nil
	}
	_, err = s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventMandateDiscarded, Actor: &by,
		Mandate: &audit.Mandate{ID: id, Digest: digest, PreviousDigest: base, Cause: cause}})
	return err == nil, err
}

// ExpireProposals ends every proposal that has waited ProposalLifetime or longer at now,
// recorded as discarded with the cause expired. It returns how many ended.
func (s *Store) ExpireProposals(ctx context.Context, now time.Time) (int, error) {
	// Reading first keeps the usual case, nothing expired, out of a write transaction.
	all, err := s.pending(ctx, ``)
	if err != nil {
		return 0, err
	}
	if !slices.ContainsFunc(slices.Collect(maps.Values(all)), func(p Pending) bool { return expired(p, now) }) {
		return 0, nil
	}
	ended := 0
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		n, err := s.expireTx(ctx, tx, now)
		ended = n
		return err
	})
	return ended, err
}

func (s *Store) expireTx(ctx context.Context, tx *sql.Tx, now time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT mandate_id, proposed_at FROM mandate_pending`)
	if err != nil {
		return 0, fmt.Errorf("mandate: read proposals: %w", err)
	}
	var ended []string
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return 0, fmt.Errorf("mandate: read proposals: %w", err)
		}
		proposed, err := time.Parse(timeFormat, at)
		if expired(Pending{ProposedAt: proposed, ExpiresAt: proposed.Add(ProposalLifetime)}, now) || err != nil {
			ended = append(ended, id)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, fmt.Errorf("mandate: read proposals: %w", err)
	}
	for _, id := range ended {
		if _, err := s.dropPendingTx(ctx, tx, id, audit.DiscardExpired, expiry); err != nil {
			return 0, err
		}
	}
	return len(ended), nil
}

// expired tells whether a proposal has waited its lifetime at now. A time that cannot be
// read (zero) cannot show the proposal is fresh: it has expired.
func expired(p Pending, now time.Time) bool {
	return p.ProposedAt.IsZero() || !now.Before(p.ExpiresAt)
}

// Pending returns the version of mandate id that waits for the confirmation of audit
// mode; false if none waits or it expired.
func (s *Store) Pending(ctx context.Context, id string) (Pending, bool, error) {
	all, err := s.waiting(ctx, `WHERE mandate_id = ?`, id)
	if err != nil || len(all) == 0 {
		return Pending{}, false, err
	}
	return all[id], true, nil
}

// PendingAll returns the waiting versions by mandate ID; proposals that expired are left
// out (ExpireProposals ends and records them).
func (s *Store) PendingAll(ctx context.Context) (map[string]Pending, error) {
	return s.waiting(ctx, ``)
}

// waiting is pending without the proposals that expired. It only reads.
func (s *Store) waiting(ctx context.Context, where string, args ...any) (map[string]Pending, error) {
	all, err := s.pending(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	maps.DeleteFunc(all, func(_ string, p Pending) bool { return expired(p, now) })
	return all, nil
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
		if proposed, err := time.Parse(timeFormat, at); err == nil {
			p.ProposedAt, p.ExpiresAt = proposed, proposed.Add(ProposalLifetime)
		}
		out[id] = p
	}
	return out, rows.Err()
}

// ConfirmAuditMode stores the waiting version of mandate id after a human of the
// household confirmed it explicitly (SPEC-v0 section 11.6 item 4). digest names exactly
// the version the human saw and counts for no other: ErrConflict if another version
// waits now, or if another version took effect since it was proposed (the proposal is
// then dropped). ErrNotFound if none waits, or if it expired. ownAllowed tells whether the
// human who proposed it may confirm it: only when no other administrator exists,
// ErrOwnProposal otherwise (SPEC-v0 section 11.6 item 4).
func (s *Store) ConfirmAuditMode(ctx context.Context, id, digest string, by audit.Actor, ownAllowed bool) (Info, error) {
	now := time.Now()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var document, base, current, proposer, at string
		err := tx.QueryRowContext(ctx, `SELECT p.document, p.base_digest, m.current_digest, p.proposed_by, p.proposed_at FROM mandate_pending p
			JOIN mandates m ON m.id = p.mandate_id WHERE p.mandate_id = ? AND p.digest = ?`, id, digest).Scan(&document, &base, &current, &proposer, &at)
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
		if proposed, err := time.Parse(timeFormat, at); err != nil || expired(Pending{ProposedAt: proposed, ExpiresAt: proposed.Add(ProposalLifetime)}, now) {
			return errExpired
		}
		if base != current {
			return errStale
		}
		if proposer == by.ID && !ownAllowed {
			return ErrOwnProposal
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
	if errors.Is(err, errExpired) {
		// Ended and recorded in a transaction of its own; the confirmation finds nothing.
		if _, expErr := s.ExpireProposals(ctx, now); expErr != nil {
			return Info{}, expErr
		}
		return Info{}, ErrNotFound
	}
	if errors.Is(err, errStale) {
		// Another version took effect: the proposal no longer applies to the mandate.
		if dropErr := s.inTx(ctx, func(tx *sql.Tx) error {
			_, err := s.dropPendingTx(ctx, tx, id, audit.DiscardSuperseded, by)
			return err
		}); dropErr != nil {
			return Info{}, dropErr
		}
		return Info{}, fmt.Errorf("%w: another version took effect since the proposal", ErrConflict)
	}
	if err != nil {
		return Info{}, err
	}
	return s.Get(ctx, id)
}

var (
	errStale   = errors.New("mandate: the proposal is stale")
	errExpired = errors.New("mandate: the proposal expired")
)

// DiscardAuditMode drops the waiting version of mandate id, recorded as rejected by the
// human who discarded it; ErrNotFound if none waits.
func (s *Store) DiscardAuditMode(ctx context.Context, id string, by audit.Actor) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		dropped, err := s.dropPendingTx(ctx, tx, id, audit.DiscardRejected, by)
		if err == nil && !dropped {
			return ErrNotFound
		}
		return err
	})
}
