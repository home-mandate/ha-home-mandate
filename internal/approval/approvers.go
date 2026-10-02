// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/home-mandate/home-mandate/internal/i18n"
)

var (
	// ErrInvalidApprover means an approver's user ID, notify service or language is invalid.
	ErrInvalidApprover = errors.New("approval: invalid approver")
	// ErrApproverNotFound means there is no such approver.
	ErrApproverNotFound = errors.New("approval: approver not found")
)

var (
	// userIDPattern matches Home Assistant user IDs and the approver strings a mandate
	// may contain (SPEC-v0 schema).
	userIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	notifyServicePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

// Approver is a Home Assistant user who receives approval requests on a phone.
type Approver struct {
	UserID        string
	NotifyService string // e.g. mobile_app_pixel_9 for notify.mobile_app_pixel_9
	Language      string // "", "de" or "en"; "" means the household language
	CreatedAt     time.Time
}

// Approvers stores the approvers (local settings).
type Approvers struct {
	db  *sql.DB
	now func() time.Time
}

// NewApprovers returns the approvers in db.
func NewApprovers(db *sql.DB) *Approvers {
	return &Approvers{db: db, now: time.Now}
}

// Put adds or replaces an approver.
func (a *Approvers) Put(ctx context.Context, ap Approver) error {
	if !userIDPattern.MatchString(ap.UserID) || !notifyServicePattern.MatchString(ap.NotifyService) {
		return ErrInvalidApprover
	}
	if ap.Language != "" {
		if _, ok := i18n.Parse(ap.Language); !ok || len(ap.Language) != 2 {
			return fmt.Errorf("%w: language must be de or en", ErrInvalidApprover)
		}
	}
	if _, err := a.db.ExecContext(ctx, `INSERT INTO approvers (user_id, notify_service, language, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET notify_service = excluded.notify_service, language = excluded.language`,
		ap.UserID, ap.NotifyService, ap.Language, a.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	return nil
}

// List returns the approvers by user ID.
func (a *Approvers) List(ctx context.Context) ([]Approver, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT user_id, notify_service, language, created_at FROM approvers ORDER BY user_id`)
	if err != nil {
		return nil, fmt.Errorf("approval: list approvers: %w", err)
	}
	defer rows.Close()
	var list []Approver
	for rows.Next() {
		var ap Approver
		var created string
		if err := rows.Scan(&ap.UserID, &ap.NotifyService, &ap.Language, &created); err != nil {
			return nil, fmt.Errorf("approval: list approvers: %w", err)
		}
		ap.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		list = append(list, ap)
	}
	return list, rows.Err()
}

// Remove deletes an approver; open requests to them stay open until their timeout.
func (a *Approvers) Remove(ctx context.Context, userID string) error {
	res, err := a.db.ExecContext(ctx, `DELETE FROM approvers WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("approval: remove approver: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrApproverNotFound
	}
	return nil
}
