// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestAuthUserIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		u    AuthUser
		want bool
	}{
		{"owner", AuthUser{IsOwner: true, IsActive: true}, true},
		{"inactive owner", AuthUser{IsOwner: true, IsActive: false, GroupIDs: []string{"system-admin"}}, false},
		{"active admin", AuthUser{IsActive: true, GroupIDs: []string{"system-users", "system-admin"}}, true},
		{"inactive admin", AuthUser{IsActive: false, GroupIDs: []string{"system-admin"}}, false},
		{"active user", AuthUser{IsActive: true, GroupIDs: []string{"system-users"}}, false},
		{"read only", AuthUser{IsActive: true, GroupIDs: []string{"system-read-only"}}, false},
		{"look-alike group", AuthUser{IsActive: true, GroupIDs: []string{"System-Admin", "system-admin "}}, false},
		{"no groups", AuthUser{IsActive: true}, false},
	}
	for _, tc := range tests {
		if got := tc.u.IsAdmin(); got != tc.want {
			t.Errorf("%s: IsAdmin() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestListUsers(t *testing.T) {
	f := newFakeHA(t)
	f.handle("config/auth/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{
			{"id": "u1", "name": "Markus", "is_owner": true, "is_active": true, "group_ids": []string{"system-admin"},
				"credentials": []map[string]any{{"type": "homeassistant", "secret": "never decoded"}}},
			{"id": "u2", "name": "Gast", "is_active": true, "group_ids": []string{"system-users"}},
		}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	users, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].ID != "u1" || !users[0].IsAdmin() || users[1].IsAdmin() || users[1].Name != "Gast" {
		t.Errorf("users = %+v", users)
	}
}

func TestListUsersRejectsUserWithoutID(t *testing.T) {
	f := newFakeHA(t)
	f.handle("config/auth/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"name": "nobody", "is_owner": true}}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	if _, err := c.ListUsers(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Errorf("ListUsers = %v, want ErrProtocol", err)
	}
}

func TestListUsersPassesErrors(t *testing.T) {
	f := newFakeHA(t)
	f.handle("config/auth/list", func(fakeMsg) (any, *CommandError) {
		return nil, &CommandError{Code: "unauthorized", Message: "Unauthorized"}
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	var cerr *CommandError
	if _, err := c.ListUsers(context.Background()); !errors.As(err, &cerr) || cerr.Code != "unauthorized" {
		t.Errorf("ListUsers = %v", err)
	}
}

func TestNotifyServices(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_services", func(fakeMsg) (any, *CommandError) {
		return map[string]any{
			"notify": map[string]any{
				"mobile_app_pixel_9":                    map[string]any{"name": "Send"},
				"mobile_app_iphone":                     map[string]any{},
				"persistent_notify":                     map[string]any{},
				"notify":                                map[string]any{},
				"mobile_app_Bad-Name":                   map[string]any{},
				"mobile_app_" + strings.Repeat("x", 60): map[string]any{},
			},
			"light": map[string]any{"mobile_app_fake": map[string]any{}},
		}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	got, err := c.NotifyServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"mobile_app_iphone", "mobile_app_pixel_9"}; !slices.Equal(got, want) {
		t.Errorf("NotifyServices = %v, want %v", got, want)
	}
}

func TestNotifyServicesMalformed(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_services", func(fakeMsg) (any, *CommandError) { return []string{"notify"}, nil })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	if _, err := c.NotifyServices(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Errorf("NotifyServices = %v, want ErrProtocol", err)
	}
}

const testBellID = "hm_approval_00112233445566778899aabbccddeeff"

func TestBellRingAndClear(t *testing.T) {
	f := newFakeHA(t)
	got := make(chan fakeMsg, 2)
	f.handle("call_service", func(m fakeMsg) (any, *CommandError) {
		got <- m
		return map[string]any{}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	bell := c.Bell()
	if err := bell.Ring(context.Background(), testBellID, Notification{Title: "Home-Mandate", Message: "A request waits"}); err != nil {
		t.Fatal(err)
	}
	ring := <-got
	data, _ := ring["service_data"].(map[string]any)
	if ring["domain"] != "persistent_notification" || ring["service"] != "create" || ring["target"] != nil ||
		data["notification_id"] != testBellID || data["message"] != "A request waits" || data["title"] != "Home-Mandate" {
		t.Errorf("ring = %v", ring)
	}
	if err := bell.Clear(context.Background(), testBellID); err != nil {
		t.Fatal(err)
	}
	clear := <-got
	data, _ = clear["service_data"].(map[string]any)
	if clear["domain"] != "persistent_notification" || clear["service"] != "dismiss" || len(data) != 1 || data["notification_id"] != testBellID {
		t.Errorf("clear = %v", clear)
	}
}

func TestBellRefusesForeignIDsAndContent(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	bell := c.Bell()
	ok := Notification{Title: "t", Message: "m"}
	for name, tc := range map[string]struct {
		id string
		n  Notification
	}{
		"foreign id":       {"config_entry_discovery", ok},
		"prefix only":      {"hm_approval_", ok},
		"upper case hex":   {"hm_approval_00112233445566778899AABBCCDDEEFF", ok},
		"suffix":           {testBellID + "x", ok},
		"no message":       {testBellID, Notification{Title: "t"}},
		"long message":     {testBellID, Notification{Message: strings.Repeat("m", maxNotifyText+1)}},
		"long title":       {testBellID, Notification{Title: strings.Repeat("t", maxNotifyText+1), Message: "m"}},
		"with actions":     {testBellID, Notification{Message: "m", Actions: []NotificationAction{{Action: "A", Title: "a"}}}},
		"newline injected": {testBellID + "\n", ok},
	} {
		if err := bell.Ring(context.Background(), tc.id, tc.n); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("Ring %s: %v", name, err)
		}
	}
	for _, id := range []string{"", "persistent_notification", "hm_approval_1"} {
		if err := bell.Clear(context.Background(), id); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("Clear(%q) = %v", id, err)
		}
	}
	if n := f.count("call_service"); n != 0 {
		t.Errorf("%d calls reached Home Assistant", n)
	}
}

func TestBellLeftovers(t *testing.T) {
	f := newFakeHA(t)
	f.handle("persistent_notification/get", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{
			{"notification_id": "hm_approval_ffeeddccbbaa99887766554433221100", "title": "x"},
			{"notification_id": "config_entry_discovery"},
			{"notification_id": testBellID},
			{"notification_id": "hm_approval_not_ours"},
		}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	got, err := c.Bell().Leftovers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{testBellID, "hm_approval_ffeeddccbbaa99887766554433221100"}; !slices.Equal(got, want) {
		t.Errorf("Leftovers = %v, want %v", got, want)
	}
}

func TestBellLeftoversErrors(t *testing.T) {
	f := newFakeHA(t)
	f.handle("persistent_notification/get", func(fakeMsg) (any, *CommandError) { return map[string]any{"x": 1}, nil })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	if _, err := c.Bell().Leftovers(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Errorf("Leftovers = %v, want ErrProtocol", err)
	}
	f.handle("persistent_notification/get", func(fakeMsg) (any, *CommandError) {
		return nil, &CommandError{Code: "unknown_command"}
	})
	if _, err := c.Bell().Leftovers(context.Background()); err == nil {
		t.Error("Leftovers succeeded on an error result")
	}
}
