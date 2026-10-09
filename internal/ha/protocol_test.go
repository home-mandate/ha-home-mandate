// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"net"
	"net/netip"
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

// appPlaintext is the policy of app mode, as internal/config sets it.
var appPlaintext = Plaintext{Hosts: []string{"supervisor", "homeassistant"}, Network: netip.MustParsePrefix("172.30.32.0/23")}

func TestNewValidatesConfig(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		tok   Secret
		plain Plaintext
		want  error
	}{
		{"supervisor in app mode", "ws://supervisor/core/websocket", "t", appPlaintext, nil},
		{"homeassistant in app mode", "ws://homeassistant:8123/api/websocket", "t", appPlaintext, nil},
		{"localhost in app mode", "ws://localhost:8123/api/websocket", "t", appPlaintext, ErrInsecureURL},
		{"hassio address in app mode", "ws://172.30.32.1:8123/api/websocket", "t", appPlaintext, ErrInsecureURL},
		{"tls in app mode", "wss://ha.example.org/api/websocket", "t", appPlaintext, nil},
		{"supervisor in container mode", "ws://supervisor/core/websocket", "t", Plaintext{}, ErrInsecureURL},
		{"homeassistant in container mode", "ws://homeassistant:8123/api/websocket", "t", Plaintext{}, ErrInsecureURL},
		{"localhost", "ws://localhost:8123/api/websocket", "t", Plaintext{}, nil},
		{"loopback v4", "ws://127.0.0.1:8123/api/websocket", "t", Plaintext{}, nil},
		{"loopback v6", "ws://[::1]:8123/api/websocket", "t", Plaintext{}, nil},
		{"tls", "wss://ha.example.org/api/websocket", "t", Plaintext{}, nil},
		{"plaintext on lan", "ws://192.168.1.10:8123/api/websocket", "t", Plaintext{}, ErrInsecureURL},
		{"plaintext hostname", "ws://homeassistant.local:8123/api/websocket", "t", Plaintext{}, ErrInsecureURL},
		{"localhost lookalike", "ws://localhost.evil.example/api/websocket", "t", Plaintext{}, ErrInsecureURL},
		{"http scheme", "http://localhost:8123/api/websocket", "t", Plaintext{}, ErrInvalidURL},
		{"userinfo", "wss://user:pw@ha.example.org/api/websocket", "t", Plaintext{}, ErrInvalidURL},
		{"query", "wss://ha.example.org/api/websocket?token=x", "t", Plaintext{}, ErrInvalidURL},
		{"fragment", "wss://ha.example.org/api/websocket#x", "t", Plaintext{}, ErrInvalidURL},
		{"no host", "wss:///api/websocket", "t", Plaintext{}, ErrInvalidURL},
		{"garbage", "::", "t", Plaintext{}, ErrInvalidURL},
		{"empty token", "wss://ha.example.org/api/websocket", "", Plaintext{}, ErrInvalidConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Config{URL: tt.url, Token: tt.tok, Plaintext: tt.plain})
			if !errors.Is(err, tt.want) {
				t.Errorf("New = %v, want %v", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "pw") {
				t.Errorf("error leaks credentials: %v", err)
			}
		})
	}
}

func TestPlaintextAllowsAddress(t *testing.T) {
	tests := []struct {
		name  string
		plain Plaintext
		ip    string
		want  bool
	}{
		{"loopback v4", Plaintext{}, "127.0.0.1", true},
		{"loopback v6", Plaintext{}, "::1", true},
		{"private in container mode", Plaintext{}, "192.168.1.10", false},
		{"hassio in container mode", Plaintext{}, "172.30.32.2", false},
		{"public in container mode", Plaintext{}, "203.0.113.7", false},
		{"core in app mode", appPlaintext, "172.30.32.1", true},
		{"supervisor in app mode", appPlaintext, "172.30.32.2", true},
		{"upper half of hassio", appPlaintext, "172.30.33.9", true},
		{"mapped hassio address", appPlaintext, "::ffff:172.30.32.1", true},
		{"other private in app mode", appPlaintext, "10.0.0.5", false},
		{"next to hassio", appPlaintext, "172.30.34.1", false},
		{"loopback in app mode", appPlaintext, "127.0.0.1", false},
	}
	for _, tt := range tests {
		ip := netip.MustParseAddr(tt.ip)
		if got := tt.plain.allowsAddr(ip); got != tt.want {
			t.Errorf("%s: allowsAddr(%s) = %v, want %v", tt.name, tt.ip, got, tt.want)
		}
	}
}

// fixedLookup resolves every name to ips, as a DNS answer an attacker controls would.
func fixedLookup(ips ...string) func(context.Context, string) ([]net.IPAddr, error) {
	return func(context.Context, string) ([]net.IPAddr, error) {
		var out []net.IPAddr
		for _, ip := range ips {
			out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
		}
		return out, nil
	}
}

// The address is checked after name resolution, so a host name on the list cannot be
// pointed elsewhere via DNS.
func TestPlaintextDialChecksResolvedAddress(t *testing.T) {
	tests := []struct {
		name   string
		plain  Plaintext
		addr   string
		lookup []string
	}{
		{"homeassistant outside hassio", appPlaintext, "homeassistant:8123", []string{"192.0.2.1"}},
		{"supervisor to loopback", appPlaintext, "supervisor:80", []string{"127.0.0.1"}},
		{"supervisor in container mode", Plaintext{}, "supervisor:80", []string{"172.30.32.2"}},
		{"localhost elsewhere", Plaintext{}, "localhost:8123", []string{"192.0.2.1"}},
		{"name not on the list", appPlaintext, "evil:8123", []string{"172.30.32.1"}},
	}
	for _, tt := range tests {
		dial := tt.plain.dialer(fixedLookup(tt.lookup...))
		if _, err := dial(context.Background(), "tcp", tt.addr); !errors.Is(err, ErrInsecureURL) {
			t.Errorf("%s: dial = %v, want ErrInsecureURL", tt.name, err)
		}
	}
}

func TestPlaintextDialRefusesNonLocalAddresses(t *testing.T) {
	dial := Plaintext{}.dialer(net.DefaultResolver.LookupIPAddr)
	if _, err := dial(context.Background(), "tcp", "192.0.2.1:8123"); !errors.Is(err, ErrInsecureURL) {
		t.Errorf("dial = %v, want ErrInsecureURL", err)
	}
	// Both loopback addresses are tried; the listener only exists on 127.0.0.1.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	conn, err := dial(context.Background(), "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("dial(localhost) = %v", err)
	}
	conn.Close()
	// Refused, or timed out where a firewall drops packets to closed ports (WSL).
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := dial(ctx, "tcp", "127.0.0.1:1"); err == nil || errors.Is(err, ErrInsecureURL) {
		t.Errorf("dial to a closed local port = %v, want a connection error", err)
	}
	if _, err := dial(context.Background(), "tcp", "no-port"); err == nil {
		t.Error("dial without port succeeded")
	}
	failing := func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("no such host") }
	if _, err := (Plaintext{}).dialer(failing)(context.Background(), "tcp", "localhost:1"); err == nil {
		t.Error("dial with a failing lookup succeeded")
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
