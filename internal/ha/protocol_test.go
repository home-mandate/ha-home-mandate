// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// The allowlist is defined by docs/ARCHITECTURE.md section 11.2; changing it requires
// changing that table first.
func TestAllowlistMatchesArchitecture(t *testing.T) {
	wantCommands := []string{
		"auth/current_user",
		"call_service",
		"config/area_registry/list",
		"config/auth/list",
		"config/device_registry/list",
		"config/entity_registry/list",
		"config/floor_registry/list",
		"config/label_registry/list",
		"get_config",
		"get_services",
		"get_states",
		"persistent_notification/get",
		"ping",
		"subscribe_entities",
		"subscribe_events",
		"unsubscribe_events",
	}
	wantEvents := []string{
		"area_registry_updated",
		"device_registry_updated",
		"entity_registry_updated",
		"floor_registry_updated",
		"label_registry_updated",
		"mobile_app_notification_action",
		"state_changed",
	}
	if got := sortedKeys(allowedCommands); !slices.Equal(got, wantCommands) {
		t.Errorf("allowed commands = %v, want %v", got, wantCommands)
	}
	if got := sortedKeys(allowedEvents); !slices.Equal(got, wantEvents) {
		t.Errorf("allowed events = %v, want %v", got, wantEvents)
	}
	// The UI shows the list that is enforced (system.ha.commands).
	if got := AllowedCommands(); !slices.Equal(got, wantCommands) {
		t.Errorf("AllowedCommands() = %v, want %v", got, wantCommands)
	}
}

func sortedKeys(m map[string]bool) []string {
	var keys []string
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func TestCommandsOutsideAllowlistNeverReachHA(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	forbidden := []command{
		{Type: "config/auth/create"},
		{Type: "config/auth/delete"},
		{Type: "persistent_notification/subscribe"},
		{Type: "homeassistant/restart"},
		{Type: "execute_script"},
		{Type: "render_template"},
		{Type: "config/entity_registry/update"},
		{Type: "auth/long_lived_access_token"},
		{Type: "subscribe_trigger"},
		{Type: "fire_event"},
		{Type: "GET_STATES"},
		{Type: ""},
		{Type: "subscribe_events"}, // without event_type HA would send every event
		{Type: "subscribe_events", Fields: map[string]any{"event_type": "call_service"}},
		{Type: "subscribe_events", Fields: map[string]any{"event_type": "*"}},
		{Type: "subscribe_events", Fields: map[string]any{"event_type": 42}},
		{Type: "get_states", Fields: map[string]any{"type": "config/auth/create"}},
		{Type: "get_states", Fields: map[string]any{"id": 1}},
	}
	for _, cmd := range forbidden {
		if _, err := c.request(context.Background(), cmd); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("request(%v) = %v, want ErrCommandNotAllowed", cmd, err)
		}
	}
	if _, err := c.SubscribeEvents(context.Background(), "call_service", func(Event) {}); !errors.Is(err, ErrCommandNotAllowed) {
		t.Errorf("SubscribeEvents(call_service) = %v, want ErrCommandNotAllowed", err)
	}
	if _, err := c.SubscribeEvents(context.Background(), EventStateChanged, nil); err == nil {
		t.Error("SubscribeEvents with nil handler succeeded")
	}
	roundTrip(t, c)
	if got := f.receivedTypes(); !slices.Equal(got, []string{"ping"}) {
		t.Errorf("HA received %v, want only the sentinel ping", got)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	tests := []struct {
		name string
		url  string
		tok  Secret
		want error
	}{
		{"supervisor", "ws://supervisor/core/websocket", "t", nil},
		{"localhost", "ws://localhost:8123/api/websocket", "t", nil},
		{"loopback v4", "ws://127.0.0.1:8123/api/websocket", "t", nil},
		{"loopback v6", "ws://[::1]:8123/api/websocket", "t", nil},
		{"tls", "wss://ha.example.org/api/websocket", "t", nil},
		{"plaintext on lan", "ws://192.168.1.10:8123/api/websocket", "t", ErrInsecureURL},
		{"plaintext hostname", "ws://homeassistant.local:8123/api/websocket", "t", ErrInsecureURL},
		{"localhost lookalike", "ws://localhost.evil.example/api/websocket", "t", ErrInsecureURL},
		{"http scheme", "http://localhost:8123/api/websocket", "t", ErrInvalidURL},
		{"userinfo", "wss://user:pw@ha.example.org/api/websocket", "t", ErrInvalidURL},
		{"query", "wss://ha.example.org/api/websocket?token=x", "t", ErrInvalidURL},
		{"fragment", "wss://ha.example.org/api/websocket#x", "t", ErrInvalidURL},
		{"no host", "wss:///api/websocket", "t", ErrInvalidURL},
		{"garbage", "::", "t", ErrInvalidURL},
		{"empty token", "wss://ha.example.org/api/websocket", "", ErrInvalidConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Config{URL: tt.url, Token: tt.tok})
			if !errors.Is(err, tt.want) {
				t.Errorf("New = %v, want %v", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "pw") {
				t.Errorf("error leaks credentials: %v", err)
			}
		})
	}
}

func TestPlaintextAllowed(t *testing.T) {
	tests := []struct {
		host string
		ip   string
		want bool
	}{
		{"localhost", "127.0.0.1", true},
		{"localhost", "::1", true},
		{"localhost", "192.168.1.10", false},
		{"localhost", "203.0.113.7", false},
		{"supervisor", "172.30.32.2", true},
		{"supervisor", "10.0.0.5", true},
		{"supervisor", "203.0.113.7", false},
		{"127.0.0.1", "127.0.0.1", true},
	}
	for _, tt := range tests {
		if got := plaintextAllowed(tt.host, net.ParseIP(tt.ip)); got != tt.want {
			t.Errorf("plaintextAllowed(%s, %s) = %v, want %v", tt.host, tt.ip, got, tt.want)
		}
	}
}

func TestPlaintextDialRefusesNonLocalAddresses(t *testing.T) {
	if _, err := plaintextDial(context.Background(), "tcp", "192.0.2.1:8123"); !errors.Is(err, ErrInsecureURL) {
		t.Errorf("plaintextDial = %v, want ErrInsecureURL", err)
	}
	// Both loopback addresses are tried; the listener only exists on 127.0.0.1.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	conn, err := plaintextDial(context.Background(), "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("plaintextDial(localhost) = %v", err)
	}
	conn.Close()
	// Refused, or timed out where a firewall drops packets to closed ports (WSL).
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := plaintextDial(ctx, "tcp", "127.0.0.1:1"); err == nil || errors.Is(err, ErrInsecureURL) {
		t.Errorf("plaintextDial to a closed local port = %v, want a connection error", err)
	}
	if _, err := plaintextDial(context.Background(), "tcp", "no-port"); err == nil {
		t.Error("plaintextDial without port succeeded")
	}
}

func TestDecodeMessage(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
		want    string
	}{
		{"auth required", `{"type":"auth_required","ha_version":"2026.9.4"}`, false, typeAuthRequired},
		{"result", `{"id":3,"type":"result","success":true,"result":[]}`, false, typeResult},
		{"event", `{"id":3,"type":"event","event":{"event_type":"state_changed"}}`, false, typeEvent},
		{"pong", `{"id":4,"type":"pong"}`, false, typePong},
		{"unknown type", `{"type":"future"}`, false, "future"},
		{"result id zero", `{"id":0,"type":"result","success":true}`, true, ""},
		{"negative id", `{"id":-1,"type":"pong"}`, true, ""},
		{"fractional id", `{"id":1.5,"type":"pong"}`, true, ""},
		{"array", `[]`, true, ""},
		{"string", `"x"`, true, ""},
		{"null", `null`, true, ""},
		{"empty", ``, true, ""},
		{"missing type", `{"id":1}`, true, ""},
		{"wrong type type", `{"type":1}`, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := decodeMessage([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrProtocol) {
				t.Errorf("err = %v, want ErrProtocol", err)
			}
			if err == nil && m.Type != tt.want {
				t.Errorf("type = %q, want %q", m.Type, tt.want)
			}
		})
	}
}

func FuzzDecodeMessage(f *testing.F) {
	for _, s := range []string{
		`{"type":"auth_required"}`, `{"id":1,"type":"result","success":false,"error":{"code":"x","message":"y"}}`,
		`{"id":2,"type":"event","event":{"event_type":"state_changed","data":{},"context":{"user_id":"u"}}}`,
		`[]`, `{"id":1e400,"type":"pong"}`, `{"type":"result","id":"1"}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := decodeMessage(data)
		if err != nil {
			return
		}
		if m.Type == "" {
			t.Fatal("decoded a message without type")
		}
		if (m.Type == typeResult || m.Type == typeEvent || m.Type == typePong) && m.ID <= 0 {
			t.Fatalf("decoded %s without a positive id", m.Type)
		}
	})
}

func TestBackoffStaysWithinBounds(t *testing.T) {
	min, max := 100*time.Millisecond, 3*time.Second
	d := min
	for range 20 {
		j := jitter(d)
		if j < d/2 || j > d {
			t.Fatalf("jitter(%v) = %v, want within [%v, %v]", d, j, d/2, d)
		}
		d = nextBackoff(d, max)
		if d > max {
			t.Fatalf("backoff %v exceeds %v", d, max)
		}
	}
	if d != max {
		t.Errorf("backoff = %v after 20 rounds, want %v", d, max)
	}
	if j := jitter(0); j != 0 {
		t.Errorf("jitter(0) = %v", j)
	}
}

func TestCommandErrorMessage(t *testing.T) {
	err := &CommandError{Code: "not_found", Message: "Entity lock.secret not found"}
	if got := err.Error(); got != "home assistant: command failed: not_found" {
		t.Errorf("Error() = %q", got)
	}
}
