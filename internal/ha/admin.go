// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Commands the local UI needs on top of the gateway's own (docs/ARCHITECTURE.md section
// 11.2, decision B1). All of them only read, except the two persistent notification
// services, which have their own checked path below.
const (
	cmdAuthList            = "config/auth/list"
	cmdGetServices         = "get_services"
	cmdPersistentNotifyGet = "persistent_notification/get"
)

// groupAdmin is Home Assistant's administrator group (core auth/const.py GROUP_ID_ADMIN).
const groupAdmin = "system-admin"

// AuthUser is a user from config/auth/list. Only what the UI needs is decoded; the
// credentials Home Assistant includes are dropped.
type AuthUser struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	IsOwner         bool     `json:"is_owner"`
	IsActive        bool     `json:"is_active"`
	SystemGenerated bool     `json:"system_generated"`
	GroupIDs        []string `json:"group_ids"`
}

// IsAdmin is Home Assistant's own rule (core auth/models.py User.is_admin): the owner, or
// an active member of the administrator group.
func (u AuthUser) IsAdmin() bool {
	return u.IsOwner || u.IsActive && slices.Contains(u.GroupIDs, groupAdmin)
}

// ListUsers returns the Home Assistant users. The UI uses it for one thing only: whether
// the person signed in through Ingress is an administrator, and the names of users it
// shows (decisions U2, D15).
func (c *Client) ListUsers(ctx context.Context) ([]AuthUser, error) {
	users, err := call[[]AuthUser](ctx, c, cmdAuthList)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.ID == "" {
			return nil, fmt.Errorf("%w: user without id", ErrProtocol)
		}
	}
	return users, nil
}

// notifyAppPrefix marks the notify services of the Companion App.
const notifyAppPrefix = "mobile_app_"

// NotifyServices returns the notify services of the Companion App ("mobile_app_…",
// without "notify."), sorted. Other notify services are left out: approval requests need
// the app's actionable notifications.
func (c *Client) NotifyServices(ctx context.Context) ([]string, error) {
	all, err := call[map[string]map[string]json.RawMessage](ctx, c, cmdGetServices)
	if err != nil {
		return nil, err
	}
	var out []string
	for name := range all["notify"] {
		if strings.HasPrefix(name, notifyAppPrefix) && notifyServicePattern.MatchString(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// bellIDPattern is the only form of persistent notification ID Home-Mandate creates,
// removes or lists (internal/approval: "hm_approval_" and 32 hex digits). Notifications
// of anyone else are never touched.
var bellIDPattern = regexp.MustCompile(`^hm_approval_[0-9a-f]{32}$`)

// Bell shows Home-Mandate's neutral hint in Home Assistant's notification bell (decision
// F2 B2). It implements approval.Bell.
type Bell struct{ c *Client }

// Bell returns the notification bell of c.
func (c *Client) Bell() Bell { return Bell{c: c} }

// Ring creates or replaces the persistent notification id.
func (b Bell) Ring(ctx context.Context, id string, n Notification) error {
	if !bellIDPattern.MatchString(id) || n.Message == "" || len(n.Actions) > 0 ||
		utf8.RuneCountInString(n.Message) > maxNotifyText || utf8.RuneCountInString(n.Title) > maxNotifyText {
		return fmt.Errorf("%w: persistent notification", ErrCommandNotAllowed)
	}
	_, err := b.c.request(ctx, command{Type: "call_service", Fields: map[string]any{
		"domain": "persistent_notification", "service": "create", "return_response": false,
		"service_data": map[string]any{"notification_id": id, "title": n.Title, "message": n.Message},
	}})
	return err
}

// Clear removes the persistent notification id.
func (b Bell) Clear(ctx context.Context, id string) error {
	if !bellIDPattern.MatchString(id) {
		return fmt.Errorf("%w: persistent notification", ErrCommandNotAllowed)
	}
	_, err := b.c.request(ctx, command{Type: "call_service", Fields: map[string]any{
		"domain": "persistent_notification", "service": "dismiss", "return_response": false,
		"service_data": map[string]any{"notification_id": id},
	}})
	return err
}

// Leftovers returns the IDs of Home-Mandate's own hints that are still shown, e.g. after
// a crash, so that they can be removed at start.
func (b Bell) Leftovers(ctx context.Context) ([]string, error) {
	raw, err := b.c.request(ctx, command{Type: cmdPersistentNotifyGet})
	if err != nil {
		return nil, err
	}
	var list []struct {
		ID string `json:"notification_id"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%w: %s result: %w", ErrProtocol, cmdPersistentNotifyGet, err)
	}
	var out []string
	for _, n := range list {
		if bellIDPattern.MatchString(n.ID) {
			out = append(out, n.ID)
		}
	}
	slices.Sort(out)
	return out, nil
}

// AllowedCommands returns the allowlist of WebSocket commands, sorted; the UI shows it
// under "Why admin rights?" (it is generated from the list that is enforced).
func AllowedCommands() []string {
	return slices.Sorted(maps.Keys(allowedCommands))
}
