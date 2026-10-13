// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxNotifyText    = 1000
	maxNotifyURL     = 512
	maxNotifyActions = 3
)

var (
	// notifyServicePattern admits only devices of the Companion App: other notify
	// services (a group, a messenger, notify.notify) could reach people who are no
	// approvers, and their buttons would not require unlocking a phone.
	notifyServicePattern = regexp.MustCompile(`^mobile_app_[a-z0-9_]{1,53}$`)
	notifyActionPattern  = regexp.MustCompile(`^[A-Za-z0-9_]{1,80}$`)
	notifyTagPattern     = regexp.MustCompile(`^[A-Za-z0-9_]{1,80}$`)
)

// clearNotification is the message that makes the Companion App remove the notification
// with the tag (iOS from app version 2021.5, Android).
const clearNotification = "clear_notification"

// ValidNotifyService tells whether service names a device of the Companion App
// (notify.mobile_app_…), the only notify services Home-Mandate sends to.
func ValidNotifyService(service string) bool {
	return notifyServicePattern.MatchString(service)
}

// Notification is a push notification for a human, optionally with buttons. Tag, if set,
// is the Companion App's data.tag: a later notification with the same tag replaces it,
// and ClearNotification removes it, buttons included.
type Notification struct {
	Title   string
	Message string
	Actions []NotificationAction
	Tag     string `json:",omitempty"`
	// URL is what a tap opens: an https address or a path of Home Assistant's frontend
	// (iOS data.url, Android data.clickAction); empty opens the Companion App.
	URL string `json:",omitempty"`
	// Sticky keeps the notification on Android when it is tapped.
	Sticky bool `json:",omitempty"`
}

// NotificationAction is a button; pressing it fires mobile_app_notification_action
// with Action as data.action.
type NotificationAction struct {
	Action      string
	Title       string
	Destructive bool
}

// Notify sends a notification through notify.<service>, e.g. notify.mobile_app_pixel_9.
// It is a separate path from device actions: only notify services, no target, and every
// button requires unlocking the phone (authenticationRequired, iOS). The services come
// from the local settings, never from an agent.
func (c *Client) Notify(ctx context.Context, service string, n Notification) error {
	if err := checkNotification(service, n); err != nil {
		return err
	}
	data := map[string]any{"title": n.Title, "message": n.Message}
	extra := map[string]any{}
	if len(n.Actions) > 0 {
		actions := make([]map[string]any, len(n.Actions))
		for i, a := range n.Actions {
			actions[i] = map[string]any{"action": a.Action, "title": a.Title, "authenticationRequired": true}
			if a.Destructive {
				actions[i]["destructive"] = true
			}
		}
		extra["actions"] = actions
	}
	if n.Tag != "" {
		extra["tag"] = n.Tag
	}
	if n.URL != "" {
		extra["url"], extra["clickAction"] = n.URL, n.URL
	}
	if n.Sticky {
		extra["sticky"] = "true" // the Companion App reads the string
	}
	if len(extra) > 0 {
		data["data"] = extra
	}
	return c.notify(ctx, service, data)
}

// ClearNotification removes the notification with tag from the device behind
// notify.<service>, buttons included. The Companion App may need to have been used
// recently for that (a platform limit); a notification that is gone already is no error.
func (c *Client) ClearNotification(ctx context.Context, service, tag string) error {
	switch {
	case !notifyServicePattern.MatchString(service):
		return fmt.Errorf("%w: notify service %q is not a device of the Companion App (mobile_app_…)", ErrCommandNotAllowed, service)
	case !notifyTagPattern.MatchString(tag):
		return fmt.Errorf("%w: notification tag", ErrCommandNotAllowed)
	}
	return c.notify(ctx, service, map[string]any{"message": clearNotification, "data": map[string]any{"tag": tag}})
}

func (c *Client) notify(ctx context.Context, service string, data map[string]any) error {
	_, err := c.request(ctx, command{Type: "call_service", Fields: map[string]any{
		"domain": "notify", "service": service, "service_data": data, "return_response": false,
	}})
	return err
}

func checkNotification(service string, n Notification) error {
	switch {
	case !notifyServicePattern.MatchString(service):
		return fmt.Errorf("%w: notify service %q is not a device of the Companion App (mobile_app_…)", ErrCommandNotAllowed, service)
	case n.Message == "" || utf8.RuneCountInString(n.Message) > maxNotifyText || utf8.RuneCountInString(n.Title) > maxNotifyText:
		return fmt.Errorf("%w: notification text", ErrCommandNotAllowed)
	case len(n.Actions) > maxNotifyActions:
		return fmt.Errorf("%w: too many notification actions", ErrCommandNotAllowed)
	case n.Tag != "" && !notifyTagPattern.MatchString(n.Tag):
		return fmt.Errorf("%w: notification tag", ErrCommandNotAllowed)
	case n.URL != "" && !ValidTapTarget(n.URL):
		return fmt.Errorf("%w: notification target", ErrCommandNotAllowed)
	}
	for _, a := range n.Actions {
		if !notifyActionPattern.MatchString(a.Action) || a.Title == "" || utf8.RuneCountInString(a.Title) > maxNotifyText {
			return fmt.Errorf("%w: notification action", ErrCommandNotAllowed)
		}
	}
	return nil
}

// ValidTapTarget admits an https address without user information or a path of Home
// Assistant's frontend, both without spaces, backslashes or control characters: a tap
// must not open another app or a scheme the Companion App would hand to the system.
func ValidTapTarget(target string) bool {
	if len(target) > maxNotifyURL || strings.IndexFunc(target, func(r rune) bool { return r <= ' ' || r == 0x7f || r == '\\' }) >= 0 {
		return false
	}
	if strings.HasPrefix(target, "/") {
		return !strings.HasPrefix(target, "//")
	}
	u, err := url.Parse(target)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
