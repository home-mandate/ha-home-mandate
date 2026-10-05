// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/home-mandate/home-mandate/internal/config"
)

const (
	ownerID      = "8f2b1c0d9e7a4b3c8f2b1c0d9e7a4b3c"
	leftoverBell = "hm_approval_00112233445566778899aabbccddeeff"
)

// fakeHomeAssistant answers the WebSocket API with a small household.
type fakeHomeAssistant struct {
	t  *testing.T
	mu sync.Mutex
	// calls are the service calls received, as domain.service.
	calls []string
}

func (f *fakeHomeAssistant) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()
	write := func(v any) {
		data, _ := json.Marshal(v)
		_ = conn.Write(ctx, websocket.MessageText, data)
	}
	write(map[string]any{"type": "auth_required"})
	if _, _, err := conn.Read(ctx); err != nil {
		return
	}
	write(map[string]any{"type": "auth_ok", "ha_version": "2026.9.4"})
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		id, typ := msg["id"], msg["type"]
		var result any = nil
		switch typ {
		case "ping":
			write(map[string]any{"id": id, "type": "pong"})
			continue
		case "get_config":
			result = map[string]any{"version": "2026.9.4", "time_zone": "Europe/Berlin", "language": "de",
				"unit_system": map[string]string{"temperature": "°C", "length": "km"}}
		case "auth/current_user":
			result = map[string]any{"id": "5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b", "name": "Home-Mandate", "is_admin": true}
		case "config/auth/list":
			result = []map[string]any{{"id": ownerID, "name": "Markus", "is_owner": true, "is_active": true, "group_ids": []string{"system-admin"}},
				{"id": "5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b", "name": "Home-Mandate", "is_active": true, "group_ids": []string{"system-admin"}}}
		case "persistent_notification/get":
			result = []map[string]any{{"notification_id": leftoverBell}, {"notification_id": "someone_else"}}
		case "call_service":
			f.mu.Lock()
			f.calls = append(f.calls, msg["domain"].(string)+"."+msg["service"].(string))
			f.mu.Unlock()
			result = map[string]any{}
		case "get_states", "config/entity_registry/list", "config/device_registry/list", "config/area_registry/list":
			result = []any{}
		}
		write(map[string]any{"id": id, "type": "result", "success": true, "result": result})
	}
}

func (f *fakeHomeAssistant) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// The gateway as serve starts it: connected to Home Assistant, it reads the household's
// configuration, removes approval hints a crash left in the bell, reports its state to
// the UI and serves the UI on the Ingress listener only to the Supervisor.
func TestGatewayWithHomeAssistantAndIngress(t *testing.T) {
	fake := &fakeHomeAssistant{t: t}
	haSrv := httptest.NewServer(http.HandlerFunc(fake.serve))
	defer haSrv.Close()
	c := newCLI(t)
	s, err := openStore(context.Background(), c.envVars["HM_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	certFile, keyFile := selfSigned(t, t.TempDir())
	s.cfg = config.Config{Mode: config.ModeContainer, HAURL: "ws" + strings.TrimPrefix(haSrv.URL, "http") + "/api/websocket", HAToken: "t",
		MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile, PublicURL: "https://hm.example.org:8765",
		HABrowserURL: "https://ha.example.org", HAHTTPURL: "https://ha.example.org", IngressAddr: "127.0.0.1:0",
		IngressProxy:    config.SupervisorAddr,
		ApprovalTimeout: 2 * time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	g, err := newGateway(ctx, s, slog.New(slog.DiscardHandler))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- g.run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !(g.status().HAConnected && g.householdTimeZone() != "" && slices.Contains(fake.received(), "persistent_notification.dismiss")) {
		if time.Now().After(deadline) {
			t.Fatalf("status %+v, calls %v", g.status(), fake.received())
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := g.status()
	if st.HAVersion != "2026.9.4" || st.TimeZone != "Europe/Berlin" || st.Language != "de" || st.Units["temperature"] != "°C" ||
		st.ServiceUser != "5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b" || st.HASince.IsZero() || g.householdLanguage() != "de" {
		t.Errorf("status = %+v", st)
	}
	if calls := fake.received(); slices.Contains(calls, "persistent_notification.create") || len(calls) != 1 {
		t.Errorf("calls = %v, want one dismiss of the leftover", calls)
	}

	// Over the network the Ingress listener answers nobody but the Supervisor.
	resp, err := http.Get("http://" + g.ingressL.Addr().String() + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("from 127.0.0.1 = %d", resp.StatusCode)
	}
	// As the Supervisor: the API with the gateway's state.
	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	req.RemoteAddr = "172.30.32.2:1234"
	req.Header.Set("X-Remote-User-Id", ownerID)
	rec := httptest.NewRecorder()
	g.ingress.Handler.ServeHTTP(rec, req)
	var sys struct {
		Mode   string  `json:"mode"`
		MCPURL *string `json:"mcp_url"`
		HA     struct {
			Connected bool    `json:"connected"`
			UserName  *string `json:"user_name"`
		} `json:"ha"`
		TLS struct {
			Present    bool    `json:"present"`
			ValidUntil *string `json:"valid_until"`
		} `json:"tls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sys); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("system = %d %s", rec.Code, rec.Body)
	}
	if sys.Mode != "container" || sys.MCPURL == nil || *sys.MCPURL != "https://hm.example.org:8765/mcp" || !sys.HA.Connected ||
		sys.HA.UserName == nil || *sys.HA.UserName != "Home-Mandate" || !sys.TLS.Present || sys.TLS.ValidUntil == nil {
		t.Errorf("system = %+v", sys)
	}
	// The UI itself (the embedded build or its placeholder).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.30.32.2:1234"
	rec = httptest.NewRecorder()
	g.ingress.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("UI = %d %v", rec.Code, rec.Header())
	}

	g.onDisconnect()
	if g.status().HAConnected && g.householdTimeZone() != "" {
		t.Error("disconnect not recorded")
	}
}

func TestIngressListenerNeedsAFreeAddress(t *testing.T) {
	c := newCLI(t)
	s, err := openStore(context.Background(), c.envVars["HM_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	busy := httptest.NewServer(http.NotFoundHandler())
	defer busy.Close()
	s.cfg = config.Config{Mode: config.ModeContainer, HAURL: "ws://localhost:1/api/websocket", HAToken: "t", MCPAddr: "127.0.0.1:0",
		IngressAddr: strings.TrimPrefix(busy.URL, "http://"), ApprovalTimeout: 2 * time.Minute}
	if _, err := newGateway(context.Background(), s, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("gateway started on a busy Ingress address")
	}
}

func TestTLSStatusWithoutCertificate(t *testing.T) {
	if present, until := (&gateway{}).tlsStatus(); present || !until.IsZero() {
		t.Errorf("status = %v, %v", present, until)
	}
}

// Decision U8: the Home Assistant token is never stored. After a gateway ran with it,
// no file of the data directory contains it.
func TestTheHomeAssistantTokenIsNeverStored(t *testing.T) {
	c := newCLI(t)
	const token = "ha-secret-token-must-not-be-stored-7f3a9c"
	c.envVars["HM_HA_TOKEN"] = token
	e, _, _ := c.env("")
	stderr := &lockedBuffer{}
	e.stderr = stderr
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve"}, e) }()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stderr.String(), "home-mandate started"); {
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not start: %s", stderr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.register("Agent")
	cancel()
	<-done
	entries, err := os.ReadDir(c.envVars["HM_DATA_DIR"])
	if err != nil || len(entries) == 0 {
		t.Fatalf("data directory: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(c.envVars["HM_DATA_DIR"], entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), token) {
			t.Errorf("%s contains the Home Assistant token", entry.Name())
		}
	}
	if strings.Contains(stderr.String(), token) {
		t.Error("the log contains the Home Assistant token")
	}
}

// Decision U2: in app mode the Supervisor must be at the trusted address, else the UI
// stays locked; in container mode the configured proxy is taken as it is.
func TestTrustedProxy(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	supervisor := config.SupervisorAddr
	at := func(addrs ...string) func(context.Context, string) ([]netip.Addr, error) {
		return func(_ context.Context, host string) ([]netip.Addr, error) {
			if host != "supervisor" {
				t.Errorf("looked up %q", host)
			}
			var out []netip.Addr
			for _, a := range addrs {
				out = append(out, netip.MustParseAddr(a))
			}
			return out, nil
		}
	}
	app := config.Config{Mode: config.ModeApp, IngressProxy: supervisor}
	for _, tc := range []struct {
		name   string
		lookup func(context.Context, string) ([]netip.Addr, error)
		want   netip.Addr
	}{
		{"as expected", at("172.30.32.2"), supervisor},
		{"IPv4-mapped, with IPv6 too", at("fd0c:ac1e:2100::2", "::ffff:172.30.32.2"), supervisor},
		{"moved", at("172.30.40.2"), netip.Addr{}},
		{"nothing", at(), netip.Addr{}},
		{"lookup failed", func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }, netip.Addr{}},
	} {
		if got := trustedProxy(context.Background(), app, tc.lookup, logger); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	proxy := netip.MustParseAddr("10.0.0.2")
	container := config.Config{Mode: config.ModeContainer, IngressProxy: proxy}
	if got := trustedProxy(context.Background(), container, nil, logger); got != proxy {
		t.Errorf("container mode = %v", got)
	}
	if addrs, err := lookupHost(context.Background(), "localhost"); err != nil || len(addrs) == 0 {
		t.Errorf("lookupHost(localhost) = %v, %v", addrs, err)
	}
}
