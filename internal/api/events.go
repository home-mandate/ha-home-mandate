// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/home-mandate/home-mandate/internal/audit"
)

// Live events over the WebSocket api/events (decision D6, B5). The connection only
// sends; its one incoming message is the CSRF token. Events say what changed; the UI
// reloads lists through the REST endpoints, and everything after every (re)connect.
const (
	closeForbidden = 4403 // the user is no administrator (any more): no further attempts
	closeCSRF      = 4419 // no or a wrong CSRF token: reload the session, then reconnect

	writeTimeout  = 10 * time.Second
	clientBuffer  = 64
	maxClients    = 50
	tailBatch     = 500
	maxEventBytes = 256 << 10
)

// Intervals of the event stream; variables so that tests can shorten them.
var (
	csrfWait     = 10 * time.Second
	adminRecheck = 60 * time.Second
	heartbeat    = 30 * time.Second // a "system" event; the UI's watchdog notices a dead stream
	tailEvery    = time.Second
)

// event is one ServerEvent of the contract; unused fields are left out.
type event struct {
	Type    string               `json:"type"`
	System  *wireSystem          `json:"system,omitempty"`
	Request *wireApprovalRequest `json:"request,omitempty"`
	ID      string               `json:"id,omitempty"`
	Entry   *wireHistoryEntry    `json:"entry,omitempty"`
	Seq     int64                `json:"seq,omitempty"`
}

// hub fans events out to the open connections. A connection whose buffer is full is
// closed instead of blocking the others; the UI reconnects and reloads.
type hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

type client struct {
	user     string
	send     chan []byte
	overflow chan struct{}
	once     sync.Once
}

func newHub() *hub { return &hub{clients: map[*client]struct{}{}} }

func (h *hub) add(user string) (*client, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= maxClients {
		return nil, false
	}
	c := &client{user: user, send: make(chan []byte, clientBuffer), overflow: make(chan struct{})}
	h.clients[c] = struct{}{}
	return c, true
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// publishFor sends to every connection what payload returns for its user.
func (h *hub) publishFor(payload func(user string) any) {
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		data, err := json.Marshal(payload(c.user))
		if err != nil {
			continue
		}
		if len(data) > maxEventBytes { // the UI would miss it: let it reconnect and reload
			c.once.Do(func() { close(c.overflow) })
			continue
		}
		select {
		case c.send <- data:
		default:
			c.once.Do(func() { close(c.overflow) })
		}
	}
}

// publish sends the same event to every connection.
func (s *Server) publish(e event) {
	s.hub.publishFor(func(string) any { return e })
}

// publishSystem sends the current system status to every connection.
func (s *Server) publishSystem(ctx context.Context) {
	sys, err := s.system(ctx)
	if err != nil {
		s.cfg.Logger.Warn("system status not announced", "error", err)
		return
	}
	s.publish(event{Type: "system", System: &sys})
}

// SystemChanged announces a change of the gateway's state, e.g. the connection to Home
// Assistant.
func (s *Server) SystemChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), usersTimeout)
	defer cancel()
	s.publishSystem(ctx)
}

// DevicesChanged announces that the device catalog changed, e.g. a device was renamed or
// an area removed: the UI reloads devices and the references of mandates.
func (s *Server) DevicesChanged() {
	s.publish(event{Type: "devices.changed"})
}

// events serves the WebSocket. The request passed the Supervisor, user, administrator
// and request limit checks already.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userKey{}).(string)
	// The browser opens the stream from the page itself; a page of another site cannot
	// (CSWSH). The CSRF token as first message is the second barrier.
	if !sameOriginStream(r) {
		writeError(w, fail(codeForbidden))
		return
	}
	c, ok := s.hub.add(user)
	if !ok {
		writeError(w, failRetry(codeRateLimited, int(heartbeat/time.Second)))
		return
	}
	defer s.hub.remove(c)
	// Origin is checked above (Sec-Fetch-Site); behind Ingress the Host is not the
	// browser's, so the library's own Origin == Host check would refuse every browser.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024)
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	if !s.readCSRF(ctx, conn, user) {
		_ = conn.Close(closeCSRF, "csrf")
		return
	}
	sys, err := s.system(ctx)
	if err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, "unavailable")
		return
	}
	if !write(ctx, conn, event{Type: "system", System: &sys}) {
		return
	}
	// The client sends nothing more; reading notices its close (and refuses messages).
	go func() {
		defer cancel()
		_, _, _ = conn.Read(ctx)
	}()
	s.stream(ctx, conn, c)
}

// sameOriginStream tells whether a WebSocket handshake comes from the UI's own page.
// Firefox sends Sec-Fetch-Site, Chromium does not on WebSocket handshakes; then the
// Origin must name the host the browser asked for. Behind Ingress that is
// X-Forwarded-Host (set by Home Assistant from the browser's Host; a page cannot set it
// on a WebSocket), otherwise Host.
func sameOriginStream(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := r.Host
	if forwarded := r.Header.Values("X-Forwarded-Host"); len(forwarded) > 0 {
		// The last proxy's value: the one in front of the gateway set it.
		parts := strings.Split(forwarded[len(forwarded)-1], ",")
		host = strings.TrimSpace(parts[len(parts)-1])
	}
	return strings.EqualFold(u.Host, host)
}

// readCSRF reads the one message the client sends: {"csrf": "<token>"}. A context
// deadline on the read would close the connection without a status, so the wait runs
// beside the read and the caller closes with 4419.
func (s *Server) readCSRF(ctx context.Context, conn *websocket.Conn, user string) bool {
	type message struct {
		typ  websocket.MessageType
		data []byte
		err  error
	}
	got := make(chan message, 1)
	go func() {
		typ, data, err := conn.Read(ctx)
		got <- message{typ, data, err}
	}()
	timer := time.NewTimer(csrfWait)
	defer timer.Stop()
	var m message
	select {
	case m = <-got:
	case <-timer.C:
		return false
	}
	if m.err != nil || m.typ != websocket.MessageText {
		return false
	}
	var msg struct {
		CSRF string `json:"csrf"`
	}
	dec := json.NewDecoder(strings.NewReader(string(m.data)))
	dec.DisallowUnknownFields()
	return dec.Decode(&msg) == nil && s.validCSRF(user, msg.CSRF)
}

// stream sends events until the connection ends, a heartbeat every 30 s, and checks the
// administrator rights every 60 s.
func (s *Server) stream(ctx context.Context, conn *websocket.Conn, c *client) {
	beat := time.NewTicker(heartbeat)
	defer beat.Stop()
	check := time.NewTicker(adminRecheck)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.overflow:
			_ = conn.Close(websocket.StatusTryAgainLater, "too slow")
			return
		case data := <-c.send:
			if !writeRaw(ctx, conn, data) {
				return
			}
		case <-beat.C:
			sys, err := s.system(ctx)
			if err != nil || !write(ctx, conn, event{Type: "system", System: &sys}) {
				return
			}
		case <-check.C:
			admin, err := s.users.IsAdmin(ctx, c.user)
			switch {
			case err != nil:
				_ = conn.Close(websocket.StatusTryAgainLater, "unavailable")
				return
			case !admin:
				_ = conn.Close(closeForbidden, "forbidden")
				return
			}
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, e event) bool {
	data, err := json.Marshal(e)
	return err == nil && writeRaw(ctx, conn, data)
}

func writeRaw(ctx context.Context, conn *websocket.Conn, data []byte) bool {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data) == nil
}

// RunTail announces new audit entries until ctx ends: audit.appended with the newest
// seq, and the lists an administrative entry changed. Polling the log also catches the
// changes of the command line, which runs in another process.
func (s *Server) RunTail(ctx context.Context) {
	ticker := time.NewTicker(tailEvery)
	defer ticker.Stop()
	started := false
	for {
		// Start at the end of the log, once it could be read: never replay the whole log.
		if !started {
			last, err := s.cfg.Log.LastSeq(ctx)
			if err == nil {
				s.setLastSeen(last)
				started = true
			} else if ctx.Err() == nil {
				s.cfg.Logger.Warn("reading the audit log failed, retrying", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if started {
				s.tail(ctx)
			}
		}
	}
}

func (s *Server) setLastSeen(seq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSeen = seq
}

// tail announces what was appended since the last call.
func (s *Server) tail(ctx context.Context) {
	s.mu.Lock()
	last := s.lastSeen
	s.mu.Unlock()
	entries, err := s.cfg.Log.After(ctx, last, tailBatch)
	if err != nil {
		if ctx.Err() == nil {
			s.cfg.Logger.Warn("reading new audit entries failed", "error", err)
		}
		return
	}
	if len(entries) == 0 {
		return
	}
	newest := entries[len(entries)-1].Seq
	s.setLastSeen(newest)
	agents, system := false, false
	mandates := map[string]bool{}
	for _, e := range entries {
		switch e.Event {
		case audit.EventAgentRegistered, audit.EventAgentRevoked:
			agents = true
		case audit.EventMandateCreated, audit.EventMandateUpdated, audit.EventMandateRevoked:
			var m struct {
				Mandate struct {
					ID string `json:"id"`
				} `json:"mandate"`
			}
			if json.Unmarshal(e.Entry, &m) == nil && m.Mandate.ID != "" {
				mandates[m.Mandate.ID] = true
			}
		case audit.EventEmergencyStopActivated, audit.EventEmergencyStopReleased:
			system = true
		}
	}
	s.publish(event{Type: "audit.appended", Seq: newest})
	if agents {
		s.publish(event{Type: "agents.changed"})
	}
	for id := range mandates {
		s.publish(event{Type: "mandates.changed", ID: id})
	}
	if system {
		s.publishSystem(ctx)
	}
}
