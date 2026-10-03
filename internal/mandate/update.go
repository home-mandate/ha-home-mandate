// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/home-mandate/home-mandate/internal/audit"
)

// Change says what an edit by a human is based on. The administration API passes it on
// from the editor; the store enforces it, whatever the editor did or did not check.
type Change struct {
	// BaseDigest is the digest of the version the edit started from. If that is not the
	// current version any more, the update is refused: nobody overwrites a version they
	// have not seen.
	BaseDigest string
	// ConfirmCritical is the human's separate confirmation that rules of the new version
	// may allow critical actions without approval.
	ConfirmCritical bool
	// Name, if not empty, becomes the display name in the same transaction.
	Name string
}

// Update stores document as a new version of the mandate id that a human edited. It is
// refused with ErrConflict if change.BaseDigest is not the current version or the mandate
// is revoked, and with ErrCriticalConfirmation if a rule carries allow_critical that the
// current version does not have in exactly this form (a changed or renamed rule is a new
// grant) and change.ConfirmCritical is not set. Unchanged content creates no version.
func (s *Store) Update(ctx context.Context, id string, document []byte, change Change, by audit.Actor) (Info, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var status, digest, current string
		// A digest can repeat among the versions (a restored version); the newest row is the current one.
		err := tx.QueryRowContext(ctx, `SELECT m.status, m.current_digest, v.document FROM mandates m
			JOIN mandate_versions v ON v.mandate_id = m.id AND v.digest = m.current_digest
			WHERE m.id = ? ORDER BY v.version DESC LIMIT 1`, id).Scan(&status, &digest, &current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("mandate: read: %w", err)
		}
		info, err := s.check(document)
		if err != nil {
			return err
		}
		if info.ID != id {
			return fmt.Errorf("%w: the document belongs to mandate %s", ErrInvalid, info.ID)
		}
		if status != StatusActive {
			return fmt.Errorf("%w: the mandate is revoked", ErrConflict)
		}
		if change.BaseDigest != digest {
			return fmt.Errorf("%w: another version was stored meanwhile", ErrConflict)
		}
		if !change.ConfirmCritical {
			granted, err := newCriticalGrant([]byte(current), document)
			if err != nil {
				return err
			}
			if granted {
				return ErrCriticalConfirmation
			}
		}
		if err := s.put(ctx, tx, info, document, by); err != nil {
			return err
		}
		if change.Name != "" {
			return s.SetNameTx(ctx, tx, id, change.Name)
		}
		return nil
	})
	if err != nil {
		return Info{}, err
	}
	return s.Get(ctx, id)
}

// NewCriticalGrant tells whether the document next has a rule with allow_critical that
// current (nil: none) does not have in exactly this form; for checks outside an update,
// such as a new mandate from a template.
func NewCriticalGrant(current, next []byte) (bool, error) {
	if current == nil {
		current = []byte(`{}`)
	}
	return newCriticalGrant(current, next)
}

// newCriticalGrant tells whether next has a rule with allow_critical that current does
// not have in exactly this form. It compares the form of the rules, not their effect:
// removing a deny rule in front of an unchanged allow_critical rule is no new grant.
func newCriticalGrant(current, next []byte) (bool, error) {
	before, err := criticalGrants(current)
	if err != nil {
		return false, err
	}
	after, err := criticalGrants(next)
	if err != nil {
		return false, err
	}
	for _, form := range after {
		if !slices.Contains(before, form) {
			return true, nil
		}
	}
	return false, nil
}

// criticalGrants returns the canonical form of every rule that carries allow_critical.
func criticalGrants(document []byte) ([]string, error) {
	var doc struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var grants []string
	for _, rule := range doc.Rules {
		if rule["allow_critical"] != true {
			continue
		}
		// Marshal writes object keys in sorted order.
		form, err := json.Marshal(canonical(rule))
		if err != nil {
			return nil, fmt.Errorf("mandate: canonical rule: %w", err)
		}
		grants = append(grants, string(form))
	}
	return grants, nil
}

// canonical returns value with every list of strings sorted: the order of actions,
// weekdays and approvers has no meaning.
func canonical(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = canonical(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		texts := make([]string, 0, len(v))
		for i, item := range v {
			out[i] = canonical(item)
			if text, ok := item.(string); ok {
				texts = append(texts, text)
			}
		}
		if len(texts) != len(v) {
			return out
		}
		slices.Sort(texts)
		return texts
	default:
		return value
	}
}
