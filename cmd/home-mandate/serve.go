// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/config"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/mcp"
	"github.com/home-mandate/home-mandate/internal/oauth"
	"github.com/home-mandate/home-mandate/internal/pdp"
	"github.com/home-mandate/home-mandate/internal/ratelimit"
)

const (
	catalogDebounce = 500 * time.Millisecond
	retention       = 30 * 24 * time.Hour
	retentionEvery  = 24 * time.Hour
	shutdownTimeout = 5 * time.Second
)

// registryEvents keep the catalog current (ARCHITECTURE section 11.2).
var registryEvents = []string{"entity_registry_updated", "device_registry_updated", "area_registry_updated"}

// serve runs the gateway until ctx ends. It refuses to start with a broken audit log
// and ends with exitFailure when Home Assistant rejects the access token.
func serve(ctx context.Context, e env) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // also stops what a failed start-up already started
	cfg, err := config.Load(e.getenv, e.readFile)
	if err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", err)
		return exitFailure
	}
	s, err := openStore(ctx, cfg.DataDir)
	if ctx.Err() != nil {
		return exitOK // stopped during start-up
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", err)
		return exitFailure
	}
	defer s.store.Close()
	s.cfg = cfg
	logger := slog.New(slog.NewJSONHandler(e.stderr, &slog.HandlerOptions{Level: s.cfg.LogLevel}))

	if r, err := s.log.Verify(ctx); err != nil || !r.Valid {
		logger.Error("audit log is broken, not starting", "broken_at", r.BrokenAt, "error", err)
		return exitFailure
	}
	g, err := newGateway(ctx, s, logger)
	if err != nil {
		logger.Error("cannot start", "error", err)
		return exitFailure
	}
	return g.run(ctx)
}

type gateway struct {
	state    *state
	logger   *slog.Logger
	client   *ha.Client
	catalog  *catalog.Catalog
	timeZone atomic.Value // string; empty until Home Assistant answered get_config
	server   *http.Server
	listener net.Listener
}

func newGateway(ctx context.Context, s *state, logger *slog.Logger) (*gateway, error) {
	g := &gateway{state: s, logger: logger}
	g.timeZone.Store("")
	client, err := ha.New(ha.Config{URL: s.cfg.HAURL, Token: s.cfg.HAToken, RootCAs: s.cfg.HARootCAs, Logger: logger,
		OnConnect: g.onConnect, OnDisconnect: g.onDisconnect})
	if err != nil {
		return nil, err
	}
	g.client = client
	g.catalog = catalog.New(client, logger)
	for _, event := range append([]string{ha.EventStateChanged}, registryEvents...) {
		if _, err := client.SubscribeEvents(ctx, event, g.catalog.HandleEvent); err != nil {
			return nil, fmt.Errorf("subscribe %s: %w", event, err)
		}
	}

	decider := pdp.New(pdp.Config{Principal: s.household, Mandates: s.mandates, Catalog: g.catalog, TimeZone: g.householdTimeZone})
	if s.cfg.PDPAddr != "" { // the AuthZEN endpoint for other gateways is opt-in
		addr, err := decider.ListenAndServe(ctx, s.cfg.PDPAddr)
		if err != nil {
			return nil, err
		}
		logger.Info("PDP listening on loopback", "addr", addr)
	}

	resource, metadata := "", ""
	if s.cfg.PublicURL != "" {
		resource, metadata = s.cfg.PublicURL+mcp.Path, s.cfg.PublicURL+"/.well-known/oauth-protected-resource"+mcp.Path
	}
	gw := mcp.New(mcp.Config{Resource: resource, ResourceMetadataURL: metadata, Agents: s.agents, PDP: decider, Catalog: g.catalog, HA: client,
		Limiter: ratelimit.New(nil), Audit: s.log, Logger: logger, Version: version})
	g.server = &http.Server{Handler: gw.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn)}
	if g.listener, err = listen(s.cfg, g.server, logger); err != nil {
		return nil, err
	}
	return g, nil
}

// withOAuth adds the authorization server next to the MCP endpoint when a public URL is
// configured; without one, OAuth is off and every token is refused.
func withOAuth(s *state, mcpHandler http.Handler, resource string, logger *slog.Logger) (http.Handler, error) {
	if s.cfg.PublicURL == "" {
		logger.Warn("no public URL configured: OAuth is off, agents cannot be admitted")
		return mcpHandler, nil
	}
	signIn, err := oauth.NewHASignIn(oauth.HASignInConfig{PublicURL: s.cfg.PublicURL, BrowserURL: s.cfg.HABrowserURL,
		HTTPURL: s.cfg.HAHTTPURL, WebSocketURL: s.cfg.HAURL, Roots: s.cfg.HARootCAs})
	if err != nil {
		return nil, err
	}
	as := oauth.New(oauth.Config{PublicURL: s.cfg.PublicURL, Resource: resource, SignIn: signIn,
		Clients: oauth.NewCIMDResolver(nil), Admission: s.admission, Tokens: s.agents, Audit: s.log, Logger: logger})
	mux := http.NewServeMux()
	mux.Handle(mcp.Path, mcpHandler)
	mux.Handle("/", as.Handler())
	return mux, nil
}

// listen opens the MCP listener: TLS 1.3 when a certificate is configured, otherwise
// loopback only (decision 1). In app mode a missing certificate falls back to loopback.
func listen(cfg config.Config, srv *http.Server, logger *slog.Logger) (net.Listener, error) {
	addr := cfg.MCPAddr
	if cfg.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		switch {
		case err == nil:
			srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
		case cfg.Mode == config.ModeApp && errors.Is(err, fs.ErrNotExist):
			logger.Warn("no TLS certificate in /ssl, MCP endpoint only on localhost")
			addr = net.JoinHostPort("127.0.0.1", "8765")
		default:
			return nil, fmt.Errorf("load TLS certificate: %w", err)
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if srv.TLSConfig != nil {
		ln = tls.NewListener(ln, srv.TLSConfig)
	}
	logger.Info("MCP endpoint listening", "addr", ln.Addr().String(), "tls", srv.TLSConfig != nil, "path", mcp.Path)
	return ln, nil
}

func (g *gateway) householdTimeZone() string {
	return g.timeZone.Load().(string)
}

// onDisconnect stops decisions until the catalog and the time zone are reloaded.
func (g *gateway) onDisconnect() {
	g.catalog.Invalidate()
	g.timeZone.Store("")
}

// onConnect reloads what may have changed while disconnected.
func (g *gateway) onConnect(ctx context.Context) {
	g.catalog.RequestRefresh()
	cfg, err := g.client.GetConfig(ctx)
	if err != nil {
		g.logger.Warn("cannot read the Home Assistant configuration", "error", err)
		return
	}
	g.timeZone.Store(cfg.TimeZone)
}

func (g *gateway) run(ctx context.Context) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var serveFailed atomic.Bool
	wg.Go(func() { g.catalog.Run(ctx, catalogDebounce) })
	wg.Go(func() { g.retention(ctx) })
	wg.Go(func() {
		if err := g.server.Serve(g.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.logger.Error("MCP endpoint stopped", "error", err)
			serveFailed.Store(true)
			cancel()
		}
	})
	g.logger.Info("home-mandate started", "version", version, "mode", g.state.cfg.Mode, "household", g.state.household)

	err := g.client.Run(ctx)
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer stop()
	_ = g.server.Shutdown(shutdownCtx)
	wg.Wait() // nothing may use the database after this returns
	switch {
	case errors.Is(err, ha.ErrAuthInvalid):
		g.logger.Error("Home Assistant rejected the access token")
		return exitFailure
	case serveFailed.Load():
		return exitFailure
	}
	return exitOK
}

// retention truncates the audit log to 30 days and deletes expired tokens, at start and
// then daily.
func (g *gateway) retention(ctx context.Context) {
	actor := audit.Actor{Kind: audit.ActorSystem, ID: "retention"}
	for {
		if n, err := g.state.log.Truncate(ctx, time.Now().Add(-retention), actor); err != nil {
			g.logger.Error("audit log retention failed", "error", err)
		} else if n > 0 {
			g.logger.Info("audit log truncated", "entries", n)
		}
		if n, err := g.state.agents.PurgeExpiredTokens(ctx, time.Now()); err != nil {
			g.logger.Error("deleting expired tokens failed", "error", err)
		} else if n > 0 {
			g.logger.Info("expired tokens deleted", "tokens", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retentionEvery):
		}
	}
}
