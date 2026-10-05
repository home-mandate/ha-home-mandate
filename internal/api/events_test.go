// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
)

// wsServer serves the API as the Supervisor would reach it.
func (h *harness) wsServer() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = remote
		h.h.ServeHTTP(w, r)
	}))
	h.t.Cleanup(srv.Close)
	return srv
}

type stream struct {
	t    *testing.T
	conn *websocket.Conn
}

func (h *harness) connect(srv *httptest.Server, user string, hdr map[string]string) (*stream, *http.Response, error) {
	h.t.Helper()
	headers := http.Header{"X-Remote-User-Id": {user}, "Origin": {"https://ha.example.org"}, "Sec-Fetch-Site": {"same-origin"}}
	for k, v := range hdr {
		if v == "" {
			headers.Del(k)
		} else {
			headers.Set(k, v)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/events", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return nil, resp, err
	}
	h.t.Cleanup(func() { conn.CloseNow() })
	return &stream{t: h.t, conn: conn}, resp, nil
}

func (s *stream) send(v string) {
	s.t.Helper()
	if err := s.conn.Write(context.Background(), websocket.MessageText, []byte(v)); err != nil {
		s.t.Fatal(err)
	}
}

// next returns the next event, or the close status.
func (s *stream) next() (map[string]any, websocket.StatusCode) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := s.conn.Read(ctx)
	if err != nil {
		return nil, websocket.CloseStatus(err)
	}
	var e map[string]any
	if err := json.Unmarshal(data, &e); err != nil {
		s.t.Fatalf("event is no JSON: %s", data)
	}
	return e, -1
}

// until returns the first event of type typ, skipping others.
func (s *stream) until(typ string) map[string]any {
	s.t.Helper()
	for range 50 {
		e, code := s.next()
		if e == nil {
			s.t.Fatalf("closed with %d while waiting for %s", code, typ)
		}
		if e["type"] == typ {
			return e
		}
	}
	s.t.Fatalf("no %s", typ)
	return nil
}

func (h *harness) open(srv *httptest.Server, user string) *stream {
	h.t.Helper()
	s, _, err := h.connect(srv, user, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	s.send(`{"csrf":"` + h.srv.csrfToken(user, h.now.Now()) + `"}`)
	if e, code := s.next(); e == nil || e["type"] != "system" {
		h.t.Fatalf("first event = %v, %d", e, code)
	}
	return s
}

func TestEventStream(t *testing.T) {
	h := newHarness(t)
	srv := h.wsServer()
	s := h.open(srv, adminID)
	// Lists changed in the UI.
	h.ok(http.MethodPut, "/api/settings", map[string]any{"approval_timeout": "PT1M", "max_actions_per_hour": 10, "bell": false}, nil)
	if e := s.until("settings.changed"); e["type"] != "settings.changed" {
		t.Errorf("event = %v", e)
	}
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": draft(t)}, nil)
	s.until("templates.changed")
	h.ok(http.MethodPut, "/api/approvers/"+annaID, putBody(nil, true, false, nil), nil)
	s.until("approvers.changed")
	sys := s.until("system")
	if sys["system"].(map[string]any)["approvers_configured"].(float64) != 1 {
		t.Errorf("system = %v", sys)
	}
	h.ok(http.MethodPut, "/api/devices/critical", map[string]any{"entity_id": "light.kitchen", "critical": true}, nil)
	s.until("devices.changed")
	// New audit entries, also those of the command line (another process): the tail.
	voice := h.admit("Voice")
	h.srv.tail(context.Background())
	if e := s.until("audit.appended"); e["seq"].(float64) < 2 {
		t.Errorf("audit.appended = %v", e)
	}
	s.until("agents.changed")
	m := s.until("mandates.changed")
	if m["id"] != h.mandateOf(voice.ClientID).ID {
		t.Errorf("mandates.changed = %v", m)
	}
	_, _ = h.agents.SetEmergencyStop(context.Background(), true, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"})
	h.srv.tail(context.Background())
	if e := s.until("system"); !e["system"].(map[string]any)["emergency_stop"].(map[string]any)["active"].(bool) {
		t.Errorf("system after the stop = %v", e)
	}
	h.srv.tail(context.Background()) // nothing new: nothing sent
	h.srv.SystemChanged()
	s.until("system")
}

// approval.opened tells each person whether they can answer; approval.closed carries the
// history entry of the outcome.
func TestApprovalEvents(t *testing.T) {
	h := newHarness(t)
	srv := h.wsServer()
	h.putApprover(approval.Approver{UserID: annaID, UI: true})
	h.putApprover(approval.Approver{UserID: adminID, Devices: []approval.Device{{Service: "mobile_app_iphone_von_markus", Critical: true}}})
	markus, anna := h.open(srv, adminID), h.open(srv, annaID)
	id, done := h.ask(lightRequest(annaID, adminID))
	om, oa := markus.until("approval.opened"), anna.until("approval.opened")
	if om["request"].(map[string]any)["can_answer"] != false || oa["request"].(map[string]any)["can_answer"] != true ||
		om["request"].(map[string]any)["id"] != id {
		t.Errorf("opened = %v / %v", om, oa)
	}
	h.ok(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": true}, nil, as(annaID))
	<-done
	closed := markus.until("approval.closed")
	entry := closed["entry"].(map[string]any)
	if closed["id"] != id || entry["outcome"] != "approved" || entry["via"] != "ui" || entry["by_name"] != "Anna" {
		t.Errorf("closed = %v", closed)
	}
}

// Negative catalog, UI: the stream needs the CSRF token as its first message and is
// refused to other sites; a user who is no administrator any more is closed with 4403.
func TestEventStreamRefusals(t *testing.T) {
	h := newHarness(t)
	srv := h.wsServer()
	for name, hdr := range map[string]map[string]string{
		"cross site": {"Sec-Fetch-Site": "cross-site"},
		"no origin":  {"Origin": ""},
	} {
		if _, resp, err := h.connect(srv, adminID, hdr); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, resp, err := h.connect(srv, guestID, nil); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("no administrator: %v", err)
	}
	// Without Sec-Fetch-Site (Chromium on WebSockets): the Origin must be the host asked for.
	if _, resp, err := h.connect(srv, adminID, map[string]string{"Sec-Fetch-Site": ""}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("without Sec-Fetch-Site, foreign origin: %v", err)
	}
	s0, _, err := h.connect(srv, adminID, map[string]string{"Sec-Fetch-Site": "", "X-Forwarded-Host": "ha.example.org"})
	if err != nil {
		t.Errorf("without Sec-Fetch-Site, own origin: %v", err)
	} else {
		s0.send(`{"csrf":"` + h.srv.csrfToken(adminID, h.now.Now()) + `"}`)
		if e, _ := s0.next(); e == nil {
			t.Error("no system event")
		}
	}
	for name, msg := range map[string]string{
		"wrong token": `{"csrf":"x"}`,
		"other user":  `{"csrf":"` + h.srv.csrfToken(annaID, h.now.Now()) + `"}`,
		"extra field": `{"csrf":"` + h.srv.csrfToken(adminID, h.now.Now()) + `","admin":true}`,
		"not JSON":    `csrf`,
		"too long":    `{"csrf":"` + strings.Repeat("x", 2000) + `"}`,
	} {
		s, _, err := h.connect(srv, adminID, nil)
		if err != nil {
			t.Fatal(err)
		}
		s.send(msg)
		if _, code := s.next(); code != closeCSRF && !(name == "too long" && code == websocket.StatusMessageTooBig) {
			t.Errorf("%s: closed with %d", name, code)
		}
	}
	// A binary first message.
	s, _, _ := h.connect(srv, adminID, nil)
	_ = s.conn.Write(context.Background(), websocket.MessageBinary, []byte(`{"csrf":"x"}`))
	if _, code := s.next(); code != closeCSRF {
		t.Errorf("binary: closed with %d", code)
	}
	// No message in time.
	old := csrfWait
	csrfWait = 50 * time.Millisecond
	defer func() { csrfWait = old }()
	s, _, _ = h.connect(srv, adminID, nil)
	if _, code := s.next(); code != closeCSRF {
		t.Errorf("silent client: closed with %d", code)
	}
}

func TestEventStreamChecksTheAdministratorAgain(t *testing.T) {
	h := newHarness(t)
	oldCheck, oldBeat := adminRecheck, heartbeat
	adminRecheck, heartbeat = 50*time.Millisecond, 20*time.Millisecond
	defer func() { adminRecheck, heartbeat = oldCheck, oldBeat }()
	srv := h.wsServer()
	s := h.open(srv, annaID)
	s.until("system") // heartbeat
	h.ha.set(func(f *fakeHA) { f.users[1].GroupIDs = nil })
	h.now.Add(usersTTL)
	for {
		e, code := s.next()
		if e == nil {
			if code != closeForbidden {
				t.Errorf("closed with %d, want 4403", code)
			}
			break
		}
	}
	// Home Assistant down: try again later, not "forbidden for good".
	s = h.open(srv, adminID)
	h.ha.set(func(f *fakeHA) { f.usersErr = errHA })
	h.now.Add(usersTTL)
	for {
		e, code := s.next()
		if e == nil {
			if code != websocket.StatusTryAgainLater {
				t.Errorf("closed with %d, want 1013", code)
			}
			break
		}
	}
}

// A client that does not read is closed instead of holding up the others.
func TestSlowClientIsClosed(t *testing.T) {
	h := newHarness(t)
	c, ok := h.srv.hub.add(adminID, "")
	if !ok {
		t.Fatal("no client")
	}
	for range clientBuffer + 1 {
		h.srv.publish(event{Type: "templates.changed"})
	}
	select {
	case <-c.overflow:
	default:
		t.Error("overflow not signalled")
	}
	h.srv.publish(event{Type: "templates.changed"}) // closing twice must not panic
	h.srv.hub.remove(c)
	// Too many connections.
	for range maxClients {
		if _, ok := h.srv.hub.add(adminID, ""); !ok {
			t.Fatal("refused below the limit")
		}
	}
	srv := h.wsServer()
	if _, resp, err := h.connect(srv, adminID, nil); err == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("over the limit: %v", err)
	}
}

func TestPublishSkipsWhatCannotBeSent(t *testing.T) {
	h := newHarness(t)
	c, _ := h.srv.hub.add(adminID, "")
	h.srv.hub.publishFor(func(string) any { return map[string]any{"x": make(chan int)} }) // not JSON
	h.srv.hub.publishFor(func(string) any { return strings.Repeat("x", maxEventBytes+1) })
	select {
	case data := <-c.send:
		t.Errorf("sent %d bytes", len(data))
	default:
	}
	// A system status that cannot be read is not announced.
	_ = h.st.Close()
	h.srv.publishSystem(context.Background())
	select {
	case <-c.send:
		t.Error("broken system status sent")
	default:
	}
	if errors.Is(nil, errHA) {
		t.Fatal("unreachable")
	}
}

func TestSameOriginStream(t *testing.T) {
	for _, tc := range []struct {
		origin, site, host string
		forwarded          []string
		want               bool
	}{
		{"https://ha.example.org", "same-origin", "gw:8099", nil, true},
		{"https://evil.example", "cross-site", "gw:8099", nil, false},
		{"https://ha.example.org", "same-site", "gw:8099", nil, false},
		{"", "same-origin", "gw:8099", nil, false},
		{"https://ha.example.org", "", "gw:8099", []string{"ha.example.org"}, true},
		{"https://HA.example.org", "", "gw:8099", []string{"ha.example.org"}, true},
		{"https://ha.example.org:8123", "", "gw:8099", []string{"ha.example.org:8123"}, true},
		{"https://ha.example.org", "", "gw:8099", []string{"evil.example, ha.example.org"}, true},
		{"https://evil.example", "", "gw:8099", []string{"ha.example.org"}, false},
		{"https://gw:8099", "", "gw:8099", nil, true},
		{"https://evil.example", "", "gw:8099", nil, false},
		{"null", "", "gw:8099", nil, false},
		{"::", "", "gw:8099", nil, false},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://gw:8099/api/events", nil)
		r.Host = tc.host
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.site != "" {
			r.Header.Set("Sec-Fetch-Site", tc.site)
		}
		for _, f := range tc.forwarded {
			r.Header.Add("X-Forwarded-Host", f)
		}
		if got := sameOriginStream(r); got != tc.want {
			t.Errorf("%+v = %v", tc, got)
		}
	}
}
