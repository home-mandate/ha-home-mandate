// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ha is the client for the Home Assistant WebSocket API.
//
// Only commands on a fixed allowlist (docs/ARCHITECTURE.md section 11.2) are ever sent.
// Requests never wait for a connection: while disconnected they fail with
// ErrDisconnected, and requests in flight fail when the connection is lost, so nothing
// is executed later (E2E scenario 12). Run reconnects with exponential backoff and
// restores event subscriptions; a rejected access token stops it for good.
package ha

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

var (
	// ErrInvalidConfig means the configuration is incomplete.
	ErrInvalidConfig = errors.New("home assistant: invalid configuration")
	// ErrInvalidURL means the URL is not a ws:// or wss:// URL without credentials,
	// query or fragment.
	ErrInvalidURL = errors.New("home assistant: invalid URL")
	// ErrInsecureURL means a plaintext ws:// URL to a host other than localhost or the
	// Supervisor.
	ErrInsecureURL = errors.New("home assistant: plaintext connection outside localhost")
	// ErrAuthInvalid means Home Assistant rejected the access token. Run does not retry.
	ErrAuthInvalid = errors.New("home assistant: access token rejected")
	// ErrDisconnected means there is no connection; the request was not executed or its
	// outcome is unknown.
	ErrDisconnected = errors.New("home assistant: not connected")
	// ErrAlreadyRun means Run was called more than once.
	ErrAlreadyRun = errors.New("home assistant: Run called twice")
	// ErrStopped means Run has returned; the client does not connect again.
	ErrStopped = errors.New("home assistant: client stopped")
)

// Defaults for zero Config fields.
const (
	DefaultReadLimit    = 16 << 20
	DefaultAuthTimeout  = 10 * time.Second
	DefaultPingInterval = 30 * time.Second
	DefaultPingTimeout  = 10 * time.Second
	DefaultReconnectMin = time.Second
	DefaultReconnectMax = 30 * time.Second

	writeTimeout = 10 * time.Second
	// stableSession is how long a connection must last before the backoff resets, so a
	// server that accepts and immediately drops connections is not hammered.
	stableSession = 10 * time.Second
)

// plaintextHosts may be reached via ws://: the local host and the Supervisor proxy in
// app mode (http://supervisor/core/websocket).
var plaintextHosts = map[string]bool{"localhost": true, "supervisor": true}

// Config configures a Client.
type Config struct {
	// URL of the WebSocket API, e.g. ws://supervisor/core/websocket or
	// wss://ha.example.org/api/websocket.
	URL string
	// Token is the access token of Home-Mandate's own HA user (or SUPERVISOR_TOKEN).
	Token Secret
	// RootCAs replaces the system roots for wss:// when set.
	RootCAs *x509.CertPool
	// Logger receives connection events; nothing is logged when nil.
	Logger *slog.Logger
	// OnConnect, if set, runs in its own goroutine after every successful
	// authentication, e.g. to reload state that may have changed while disconnected.
	// Its context ends with the connection.
	OnConnect func(ctx context.Context)

	ReadLimit    int64 // maximum size of one message in bytes
	AuthTimeout  time.Duration
	PingInterval time.Duration
	PingTimeout  time.Duration
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

// Client is a connection to Home Assistant. Create it with New and start it with Run.
type Client struct {
	cfg        Config
	log        *slog.Logger
	httpClient *http.Client

	// writeSem (capacity 1) serializes id assignment and writes, because HA rejects ids
	// that do not increase. A channel instead of a mutex lets waiting honour ctx.
	writeSem chan struct{}
	nextID   int64
	attempts atomic.Int64 // connection attempts, for tests

	mu        sync.Mutex
	conn      *websocket.Conn // nil while disconnected
	gen       uint64          // incremented for every authenticated connection
	pending   map[int64]chan message
	subs      map[*Subscription]struct{}
	subByID   map[int64]*Subscription
	changed   chan struct{} // closed and replaced on every state change
	permanent error
	started   bool
}

// New validates cfg and returns a client that is not yet connected.
func New(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("%w: empty access token", ErrInvalidConfig)
	}
	if err := validateURL(cfg.URL); err != nil {
		return nil, err
	}
	cfg = withDefaults(cfg)
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{
		cfg:        cfg,
		log:        log,
		httpClient: newHTTPClient(cfg.URL, cfg.RootCAs),
		writeSem:   make(chan struct{}, 1),
		pending:    map[int64]chan message{},
		subs:       map[*Subscription]struct{}{},
		subByID:    map[int64]*Subscription{},
		changed:    make(chan struct{}),
	}, nil
}

func withDefaults(cfg Config) Config {
	setDefault := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	if cfg.ReadLimit <= 0 {
		cfg.ReadLimit = DefaultReadLimit
	}
	setDefault(&cfg.AuthTimeout, DefaultAuthTimeout)
	setDefault(&cfg.PingInterval, DefaultPingInterval)
	setDefault(&cfg.PingTimeout, DefaultPingTimeout)
	setDefault(&cfg.ReconnectMin, DefaultReconnectMin)
	setDefault(&cfg.ReconnectMax, DefaultReconnectMax)
	cfg.ReconnectMax = max(cfg.ReconnectMax, cfg.ReconnectMin)
	return cfg
}

// validateURL never includes the URL in errors: it may contain credentials.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.Opaque != "" || u.Hostname() == "" {
		return ErrInvalidURL
	}
	switch u.Scheme {
	case "wss":
		return nil
	case "ws":
		host := u.Hostname()
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() || plaintextHosts[host] {
			return nil
		}
		return ErrInsecureURL
	default:
		return ErrInvalidURL
	}
}

// newHTTPClient refuses redirects (they could leave the configured host or downgrade to
// ws://), ignores proxy settings and requires TLS 1.3. For ws:// it dials only addresses
// that are local after name resolution.
func newHTTPClient(rawURL string, roots *x509.CertPool) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots},
	}
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "ws" {
		transport.DialContext = plaintextDial
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// plaintextDial resolves the host and connects only to an allowed local address, so a
// name like "localhost" or "supervisor" cannot be pointed elsewhere via DNS.
func plaintextDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	var errs []error
	for _, ip := range ips {
		if !plaintextAllowed(host, ip.IP) {
			continue
		}
		// Try every allowed address, as net.Dialer does: "localhost" may resolve to ::1
		// first while the server listens on 127.0.0.1 only.
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, fmt.Errorf("%w: %s does not resolve to a local address", ErrInsecureURL, host)
}

// plaintextAllowed accepts loopback addresses, and private addresses for the Supervisor,
// which runs on the Docker network of Home Assistant OS.
func plaintextAllowed(host string, ip net.IP) bool {
	return ip.IsLoopback() || host == "supervisor" && ip.IsPrivate()
}

// Run connects and keeps the connection alive until ctx is cancelled or Home Assistant
// rejects the access token. It returns ctx.Err() or ErrAuthInvalid.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return ErrAlreadyRun
	}
	c.started = true
	c.mu.Unlock()

	defer c.setPermanentIfUnset(ErrStopped)

	delay := c.cfg.ReconnectMin
	for {
		connectedFor, err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrAuthInvalid) {
			c.log.Error("home assistant rejected the access token, not retrying")
			c.setPermanent(err)
			return err
		}
		if connectedFor >= stableSession {
			delay = c.cfg.ReconnectMin
		}
		wait := jitter(delay)
		c.log.Warn("home assistant connection lost", "error", err, "retry_in", wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		delay = nextBackoff(delay, c.cfg.ReconnectMax)
	}
}

// WaitReady blocks until the client is connected. It returns ErrAuthInvalid if the
// token was rejected and ErrStopped once Run has returned.
func (c *Client) WaitReady(ctx context.Context) error {
	for {
		c.mu.Lock()
		conn, permanent, changed := c.conn, c.permanent, c.changed
		c.mu.Unlock()
		if permanent != nil {
			return permanent
		}
		if conn != nil {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// session runs one connection: dial, authenticate, serve until it breaks. It reports
// how long the connection was usable.
func (c *Client) session(ctx context.Context) (connectedFor time.Duration, err error) {
	c.attempts.Add(1)
	conn, err := c.dial(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(c.cfg.ReadLimit)
	if err := c.authenticate(ctx, conn); err != nil {
		return 0, err
	}
	start := time.Now()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.connected(conn)
	c.log.Info("connected to home assistant")

	var wg sync.WaitGroup
	readErr := make(chan error, 1)
	wg.Go(func() { readErr <- c.readLoop(sctx, conn) })
	wg.Go(func() { c.resubscribe(sctx) })
	wg.Go(func() { c.pingLoop(sctx, conn) })
	if c.cfg.OnConnect != nil {
		wg.Go(func() { c.cfg.OnConnect(sctx) })
	}

	err = <-readErr
	cancel()
	_ = conn.CloseNow()
	c.disconnected()
	wg.Wait()
	return time.Since(start), err
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, c.cfg.AuthTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(dctx, c.cfg.URL, &websocket.DialOptions{HTTPClient: c.httpClient})
	if err != nil {
		return nil, fmt.Errorf("home assistant: connect: %w", err)
	}
	return conn, nil
}

func (c *Client) authenticate(ctx context.Context, conn *websocket.Conn) error {
	actx, cancel := context.WithTimeout(ctx, c.cfg.AuthTimeout)
	defer cancel()

	m, err := readMessage(actx, conn)
	if err != nil {
		return fmt.Errorf("home assistant: authenticate: %w", err)
	}
	if m.Type != typeAuthRequired {
		return fmt.Errorf("%w: expected auth_required, got %q", ErrProtocol, m.Type)
	}
	// The token is written only here; Secret redacts itself everywhere else.
	auth, err := json.Marshal(map[string]string{"type": "auth", "access_token": string(c.cfg.Token)})
	if err != nil {
		return fmt.Errorf("home assistant: authenticate: %w", err)
	}
	if err := conn.Write(actx, websocket.MessageText, auth); err != nil {
		return fmt.Errorf("home assistant: authenticate: %w", err)
	}
	m, err = readMessage(actx, conn)
	if err != nil {
		return fmt.Errorf("home assistant: authenticate: %w", err)
	}
	switch m.Type {
	case typeAuthOK:
		return nil
	case typeAuthInvalid:
		return ErrAuthInvalid
	default:
		return fmt.Errorf("%w: expected auth_ok, got %q", ErrProtocol, m.Type)
	}
}

func readMessage(ctx context.Context, conn *websocket.Conn) (message, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return message{}, err
	}
	if typ != websocket.MessageText {
		return message{}, fmt.Errorf("%w: binary message", ErrProtocol)
	}
	return decodeMessage(data)
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		m, err := readMessage(ctx, conn)
		if err != nil {
			return err
		}
		c.dispatch(m)
	}
}

// dispatch hands results to waiting requests and events to subscriptions. Event
// handlers run on the read loop and must return quickly.
func (c *Client) dispatch(m message) {
	switch m.Type {
	case typeResult, typePong:
		c.mu.Lock()
		ch, ok := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ok {
			ch <- m // buffered; each channel receives at most one message
		}
	case typeEvent:
		c.mu.Lock()
		sub := c.subByID[m.ID]
		c.mu.Unlock()
		if sub == nil {
			return
		}
		var e Event
		if err := json.Unmarshal(m.Event, &e); err != nil || e.EventType != sub.eventType {
			c.log.Warn("ignored malformed home assistant event", "event_type", sub.eventType)
			return
		}
		sub.handler(e) // may still run once after Unsubscribe returned
	default:
		c.log.Debug("ignored home assistant message", "type", m.Type)
	}
}

func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pctx, cancel := context.WithTimeout(ctx, c.cfg.PingTimeout)
		_, err := c.request(pctx, command{Type: "ping"})
		cancel()
		if err != nil && ctx.Err() == nil {
			c.log.Warn("home assistant did not answer ping", "error", err)
			_ = conn.CloseNow()
			return
		}
	}
}

func (c *Client) connected(conn *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
	c.gen++
	c.notifyLocked()
}

// disconnected fails all requests in flight; they are never sent again.
func (c *Client) disconnected() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = nil
	for _, ch := range c.pending {
		close(ch)
	}
	c.pending = map[int64]chan message{}
	c.subByID = map[int64]*Subscription{}
	c.notifyLocked()
}

func (c *Client) setPermanent(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permanent = err
	c.notifyLocked()
}

func (c *Client) setPermanentIfUnset(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.permanent == nil {
		c.permanent = err
		c.notifyLocked()
	}
}

func (c *Client) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// errSkipped means onID declined to send; see Client.send.
var errSkipped = errors.New("home assistant: request skipped")

// request sends an allowlisted command and waits for its result.
func (c *Client) request(ctx context.Context, cmd command) (json.RawMessage, error) {
	return c.send(ctx, cmd, nil)
}

// send is request with a hook: onID runs with c.mu held once the id is known and may
// return false to cancel the request before it is written.
func (c *Client) send(ctx context.Context, cmd command, onID func(id int64, gen uint64) bool) (json.RawMessage, error) {
	if err := checkAllowed(cmd); err != nil {
		return nil, err
	}
	// The request belongs to the connection that is current now; if that one is gone by
	// the time it can be written, it fails instead of going out on a new connection.
	c.mu.Lock()
	conn, gen := c.conn, c.gen
	c.mu.Unlock()
	if conn == nil {
		return nil, ErrDisconnected
	}
	id, ch, err := c.write(ctx, gen, cmd, onID)
	if err != nil {
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, ErrDisconnected
		}
		if m.Type == typePong {
			return nil, nil
		}
		if !m.Success {
			if m.Error == nil {
				return nil, &CommandError{Code: "unknown_error"}
			}
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *Client) write(ctx context.Context, gen uint64, cmd command, onID func(int64, uint64) bool) (int64, chan message, error) {
	select {
	case c.writeSem <- struct{}{}:
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
	defer func() { <-c.writeSem }()
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}

	c.mu.Lock()
	conn := c.conn
	if conn == nil || c.gen != gen {
		c.mu.Unlock()
		return 0, nil, ErrDisconnected
	}
	c.nextID++
	id := c.nextID
	if onID != nil && !onID(id, gen) {
		c.mu.Unlock()
		return 0, nil, errSkipped
	}
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	data, err := encodeCommand(id, cmd)
	if err != nil {
		c.forget(id)
		return 0, nil, fmt.Errorf("home assistant: encode %s: %w", cmd.Type, err)
	}
	// Not the caller's ctx: cancelling a write closes the connection.
	wctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, data); err != nil {
		c.forget(id)
		return 0, nil, fmt.Errorf("%w: %w", ErrDisconnected, err)
	}
	return id, ch, nil
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}
