// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeHA is a scriptable stand-in for the Home Assistant WebSocket API, modelled on
// homeassistant/components/websocket_api. Real HA follows in the week 2 E2E environment.
type fakeHA struct {
	t     *testing.T
	srv   *httptest.Server
	token string

	mu            sync.Mutex
	authMode      string // "ok" (default), "invalid", "silent"
	ignorePing    bool
	failSubscribe bool
	holdSubscribe chan struct{} // if set, subscribe results wait for it
	handlers      map[string]func(fakeMsg) (any, *CommandError)
	conns         []*websocket.Conn
	authed        int
	received      []fakeMsg
	subs          map[*websocket.Conn]map[int64]string
	authedCh      chan struct{}
}

type fakeMsg map[string]any

func (m fakeMsg) typ() string { s, _ := m["type"].(string); return s }
func (m fakeMsg) id() int64   { f, _ := m["id"].(float64); return int64(f) }

const fakeToken = "test-token-5f1c9a0e7b2d"

func newFakeHA(t *testing.T) *fakeHA {
	t.Helper()
	f := &fakeHA{
		t:        t,
		token:    fakeToken,
		authMode: "ok",
		handlers: map[string]func(fakeMsg) (any, *CommandError){},
		subs:     map[*websocket.Conn]map[int64]string{},
		authedCh: make(chan struct{}, 64),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.dropAll()
		f.srv.Close()
	})
	return f
}

func (f *fakeHA) url() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/api/websocket"
}

func (f *fakeHA) handle(typ string, h func(fakeMsg) (any, *CommandError)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[typ] = h
}

func (f *fakeHA) set(fn func(f *fakeHA)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeHA) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	mode := f.authMode
	f.mu.Unlock()
	defer conn.CloseNow()

	ctx := r.Context()
	if !f.authenticate(ctx, conn, mode) {
		return
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m fakeMsg
		if err := json.Unmarshal(data, &m); err != nil {
			return
		}
		f.dispatch(ctx, conn, m)
	}
}

func (f *fakeHA) authenticate(ctx context.Context, conn *websocket.Conn, mode string) bool {
	f.write(ctx, conn, map[string]any{"type": "auth_required", "ha_version": "2026.9.4"})
	_, data, err := conn.Read(ctx)
	if err != nil {
		return false
	}
	var m fakeMsg
	if json.Unmarshal(data, &m) != nil || m.typ() != "auth" {
		return false
	}
	switch {
	case mode == "silent":
		<-ctx.Done()
		return false
	case mode == "invalid" || m["access_token"] != f.token:
		f.write(ctx, conn, map[string]any{"type": "auth_invalid", "message": "Invalid access token or password"})
		return false
	}
	f.write(ctx, conn, map[string]any{"type": "auth_ok", "ha_version": "2026.9.4"})
	f.mu.Lock()
	f.authed++
	f.mu.Unlock()
	select {
	case f.authedCh <- struct{}{}:
	default: // waitAuthenticated also polls the counter
	}
	return true
}

func (f *fakeHA) dispatch(ctx context.Context, conn *websocket.Conn, m fakeMsg) {
	f.mu.Lock()
	f.received = append(f.received, m)
	ignorePing, failSubscribe, holdSubscribe := f.ignorePing, f.failSubscribe, f.holdSubscribe
	h := f.handlers[m.typ()]
	f.mu.Unlock()

	switch m.typ() {
	case "ping":
		if !ignorePing {
			f.write(ctx, conn, map[string]any{"id": m.id(), "type": "pong"})
		}
	case "subscribe_events":
		if failSubscribe {
			f.writeResult(ctx, conn, m.id(), nil, &CommandError{Code: "unauthorized", Message: "Unauthorized"})
			return
		}
		f.mu.Lock()
		if f.subs[conn] == nil {
			f.subs[conn] = map[int64]string{}
		}
		et, _ := m["event_type"].(string)
		f.subs[conn][m.id()] = et
		f.mu.Unlock()
		if holdSubscribe != nil {
			go func() {
				<-holdSubscribe
				f.writeResult(ctx, conn, m.id(), nil, nil)
			}()
			return
		}
		f.writeResult(ctx, conn, m.id(), nil, nil)
	case "unsubscribe_events":
		f.mu.Lock()
		sub, _ := m["subscription"].(float64)
		delete(f.subs[conn], int64(sub))
		f.mu.Unlock()
		f.writeResult(ctx, conn, m.id(), nil, nil)
	default:
		if h == nil {
			f.writeResult(ctx, conn, m.id(), nil, &CommandError{Code: "unknown_command", Message: "Unknown command."})
			return
		}
		go func() {
			result, cerr := h(m)
			f.writeResult(ctx, conn, m.id(), result, cerr)
		}()
	}
}

func (f *fakeHA) writeResult(ctx context.Context, conn *websocket.Conn, id int64, result any, cerr *CommandError) {
	msg := map[string]any{"id": id, "type": "result", "success": cerr == nil, "result": result}
	if cerr != nil {
		msg["error"] = map[string]any{"code": cerr.Code, "message": cerr.Message}
	}
	f.write(ctx, conn, msg)
}

func (f *fakeHA) write(ctx context.Context, conn *websocket.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		f.t.Errorf("fake HA: marshal: %v", err)
		return
	}
	_ = conn.Write(ctx, websocket.MessageText, data)
}

// pushEvent sends an event to every subscription for eventType on open connections.
func (f *fakeHA) pushEvent(eventType string, event map[string]any) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	sent := 0
	for conn, subs := range f.subs {
		for id, et := range subs {
			if et != eventType {
				continue
			}
			event["event_type"] = eventType
			data, _ := json.Marshal(map[string]any{"id": id, "type": "event", "event": event})
			if conn.Write(context.Background(), websocket.MessageText, data) == nil {
				sent++
			}
		}
	}
	return sent
}

// sendRaw sends data on the most recent connection.
func (f *fakeHA) sendRaw(data []byte) {
	f.mu.Lock()
	conn := f.conns[len(f.conns)-1]
	f.mu.Unlock()
	_ = conn.Write(context.Background(), websocket.MessageText, data)
}

func (f *fakeHA) dropAll() {
	f.mu.Lock()
	conns := f.conns
	f.conns = nil
	f.subs = map[*websocket.Conn]map[int64]string{}
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.CloseNow()
	}
}

func (f *fakeHA) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.conns)
}

func (f *fakeHA) authenticated() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authed
}

func (f *fakeHA) count(typ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.received {
		if m.typ() == typ {
			n++
		}
	}
	return n
}

func (f *fakeHA) receivedTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var types []string
	for _, m := range f.received {
		types = append(types, m.typ())
	}
	return types
}

// waitAuthenticated waits for the n-th successful authentication.
func (f *fakeHA) waitAuthenticated(n int) {
	f.t.Helper()
	deadline := time.After(5 * time.Second)
	for f.authenticated() < n {
		select {
		case <-f.authedCh:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			f.t.Fatalf("fake HA: %d authentications, want %d", f.authenticated(), n)
		}
	}
}

// eventually polls cond until it holds or fails the test after a timeout.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
