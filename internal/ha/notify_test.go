// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNotify(t *testing.T) {
	f := newFakeHA(t)
	got := make(chan fakeMsg, 1)
	f.handle("call_service", func(m fakeMsg) (any, *CommandError) {
		got <- m
		return map[string]any{}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	n := Notification{Title: "Approval needed", Message: "Voice wants to unlock", Actions: []NotificationAction{
		{Action: "HM_APPROVE_00112233445566778899aabbccddeeff", Title: "Allow"},
		{Action: "HM_DENY_00112233445566778899aabbccddeeff", Title: "Deny", Destructive: true},
	}}
	if err := c.Notify(context.Background(), "mobile_app_pixel_9", n); err != nil {
		t.Fatal(err)
	}
	sent := <-got
	data, _ := sent["service_data"].(map[string]any)
	if sent["domain"] != "notify" || sent["service"] != "mobile_app_pixel_9" || sent["target"] != nil ||
		data["title"] != "Approval needed" || data["message"] != "Voice wants to unlock" {
		t.Fatalf("sent = %v", sent)
	}
	actions := data["data"].(map[string]any)["actions"].([]any)
	want := []any{
		map[string]any{"action": "HM_APPROVE_00112233445566778899aabbccddeeff", "title": "Allow", "authenticationRequired": true},
		map[string]any{"action": "HM_DENY_00112233445566778899aabbccddeeff", "title": "Deny", "authenticationRequired": true, "destructive": true},
	}
	if !reflect.DeepEqual(actions, want) {
		t.Errorf("actions = %v", actions)
	}
}

func TestNotifyWithoutActions(t *testing.T) {
	f := newFakeHA(t)
	got := make(chan fakeMsg, 1)
	f.handle("call_service", func(m fakeMsg) (any, *CommandError) {
		got <- m
		return map[string]any{}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	if err := c.Notify(context.Background(), "mobile_app_pixel_9", Notification{Title: "Warning", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	data, _ := (<-got)["service_data"].(map[string]any)
	if _, ok := data["data"]; ok {
		t.Errorf("service data = %v", data)
	}
}

func TestNotifyRejects(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	ok := Notification{Title: "t", Message: "m"}
	for name, tc := range map[string]struct {
		service string
		n       Notification
	}{
		"service with dot":   {"mobile_app.x", ok},
		"service upper case": {"Mobile_app", ok},
		"empty service":      {"", ok},
		"long service":       {strings.Repeat("a", 65), ok},
		"not mobile_app":     {"telegram_family", ok},
		"all devices":        {"notify", ok},
		"prefix only":        {"mobile_app_", ok},
		"prefix later":       {"x_mobile_app_pixel", ok},
		"no message":         {"mobile_app_x", Notification{Title: "t"}},
		"long message":       {"mobile_app_x", Notification{Title: "t", Message: strings.Repeat("m", maxNotifyText+1)}},
		"long title":         {"mobile_app_x", Notification{Title: strings.Repeat("t", maxNotifyText+1), Message: "m"}},
		"bad action":         {"mobile_app_x", Notification{Title: "t", Message: "m", Actions: []NotificationAction{{Action: "x y", Title: "a"}}}},
		"too many actions":   {"mobile_app_x", Notification{Title: "t", Message: "m", Actions: make([]NotificationAction, 4)}},
	} {
		if err := c.Notify(context.Background(), tc.service, tc.n); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := f.count("call_service"); n != 0 {
		t.Errorf("%d calls reached Home Assistant", n)
	}
}

// A tag lets a notification be replaced or cleared later (Companion App data.tag).
func TestNotifyWithTagAndClear(t *testing.T) {
	f := newFakeHA(t)
	got := make(chan fakeMsg, 2)
	f.handle("call_service", func(m fakeMsg) (any, *CommandError) {
		got <- m
		return map[string]any{}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	tag := "hm_request_00112233445566778899aabbccddeeff"
	if err := c.Notify(context.Background(), "mobile_app_pixel_9", Notification{Title: "t", Message: "m", Tag: tag}); err != nil {
		t.Fatal(err)
	}
	data, _ := (<-got)["service_data"].(map[string]any)
	if inner, _ := data["data"].(map[string]any); inner["tag"] != tag || inner["actions"] != nil {
		t.Errorf("service data = %v", data)
	}
	if err := c.ClearNotification(context.Background(), "mobile_app_pixel_9", tag); err != nil {
		t.Fatal(err)
	}
	sent := <-got
	data, _ = sent["service_data"].(map[string]any)
	if sent["service"] != "mobile_app_pixel_9" || data["message"] != "clear_notification" || data["title"] != nil ||
		!reflect.DeepEqual(data["data"], map[string]any{"tag": tag}) {
		t.Errorf("clear = %v", sent)
	}
	for name, tc := range map[string]struct{ service, tag string }{
		"bad service": {"notify", tag},
		"no tag":      {"mobile_app_pixel_9", ""},
		"bad tag":     {"mobile_app_pixel_9", "a b"},
	} {
		if err := c.ClearNotification(context.Background(), tc.service, tc.tag); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := c.Notify(context.Background(), "mobile_app_pixel_9", Notification{Title: "t", Message: "m", Tag: "x/y"}); !errors.Is(err, ErrCommandNotAllowed) {
		t.Errorf("bad tag in notification: %v", err)
	}
}
