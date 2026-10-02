// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/home-mandate/home-mandate/internal/i18n"
)

var (
	// ErrInvalidApprover means an approver's user ID, devices, channels or language are
	// invalid.
	ErrInvalidApprover = errors.New("approval: invalid approver")
	// ErrApproverNotFound means there is no such approver.
	ErrApproverNotFound = errors.New("approval: approver not found")
	// ErrUINeedsAdmin means the UI channel was chosen for someone who is no Home
	// Assistant administrator; only administrators can open the UI.
	ErrUINeedsAdmin = errors.New("approval: answering in the UI needs an administrator")
)

// maxDevices bounds the devices of one person.
const maxDevices = 5

var (
	// userIDPattern matches Home Assistant user IDs and the approver strings a mandate
	// may contain (SPEC-v0 schema).
	userIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	notifyServicePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

// Approver is a Home Assistant user who answers approval requests (decision F2): on
// any of their devices, and if UI is set also in the Home-Mandate UI, there for
// critical actions only with UICritical. At least one channel is required.
type Approver struct {
	UserID     string
	Devices    []string // notify services, e.g. mobile_app_pixel_9 for notify.mobile_app_pixel_9
	UI         bool
	UICritical bool
	Language   string // "", "de" or "en"; "" means the household language
	CreatedAt  time.Time
}

// Channels is how one approver is reached for one request.
type Channels struct {
	Devices []string
	UI      bool
}

// Reachable tells whether there is at least one channel.
func (c Channels) Reachable() bool {
	return len(c.Devices) > 0 || c.UI
}

// Channels returns the channels of ap for a request: every device always, the UI only
// for an administrator and, for a critical action, only with UICritical (a phone asks
// for unlocking, the UI cannot).
func (ap Approver) Channels(critical, admin bool) Channels {
	return Channels{Devices: ap.Devices, UI: ap.UI && admin && (!critical || ap.UICritical)}
}

// Reach tells whether a person can be reached for ordinary and for critical requests.
type Reach struct {
	Normal   bool
	Critical bool
}

// Reach is shown in the settings, so that nobody believes a person is asked who is not.
func (ap Approver) Reach(admin bool) Reach {
	return Reach{Normal: ap.Channels(false, admin).Reachable(), Critical: ap.Channels(true, admin).Reachable()}
}

// CheckUI refuses the UI channel for someone who is no administrator. The API calls it
// when saving; requests and answers check the administrator again each time.
func CheckUI(ap Approver, admin bool) error {
	if ap.UI && !admin {
		return ErrUINeedsAdmin
	}
	return nil
}

func (ap Approver) validate() error {
	if !userIDPattern.MatchString(ap.UserID) {
		return fmt.Errorf("%w: user ID", ErrInvalidApprover)
	}
	if len(ap.Devices) > maxDevices {
		return fmt.Errorf("%w: more than %d devices", ErrInvalidApprover, maxDevices)
	}
	for i, d := range ap.Devices {
		if !notifyServicePattern.MatchString(d) || slices.Contains(ap.Devices[:i], d) {
			return fmt.Errorf("%w: device", ErrInvalidApprover)
		}
	}
	switch {
	case len(ap.Devices) == 0 && !ap.UI:
		return fmt.Errorf("%w: no channel", ErrInvalidApprover)
	case ap.UICritical && !ap.UI:
		return fmt.Errorf("%w: critical actions in the UI without the UI", ErrInvalidApprover)
	}
	if ap.Language != "" {
		if _, ok := i18n.Parse(ap.Language); !ok || len(ap.Language) != 2 {
			return fmt.Errorf("%w: language must be de or en", ErrInvalidApprover)
		}
	}
	return nil
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

// Put adds or replaces an approver with all their channels.
func (a *Approvers) Put(ctx context.Context, ap Approver) error {
	if err := ap.validate(); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO approvers (user_id, language, ui, ui_critical, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET language = excluded.language, ui = excluded.ui, ui_critical = excluded.ui_critical`,
		ap.UserID, ap.Language, ap.UI, ap.UICritical, a.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM approver_devices WHERE user_id = ?`, ap.UserID); err != nil {
		return fmt.Errorf("approval: store devices: %w", err)
	}
	for _, d := range ap.Devices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO approver_devices (user_id, notify_service) VALUES (?, ?)`, ap.UserID, d); err != nil {
			return fmt.Errorf("approval: store devices: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	return nil
}

// List returns the approvers by user ID, each with their devices in name order.
func (a *Approvers) List(ctx context.Context) ([]Approver, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT a.user_id, a.language, a.ui, a.ui_critical, a.created_at, d.notify_service
		FROM approvers a LEFT JOIN approver_devices d USING (user_id) ORDER BY a.user_id, d.notify_service`)
	if err != nil {
		return nil, fmt.Errorf("approval: list approvers: %w", err)
	}
	defer rows.Close()
	var list []Approver
	for rows.Next() {
		var ap Approver
		var created string
		var device sql.NullString
		if err := rows.Scan(&ap.UserID, &ap.Language, &ap.UI, &ap.UICritical, &created, &device); err != nil {
			return nil, fmt.Errorf("approval: list approvers: %w", err)
		}
		if n := len(list); n > 0 && list[n-1].UserID == ap.UserID {
			list[n-1].Devices = append(list[n-1].Devices, device.String)
			continue
		}
		ap.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if device.Valid {
			ap.Devices = []string{device.String}
		}
		list = append(list, ap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approval: list approvers: %w", err)
	}
	return list, nil
}

// Remove deletes an approver with their devices; open requests to them stay open until
// their timeout, but the UI no longer accepts their answer.
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
