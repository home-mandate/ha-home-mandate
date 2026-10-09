// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

var (
	// ErrInvalidApprover means an approver's user ID, devices, channels or language are
	// invalid.
	ErrInvalidApprover = errors.New("approval: invalid approver")
	// ErrApproverNotFound means there is no such approver.
	ErrApproverNotFound = errors.New("approval: approver not found")
	// ErrApproversChanged means the approvers changed since the version a change was based
	// on: the first change wins, the later one is refused and nothing is stored.
	ErrApproversChanged = errors.New("approval: approvers changed meanwhile")
	// ErrUINeedsAdmin means the UI channel was chosen for someone who is no Home
	// Assistant administrator; only administrators can open the UI.
	ErrUINeedsAdmin = errors.New("approval: answering in the UI needs an administrator")
)

// maxDevices bounds the devices of one person.
const maxDevices = 5

// userIDPattern matches Home Assistant user IDs and the approver strings a mandate may
// contain (SPEC-v0 schema). Devices must be notify services of the Companion App
// (ha.ValidNotifyService).
var userIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Approver is a Home Assistant user who answers approval requests (decision F2): on
// any of their devices, and if UI is set also in the Home-Mandate UI, there for
// critical actions only with UICritical. At least one channel is required.
type Approver struct {
	UserID     string
	Devices    []Device
	UI         bool
	UICritical bool
	Language   string // "", "de" or "en"; "" means the household language
	CreatedAt  time.Time
}

// Device is a device with the Home Assistant Companion App. Critical tells whether it
// also gets critical requests: a phone asks for unlocking before a button counts (iOS),
// the Mac app and Android do not, so the UI proposes on only for iOS (decision S11).
type Device struct {
	Service  string // notify service, e.g. mobile_app_pixel_9 for notify.mobile_app_pixel_9
	Critical bool
}

// Channels is how one approver is reached for one request.
type Channels struct {
	Devices []string // notify services
	UI      bool
}

// Reachable tells whether there is at least one channel.
func (c Channels) Reachable() bool {
	return len(c.Devices) > 0 || c.UI
}

// Channels returns the channels of ap for a request: every device, for a critical
// action only those with Critical; the UI only for an administrator and, for a critical
// action, only with UICritical (a browser asks for no unlocking).
func (ap Approver) Channels(critical, admin bool) Channels {
	var devices []string
	for _, d := range ap.Devices {
		if !critical || d.Critical {
			devices = append(devices, d.Service)
		}
	}
	return Channels{Devices: devices, UI: ap.UI && admin && (!critical || ap.UICritical)}
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

// How a kind of request reaches a person (decision S9): by push to a device, only in the
// UI (seen only while it is open), or not at all.
const (
	ReachPush = "push"
	ReachUI   = "ui"
	ReachNone = "none"
)

// ReachBy returns the channel ordinary and critical requests reach ap by, with the
// administrator role now.
func (ap Approver) ReachBy(admin bool) (normal, critical string) {
	by := func(c Channels) string {
		switch {
		case len(c.Devices) > 0:
			return ReachPush
		case c.UI:
			return ReachUI
		}
		return ReachNone
	}
	return by(ap.Channels(false, admin)), by(ap.Channels(true, admin))
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
		if !ha.ValidNotifyService(d.Service) ||
			slices.ContainsFunc(ap.Devices[:i], func(other Device) bool { return other.Service == d.Service }) {
			return fmt.Errorf("%w: device %q: each device once, a notify service of the Companion App (mobile_app_…)", ErrInvalidApprover, d.Service)
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

// Recorder writes audit entries within a transaction (audit.Log).
type Recorder interface {
	AppendTx(ctx context.Context, tx *sql.Tx, e audit.Entry) (int64, error)
}

// Approvers stores the approvers. Adding and removing someone is recorded in the audit
// log (approver.changed, SPEC-v0 section 11.1) in the transaction of the change; their
// channels are local settings.
type Approvers struct {
	db  *sql.DB
	rec Recorder
	now func() time.Time
}

// NewApprovers returns the approvers in db; rec records who was added or removed.
func NewApprovers(db *sql.DB, rec Recorder) *Approvers {
	return &Approvers{db: db, rec: rec, now: time.Now}
}

// Put adds or replaces an approver with all their channels; by made the change.
func (a *Approvers) Put(ctx context.Context, ap Approver, by audit.Actor) error {
	return a.PutIf(ctx, ap, "", by)
}

// record writes the approver.changed entry of a change within its transaction: if the
// entry cannot be written, the change is rolled back with it.
func (a *Approvers) record(ctx context.Context, tx *sql.Tx, by audit.Actor, change, userID string) error {
	_, err := a.rec.AppendTx(ctx, tx, audit.Entry{Event: audit.EventApproverChanged, Actor: &by,
		Approver: &audit.Approver{Change: change, ID: userID}})
	return err
}

// PutIf is Put based on the version of the approvers the change started from; "" skips
// the check (the command line). Another version means ErrApproversChanged. Someone who
// was no approver before is an approver.changed audit entry with by as actor.
func (a *Approvers) PutIf(ctx context.Context, ap Approver, version string, by audit.Actor) error {
	if err := ap.validate(); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkVersion(ctx, tx, version); err != nil {
		return err
	}
	var known int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM approvers WHERE user_id = ?`, ap.UserID).Scan(&known); err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO approvers (user_id, language, ui, ui_critical, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET language = excluded.language, ui = excluded.ui, ui_critical = excluded.ui_critical`,
		ap.UserID, ap.Language, ap.UI, ap.UICritical, a.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM approver_devices WHERE user_id = ?`, ap.UserID); err != nil {
		return fmt.Errorf("approval: store devices: %w", err)
	}
	for _, d := range ap.Devices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO approver_devices (user_id, notify_service, critical) VALUES (?, ?, ?)`,
			ap.UserID, d.Service, d.Critical); err != nil {
			return fmt.Errorf("approval: store devices: %w", err)
		}
	}
	if known == 0 {
		if err := a.record(ctx, tx, by, audit.ApproverAdded, ap.UserID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("approval: store approver: %w", err)
	}
	return nil
}

// querier is a database or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// List returns the approvers by user ID, each with their devices in name order.
func (a *Approvers) List(ctx context.Context) ([]Approver, error) {
	return list(ctx, a.db)
}

// Version identifies the current approvers (decision: the first change wins): it changes
// with every stored change, also of the order-free details, and is the same for the same
// approvers.
func (a *Approvers) Version(ctx context.Context) (string, error) {
	l, err := list(ctx, a.db)
	if err != nil {
		return "", err
	}
	return versionOf(l), nil
}

func versionOf(list []Approver) string {
	h := sha256.New()
	for _, ap := range list {
		fmt.Fprintf(h, "%q %q %t %t", ap.UserID, ap.Language, ap.UI, ap.UICritical)
		for _, d := range ap.Devices {
			fmt.Fprintf(h, " %q %t", d.Service, d.Critical)
		}
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// checkVersion refuses a change based on another version; "" means no check.
func checkVersion(ctx context.Context, tx *sql.Tx, version string) error {
	if version == "" {
		return nil
	}
	current, err := list(ctx, tx)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(versionOf(current)), []byte(version)) != 1 {
		return ErrApproversChanged
	}
	return nil
}

func list(ctx context.Context, q querier) ([]Approver, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.user_id, a.language, a.ui, a.ui_critical, a.created_at, d.notify_service, d.critical
		FROM approvers a LEFT JOIN approver_devices d USING (user_id) ORDER BY a.user_id, d.notify_service`)
	if err != nil {
		return nil, fmt.Errorf("approval: list approvers: %w", err)
	}
	defer rows.Close()
	var list []Approver
	for rows.Next() {
		var ap Approver
		var created string
		var service sql.NullString
		var critical sql.NullBool
		if err := rows.Scan(&ap.UserID, &ap.Language, &ap.UI, &ap.UICritical, &created, &service, &critical); err != nil {
			return nil, fmt.Errorf("approval: list approvers: %w", err)
		}
		device := Device{Service: service.String, Critical: critical.Bool}
		if n := len(list); n > 0 && list[n-1].UserID == ap.UserID {
			list[n-1].Devices = append(list[n-1].Devices, device)
			continue
		}
		ap.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if service.Valid {
			ap.Devices = []Device{device}
		}
		list = append(list, ap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approval: list approvers: %w", err)
	}
	return list, nil
}

// Remove deletes an approver with their devices; open requests to them stay open until
// their timeout, but the UI no longer accepts their answer. The removal is an
// approver.changed audit entry with by as actor.
func (a *Approvers) Remove(ctx context.Context, userID string, by audit.Actor) error {
	return a.RemoveIf(ctx, userID, "", by)
}

// RemoveIf is Remove based on a version of the approvers, as PutIf.
func (a *Approvers) RemoveIf(ctx context.Context, userID, version string, by audit.Actor) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("approval: remove approver: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkVersion(ctx, tx, version); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM approvers WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("approval: remove approver: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrApproverNotFound
	}
	if err := a.record(ctx, tx, by, audit.ApproverRemoved, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("approval: remove approver: %w", err)
	}
	return nil
}
