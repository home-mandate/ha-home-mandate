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
	"sync/atomic"
	"time"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/config"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/mcp"
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
	s, err := openState(ctx, e)
	if ctx.Err() != nil {
		return exitOK // stopped during start-up
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", err)
		return exitFailure
	}
	defer s.store.Close()
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
	client, err := ha.New(ha.Config{URL: s.cfg.HAURL, Token: s.cfg.HAToken, Logger: logger, OnConnect: g.onConnect})
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
	addr, err := decider.ListenAndServe(ctx, "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	logger.Info("PDP listening on loopback", "addr", addr)

	gw := mcp.New(mcp.Config{Agents: s.agents, PDP: decider, Catalog: g.catalog, HA: client,
		Limiter: ratelimit.New(nil), Audit: s.log, Logger: logger, Version: version})
	g.server = &http.Server{Handler: gw.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn)}
	if g.listener, err = listen(s.cfg, g.server, logger); err != nil {
		return nil, err
	}
	return g, nil
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
	go g.catalog.Run(ctx, catalogDebounce)
	go g.retention(ctx)
	go func() {
		if err := g.server.Serve(g.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.logger.Error("MCP endpoint stopped", "error", err)
			cancel()
		}
	}()
	g.logger.Info("home-mandate started", "version", version, "mode", g.state.cfg.Mode, "household", g.state.household)

	err := g.client.Run(ctx)
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer stop()
	_ = g.server.Shutdown(shutdownCtx)
	if errors.Is(err, ha.ErrAuthInvalid) {
		g.logger.Error("Home Assistant rejected the access token")
		return exitFailure
	}
	return exitOK
}

// retention truncates the audit log to 30 days, at start and then daily.
func (g *gateway) retention(ctx context.Context) {
	actor := audit.Actor{Kind: audit.ActorSystem, ID: "retention"}
	for {
		if n, err := g.state.log.Truncate(ctx, time.Now().Add(-retention), actor); err != nil {
			g.logger.Error("audit log retention failed", "error", err)
		} else if n > 0 {
			g.logger.Info("audit log truncated", "entries", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retentionEvery):
		}
	}
}
