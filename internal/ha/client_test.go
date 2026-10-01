// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig(url string) Config {
	return Config{
		URL:          url,
		Token:        Secret(fakeToken),
		AuthTimeout:  2 * time.Second,
		PingInterval: time.Hour,
		ReconnectMin: 5 * time.Millisecond,
		ReconnectMax: 20 * time.Millisecond,
	}
}

// startClient runs a client against url until the test ends.
func startClient(t *testing.T, cfg Config) (*Client, <-chan error) {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- c.Run(ctx)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return c, done
}

func waitReady(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
}

func TestGetStates(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_states", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{
			"entity_id":    "lock.front_door",
			"state":        "locked",
			"attributes":   map[string]any{"friendly_name": "Front door"},
			"last_changed": "2026-10-01T10:00:00+00:00",
			"last_updated": "2026-10-01T10:00:00+00:00",
			"context":      map[string]any{"id": "c1", "parent_id": nil, "user_id": nil},
		}}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	states, err := c.GetStates(context.Background())
	if err != nil {
		t.Fatalf("GetStates: %v", err)
	}
	if len(states) != 1 || states[0].EntityID != "lock.front_door" || states[0].State != "locked" ||
		states[0].Attributes["friendly_name"] != "Front door" {
		t.Errorf("states = %+v", states)
	}
}

func TestRegistryListsAndConfig(t *testing.T) {
	f := newFakeHA(t)
	f.handle("config/entity_registry/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"entity_id": "light.kitchen", "device_id": "d1", "area_id": nil, "platform": "hue", "labels": []string{"l1"}}}, nil
	})
	f.handle("config/device_registry/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"id": "d1", "name": "Kitchen light", "area_id": "kitchen"}}, nil
	})
	f.handle("config/area_registry/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"area_id": "kitchen", "name": "Kitchen", "floor_id": "ground"}}, nil
	})
	f.handle("config/floor_registry/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"floor_id": "ground", "name": "Ground floor", "level": 0}}, nil
	})
	f.handle("config/label_registry/list", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"label_id": "l1", "name": "Agents"}}, nil
	})
	f.handle("get_config", func(fakeMsg) (any, *CommandError) {
		return map[string]any{"time_zone": "Europe/Berlin", "language": "de", "version": "2026.9.4",
			"unit_system": map[string]any{"temperature": "°C", "length": "km"}}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	ctx := context.Background()

	entities, err := c.ListEntities(ctx)
	if err != nil || len(entities) != 1 || entities[0].DeviceID != "d1" || entities[0].AreaID != "" || entities[0].Labels[0] != "l1" {
		t.Errorf("ListEntities = %+v, %v", entities, err)
	}
	devices, err := c.ListDevices(ctx)
	if err != nil || len(devices) != 1 || devices[0].AreaID != "kitchen" {
		t.Errorf("ListDevices = %+v, %v", devices, err)
	}
	areas, err := c.ListAreas(ctx)
	if err != nil || len(areas) != 1 || areas[0].FloorID != "ground" {
		t.Errorf("ListAreas = %+v, %v", areas, err)
	}
	floors, err := c.ListFloors(ctx)
	if err != nil || len(floors) != 1 || floors[0].Name != "Ground floor" {
		t.Errorf("ListFloors = %+v, %v", floors, err)
	}
	labels, err := c.ListLabels(ctx)
	if err != nil || len(labels) != 1 || labels[0].LabelID != "l1" {
		t.Errorf("ListLabels = %+v, %v", labels, err)
	}
	cfg, err := c.GetConfig(ctx)
	if err != nil || cfg.TimeZone != "Europe/Berlin" || cfg.UnitSystem["temperature"] != "°C" {
		t.Errorf("GetConfig = %+v, %v", cfg, err)
	}
}

func TestCommandErrorIsReturned(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_states", func(fakeMsg) (any, *CommandError) {
		return nil, &CommandError{Code: "unauthorized", Message: "Unauthorized"}
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	_, err := c.GetStates(context.Background())
	var cerr *CommandError
	if !errors.As(err, &cerr) || cerr.Code != "unauthorized" {
		t.Errorf("error = %v, want CommandError unauthorized", err)
	}
}

func TestMalformedResultIsAnError(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_states", func(fakeMsg) (any, *CommandError) { return "not a list", nil })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	if _, err := c.GetStates(context.Background()); err == nil {
		t.Error("GetStates succeeded on a malformed result")
	}
}

func TestAuthInvalidIsPermanent(t *testing.T) {
	f := newFakeHA(t)
	cfg := testConfig(f.url())
	cfg.Token = Secret("wrong-token-7a3e")
	c, done := startClient(t, cfg)

	select {
	case err := <-done:
		if !errors.Is(err, ErrAuthInvalid) {
			t.Fatalf("Run = %v, want ErrAuthInvalid", err)
		}
		if strings.Contains(err.Error(), "wrong-token-7a3e") {
			t.Errorf("error contains the token: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after auth_invalid")
	}
	time.Sleep(50 * time.Millisecond) // more than the reconnect delay
	if n := f.connections(); n != 1 {
		t.Errorf("connections = %d, want 1 (no retry after auth_invalid)", n)
	}
	if err := c.WaitReady(context.Background()); !errors.Is(err, ErrAuthInvalid) {
		t.Errorf("WaitReady = %v, want ErrAuthInvalid", err)
	}
	if _, err := c.GetStates(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Errorf("GetStates = %v, want ErrDisconnected", err)
	}
}

func TestAuthTimeoutRetries(t *testing.T) {
	f := newFakeHA(t)
	f.set(func(f *fakeHA) { f.authMode = "silent" })
	cfg := testConfig(f.url())
	cfg.AuthTimeout = 20 * time.Millisecond
	startClient(t, cfg)

	eventually(t, "a second connection attempt", func() bool { return f.connections() >= 2 })
}

func TestRequestWhileDisconnectedFailsAndIsNotReplayed(t *testing.T) {
	f := newFakeHA(t)
	c, err := New(testConfig(f.url()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetStates(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("GetStates before Run = %v, want ErrDisconnected", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	f.waitAuthenticated(1)
	time.Sleep(20 * time.Millisecond)
	if n := f.count("get_states"); n != 0 {
		t.Errorf("get_states reached HA %d times after connecting, want 0", n)
	}
}

func TestPendingRequestFailsOnDisconnectAndIsNotReplayed(t *testing.T) {
	f := newFakeHA(t)
	release := make(chan struct{})
	defer close(release)
	f.handle("get_states", func(fakeMsg) (any, *CommandError) {
		<-release
		return []any{}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	errc := make(chan error, 1)
	go func() {
		_, err := c.GetStates(context.Background())
		errc <- err
	}()
	eventually(t, "get_states at HA", func() bool { return f.count("get_states") == 1 })
	f.dropAll()

	select {
	case err := <-errc:
		if !errors.Is(err, ErrDisconnected) {
			t.Errorf("GetStates = %v, want ErrDisconnected", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending request did not fail after the connection was lost")
	}
	f.waitAuthenticated(2)
	time.Sleep(20 * time.Millisecond)
	if n := f.count("get_states"); n != 1 {
		t.Errorf("get_states reached HA %d times, want 1 (no replay)", n)
	}
}

func TestRequestHonoursContext(t *testing.T) {
	f := newFakeHA(t)
	release := make(chan struct{})
	f.handle("get_states", func(fakeMsg) (any, *CommandError) {
		<-release
		return []any{}, nil
	})
	f.handle("get_config", func(fakeMsg) (any, *CommandError) { return map[string]any{}, nil })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.GetStates(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("GetStates = %v, want DeadlineExceeded", err)
	}
	close(release) // the late result must be ignored, the connection stays usable
	if _, err := c.GetConfig(context.Background()); err != nil {
		t.Errorf("GetConfig after a cancelled request: %v", err)
	}
}

func TestEventsAreDelivered(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	events := make(chan Event, 1)
	sub, err := c.SubscribeEvents(context.Background(), EventMobileAppNotificationAction, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe(context.Background())

	f.pushEvent(EventMobileAppNotificationAction, map[string]any{
		"data":       map[string]any{"action": "HM_APPROVE_abc"},
		"time_fired": "2026-10-01T10:00:00+00:00",
		"origin":     "REMOTE",
		"context":    map[string]any{"id": "c1", "user_id": "u-approver"},
	})
	select {
	case e := <-events:
		if e.EventType != EventMobileAppNotificationAction || e.Context.UserID != "u-approver" ||
			!strings.Contains(string(e.Data), "HM_APPROVE_abc") {
			t.Errorf("event = %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
	}
}

func TestReconnectRestoresSubscriptions(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	var mu sync.Mutex
	var got []string
	_, err := c.SubscribeEvents(context.Background(), EventStateChanged, func(e Event) {
		mu.Lock()
		got = append(got, string(e.Data))
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}

	f.dropAll()
	f.waitAuthenticated(2)
	eventually(t, "resubscription", func() bool { return f.count("subscribe_events") == 2 })
	if n := f.pushEvent(EventStateChanged, map[string]any{"data": map[string]any{"n": 1}}); n != 1 {
		t.Fatalf("event sent to %d subscriptions, want 1", n)
	}
	eventually(t, "event after reconnect", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})
}

func TestSubscribeWhileDisconnectedActivatesOnConnect(t *testing.T) {
	f := newFakeHA(t)
	c, err := New(testConfig(f.url()))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 1)
	if _, err := c.SubscribeEvents(context.Background(), EventStateChanged, func(e Event) { events <- e }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	eventually(t, "subscription", func() bool { return f.count("subscribe_events") == 1 })
	f.pushEvent(EventStateChanged, map[string]any{"data": map[string]any{}})
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
	}
}

func TestUnsubscribeStopsEvents(t *testing.T) {
	f := newFakeHA(t)
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	events := make(chan Event, 4)
	sub, err := c.SubscribeEvents(context.Background(), EventStateChanged, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Unsubscribe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.count("unsubscribe_events"); n != 1 {
		t.Errorf("unsubscribe_events = %d, want 1", n)
	}
	if n := f.pushEvent(EventStateChanged, map[string]any{"data": map[string]any{}}); n != 0 {
		t.Errorf("HA still has %d subscriptions", n)
	}
	if err := sub.Unsubscribe(context.Background()); err != nil {
		t.Errorf("second Unsubscribe: %v", err)
	}
}

func TestSubscribeFailureIsReported(t *testing.T) {
	f := newFakeHA(t)
	f.set(func(f *fakeHA) { f.failSubscribe = true })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	_, err := c.SubscribeEvents(context.Background(), EventStateChanged, func(Event) {})
	var cerr *CommandError
	if !errors.As(err, &cerr) || cerr.Code != "unauthorized" {
		t.Fatalf("SubscribeEvents = %v, want CommandError unauthorized", err)
	}
	f.dropAll()
	f.waitAuthenticated(2)
	time.Sleep(20 * time.Millisecond)
	if n := f.count("subscribe_events"); n != 1 {
		t.Errorf("subscribe_events = %d, want 1 (failed subscription is not restored)", n)
	}
}

func TestMalformedMessageReconnects(t *testing.T) {
	tests := map[string][]byte{
		"not json":          []byte("{not json"),
		"array":             []byte(`[{"id":1,"type":"result"}]`),
		"result without id": []byte(`{"type":"result","success":true}`),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFakeHA(t)
			c, _ := startClient(t, testConfig(f.url()))
			waitReady(t, c)

			f.sendRaw(data)
			f.waitAuthenticated(2)
		})
	}
}

func TestUnknownMessagesAreIgnored(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_config", func(fakeMsg) (any, *CommandError) { return map[string]any{}, nil })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	f.sendRaw([]byte(`{"type":"something_new"}`))
	f.sendRaw([]byte(`{"id":999999,"type":"result","success":true,"result":null}`))
	f.sendRaw([]byte(`{"id":999999,"type":"event","event":{}}`))
	if _, err := c.GetConfig(context.Background()); err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if n := f.authenticated(); n != 1 {
		t.Errorf("authentications = %d, want 1", n)
	}
}

func TestOversizedMessageClosesConnection(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_states", func(fakeMsg) (any, *CommandError) {
		return []map[string]any{{"entity_id": "sensor.big", "state": strings.Repeat("x", 4096)}}, nil
	})
	cfg := testConfig(f.url())
	cfg.ReadLimit = 1024
	c, _ := startClient(t, cfg)
	waitReady(t, c)

	if _, err := c.GetStates(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Errorf("GetStates = %v, want ErrDisconnected", err)
	}
	f.waitAuthenticated(2)
}

func TestPingTimeoutReconnects(t *testing.T) {
	f := newFakeHA(t)
	f.set(func(f *fakeHA) { f.ignorePing = true })
	cfg := testConfig(f.url())
	cfg.PingInterval = 10 * time.Millisecond
	cfg.PingTimeout = 20 * time.Millisecond
	startClient(t, cfg)

	f.waitAuthenticated(2)
}

func TestPingKeepsConnection(t *testing.T) {
	f := newFakeHA(t)
	cfg := testConfig(f.url())
	cfg.PingInterval = 5 * time.Millisecond
	cfg.PingTimeout = time.Second
	c, _ := startClient(t, cfg)
	waitReady(t, c)

	eventually(t, "pings", func() bool { return f.count("ping") >= 3 })
	if n := f.authenticated(); n != 1 {
		t.Errorf("authentications = %d, want 1", n)
	}
}

func TestRunReturnsOnCancel(t *testing.T) {
	f := newFakeHA(t)
	c, err := New(testConfig(f.url()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	f.waitAuthenticated(1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if err := c.Run(context.Background()); !errors.Is(err, ErrAlreadyRun) {
		t.Errorf("second Run = %v, want ErrAlreadyRun", err)
	}
}

func TestWaitReadyHonoursContext(t *testing.T) {
	c, err := New(testConfig("ws://localhost:1/api/websocket"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.WaitReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitReady = %v, want DeadlineExceeded", err)
	}
}

func TestUnreachableHAKeepsRetrying(t *testing.T) {
	f := newFakeHA(t)
	url := f.url()
	f.srv.Close()
	startClient(t, testConfig(url))
	time.Sleep(50 * time.Millisecond) // several reconnect attempts; must not panic or exit
}

func TestTLSWithTrustedCertificate(t *testing.T) {
	f := newFakeHA(t)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(f.serve))
	defer tlsSrv.Close()
	f.handle("get_config", func(fakeMsg) (any, *CommandError) { return map[string]any{"time_zone": "UTC"}, nil })
	url := "wss" + strings.TrimPrefix(tlsSrv.URL, "https") + "/api/websocket"

	roots := x509.NewCertPool()
	roots.AddCert(tlsSrv.Certificate())
	cfg := testConfig(url)
	cfg.RootCAs = roots
	c, _ := startClient(t, cfg)
	waitReady(t, c)
	if _, err := c.GetConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTLSRejectsUntrustedCertificate(t *testing.T) {
	f := newFakeHA(t)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(f.serve))
	defer tlsSrv.Close()
	url := "wss" + strings.TrimPrefix(tlsSrv.URL, "https") + "/api/websocket"

	startClient(t, testConfig(url)) // system roots only
	time.Sleep(50 * time.Millisecond)
	if n := f.authenticated(); n != 0 {
		t.Errorf("authenticated %d times against an untrusted certificate", n)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	f := newFakeHA(t)
	redirect := httptest.NewServer(http.RedirectHandler(f.srv.URL+"/api/websocket", http.StatusFound))
	defer redirect.Close()
	url := "ws" + strings.TrimPrefix(redirect.URL, "http") + "/api/websocket"

	startClient(t, testConfig(url))
	time.Sleep(50 * time.Millisecond)
	if n := f.connections(); n != 0 {
		t.Errorf("followed redirect: %d connections", n)
	}
}

// lockedBuffer is a bytes.Buffer safe for the concurrent writes of a logger.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestTokenNeverLogged(t *testing.T) {
	f := newFakeHA(t)
	var buf lockedBuffer
	cfg := testConfig(f.url())
	cfg.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, _ := startClient(t, cfg)
	waitReady(t, c)
	f.dropAll()
	f.waitAuthenticated(2)
	f.sendRaw([]byte("{garbage"))
	f.waitAuthenticated(3)

	cfg.Logger.Info("config", "config", cfg, "token", cfg.Token)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", "config", cfg, "token", cfg.Token)
	out := buf.String() + fmt.Sprintf("%v %+v %#v %s %v", cfg, cfg, cfg, cfg.Token, cfg.Token.LogValue())
	if strings.Contains(out, fakeToken) {
		t.Errorf("token leaked: %s", out)
	}
	if !strings.Contains(buf.String(), "connection lost") {
		t.Errorf("expected a log line about the lost connection, got %q", buf.String())
	}
}

func TestConcurrentRequestsUseIncreasingIDs(t *testing.T) {
	f := newFakeHA(t)
	f.handle("get_config", func(fakeMsg) (any, *CommandError) { return map[string]any{}, nil })
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := c.GetConfig(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []int64
	for _, m := range f.received {
		ids = append(ids, m.id())
	}
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		t.Errorf("ids not strictly increasing in arrival order: %v", ids)
	}
}
