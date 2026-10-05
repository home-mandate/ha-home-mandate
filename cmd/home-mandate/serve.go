// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/home-mandate/home-mandate/internal/api"
	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/config"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/i18n"
	"github.com/home-mandate/home-mandate/internal/mcp"
	"github.com/home-mandate/home-mandate/internal/oauth"
	"github.com/home-mandate/home-mandate/internal/pdp"
	"github.com/home-mandate/home-mandate/internal/webui"
	"github.com/mandate-spec/mandate-spec/ratelimit"
)

const (
	catalogDebounce = 500 * time.Millisecond
	retention       = 30 * 24 * time.Hour
	retentionEvery  = 24 * time.Hour
	shutdownTimeout = 5 * time.Second
	minWriteTimeout = 60 * time.Second
	approvalSlack   = 30 * time.Second
	bellCleanup     = 10 * time.Second
)

// registryEvents keep the catalog current (ARCHITECTURE section 11.2).
var registryEvents = []string{"entity_registry_updated", "device_registry_updated", "area_registry_updated"}

// restoredLimiter returns the rate limiter filled with the requests of the last hour
// from the audit log, so that a restart does not hand every agent a fresh limit
// (SPEC-v0 section 11.2). If the log cannot be read, the limiter starts empty.
func restoredLimiter(ctx context.Context, log *audit.Log, now func() time.Time, logger *slog.Logger) *ratelimit.Limiter {
	limiter := ratelimit.New(now)
	requests, err := log.RequestsSince(ctx, now().Add(-ratelimit.Window), pdp.RateKey)
	if err != nil {
		logger.Warn("cannot restore the rate limits from the audit log", "error", err)
		return limiter
	}
	for key, times := range requests {
		limiter.Restore(key, times)
	}
	return limiter
}

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
	if err := attachSigner(ctx, s, cfg.DataDir, e.getenv); err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", err)
		return exitFailure
	}
	logger := slog.New(slog.NewJSONHandler(e.stderr, &slog.HandlerOptions{Level: s.cfg.LogLevel}))

	// A stop signal during start-up cancels ctx and makes the step in progress fail; that
	// is a clean stop, not a failed start.
	r, err := s.log.Verify(ctx)
	if ctx.Err() != nil {
		return exitOK
	}
	if err != nil || !r.Valid {
		logger.Error("audit log is broken, not starting", "broken_at", r.BrokenAt, "error", err)
		return exitFailure
	}
	n, err := s.log.IndexSearch(ctx)
	if ctx.Err() != nil {
		return exitOK
	}
	if err != nil {
		logger.Error("cannot index the audit log for the search", "error", err)
		return exitFailure
	}
	if n > 0 {
		logger.Info("audit log indexed for the search", "entries", n)
	}
	g, err := newGateway(ctx, s, logger)
	if ctx.Err() != nil {
		return exitOK
	}
	if err != nil {
		logger.Error("cannot start", "error", err)
		return exitFailure
	}
	return g.run(ctx)
}

type gateway struct {
	marks    *catalog.Marks
	renames  *catalog.Renames
	state    *state
	logger   *slog.Logger
	client   *ha.Client
	catalog  *catalog.Catalog
	api      *api.Server
	timeZone atomic.Value // string; empty until Home Assistant answered get_config
	language atomic.Value // string; household language from get_config
	self     atomic.Value // string; Home-Mandate's own Home Assistant user

	mu        sync.Mutex
	haSince   time.Time // when the connection was made or lost
	haVersion string
	units     map[string]string
	bellClean sync.Once

	server   *http.Server
	listener net.Listener
	ingress  *http.Server // the UI behind Ingress; nil without HM_INGRESS_ADDR in container mode
	ingressL net.Listener
	tlsUntil time.Time // expiry of the MCP certificate; zero without TLS
}

func newGateway(ctx context.Context, s *state, logger *slog.Logger) (*gateway, error) {
	g := &gateway{state: s, logger: logger, haSince: time.Now()}
	g.timeZone.Store("")
	g.language.Store("")
	g.self.Store("")
	client, err := ha.New(ha.Config{URL: s.cfg.HAURL, Token: s.cfg.HAToken, RootCAs: s.cfg.HARootCAs, Logger: logger,
		OnConnect: g.onConnect, OnDisconnect: g.onDisconnect})
	if err != nil {
		return nil, err
	}
	g.client = client
	g.catalog = catalog.New(client, logger)
	// The entities the household marked as critical are part of the resource directory.
	marks, err := catalog.LoadMarks(ctx, s.store.DB())
	if err != nil {
		return nil, err
	}
	// Every change of the directory is an audit entry (SPEC-v0 section 11.4).
	marks.SetRecorder(s.log)
	g.catalog.SetMarks(marks)
	g.marks = marks
	// Renames a human has not resolved keep the rules on the former IDs in force.
	renames, err := catalog.LoadRenames(ctx, s.store.DB())
	if err != nil {
		return nil, err
	}
	renames.SetRecorder(s.log)
	g.catalog.SetAliases(renames)
	g.renames = renames
	for _, event := range append([]string{ha.EventStateChanged}, registryEvents...) {
		if _, err := client.SubscribeEvents(ctx, event, g.catalog.HandleEvent); err != nil {
			return nil, fmt.Errorf("subscribe %s: %w", event, err)
		}
	}

	// The approval service and the API need each other: the API reads open requests and
	// takes answers, the service asks the API who is an administrator, whether the bell
	// is on, and tells it about new requests. Both exist before anything runs.
	// Until the API exists nobody is an administrator, the bell is off and nothing is
	// announced; Home Assistant events may arrive before newGateway returns.
	var uiAPI atomic.Pointer[api.Server]
	approvals := approval.New(approval.Config{Approvers: s.approvers, Notifier: client, Language: g.householdLanguage,
		MaxTimeout: s.cfg.ApprovalTimeout, ServiceUser: g.serviceUser, Logger: logger,
		IsAdmin: func(ctx context.Context, user string) (bool, error) {
			if a := uiAPI.Load(); a != nil {
				return a.IsAdmin(ctx, user)
			}
			return false, errors.New("starting")
		},
		Bell: client.Bell(),
		BellEnabled: func() bool {
			a := uiAPI.Load()
			return a != nil && a.BellEnabled()
		},
		OnOpened: func(o approval.Open) {
			if a := uiAPI.Load(); a != nil {
				a.ApprovalOpened(o)
			}
		}})
	if _, err := client.SubscribeEvents(ctx, ha.EventMobileAppNotificationAction, approvals.HandleEvent); err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", ha.EventMobileAppNotificationAction, err)
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
		TemperatureUnit: g.temperatureUnit,
		Limiter:         restoredLimiter(ctx, s.log, time.Now, logger), ApprovalLimit: s.cfg.ApprovalTimeout, Clock: s.log, Audit: s.log, Approvals: approvals, Logger: logger, Version: version})
	as, handler, err := withOAuth(s, gw.Handler(), resource, logger)
	if err != nil {
		return nil, err
	}
	g.server = &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: writeTimeout(s.cfg.ApprovalTimeout), IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn)}
	if g.listener, err = listen(s.cfg, g.server, logger); err != nil {
		return nil, err
	}
	if s.cfg.TLSCert != "" && g.server.TLSConfig != nil {
		g.tlsUntil = certificateExpiry(g.server.TLSConfig)
	}

	apiCfg := api.Config{Proxy: trustedProxy(ctx, s.cfg, lookupHost, logger), Store: s.store, Log: s.log, Agents: s.agents, Mandates: s.mandates, Admission: s.admission,
		Approvers: s.approvers, Approvals: approvals, HA: client, Catalog: g.catalog, Marks: g.marks, Renames: g.renames, Status: g.status, UI: webui.Handler(),
		Principal: s.household, Mode: string(s.cfg.Mode), Version: version, Commit: commit, Retention: retention,
		TLS: func() (bool, time.Time) { return !g.tlsUntil.IsZero(), g.tlsUntil }, Logger: logger}
	if as != nil {
		apiCfg.Pairing = as
	}
	// The MCP address is taken from the configuration only, never from a request header.
	if g.server.TLSConfig != nil && s.cfg.PublicURL != "" {
		apiCfg.MCPURL = s.cfg.PublicURL + mcp.Path
	}
	g.api = api.New(apiCfg)
	g.catalog.OnRefresh(func(renames []catalog.Rename) {
		directoryChanged(ctx, logger, g.marks, g.renames, g.api.DevicesChanged, renames)
	})
	if err := g.api.LoadSettings(ctx); err != nil {
		g.listener.Close()
		return nil, err
	}
	uiAPI.Store(g.api)
	s.log.OnCommit(g.api.AuditCommitted)
	if err := g.listenIngress(); err != nil {
		g.listener.Close()
		return nil, err
	}
	return g, nil
}

// listenIngress opens the listener of the UI: always in app mode (:8099), in container
// mode only with HM_INGRESS_ADDR (decision U3). Whatever the address, the API serves only
// the Supervisor (172.30.32.2). It has no write timeout: the event stream stays open;
// every API request has its own time limit.
func (g *gateway) listenIngress() error {
	addr := g.state.cfg.IngressAddr
	if addr == "" {
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ingress listener: %w", err)
	}
	g.ingressL = ln
	g.ingress = &http.Server{Handler: g.api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: slog.NewLogLogger(g.logger.Handler(), slog.LevelWarn)}
	g.logger.Info("UI listening for Ingress", "addr", ln.Addr().String())
	return nil
}

// lookupHost resolves a host name; replaced in tests.
var lookupHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// supervisorLookup bounds the start-up check of the Supervisor's address.
const supervisorLookup = 5 * time.Second

// trustedProxy is the one address the UI listener serves (decision U2). In app mode that
// is the Supervisor's fixed address; the Supervisor also names itself "supervisor" in
// every app's hosts file, and if that name does not lead to the same address the UI stays
// locked: a change in Home Assistant OS is noticed, never followed blindly.
func trustedProxy(ctx context.Context, cfg config.Config, lookup func(context.Context, string) ([]netip.Addr, error), logger *slog.Logger) netip.Addr {
	if cfg.Mode != config.ModeApp {
		return cfg.IngressProxy
	}
	ctx, cancel := context.WithTimeout(ctx, supervisorLookup)
	defer cancel()
	addrs, err := lookup(ctx, "supervisor")
	for _, a := range addrs {
		if err == nil && a.Unmap() == cfg.IngressProxy {
			return cfg.IngressProxy
		}
	}
	logger.Error("the Supervisor is not at the address the UI trusts, the UI stays locked",
		"expected", cfg.IngressProxy.String(), "found", fmt.Sprint(addrs), "error", err)
	return netip.Addr{}
}

// certificateExpiry is the end of validity of the configured certificate.
func certificateExpiry(cfg *tls.Config) time.Time {
	if len(cfg.Certificates) == 0 || len(cfg.Certificates[0].Certificate) == 0 {
		return time.Time{}
	}
	cert, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		return time.Time{}
	}
	return cert.NotAfter
}

// withOAuth adds the authorization server next to the MCP endpoint when a public URL is
// configured; without one, OAuth is off, every token is refused and nobody can pair.
func withOAuth(s *state, mcpHandler http.Handler, resource string, logger *slog.Logger) (*oauth.Server, http.Handler, error) {
	if s.cfg.PublicURL == "" {
		logger.Warn("no public URL configured: OAuth is off, agents cannot be admitted")
		return nil, mcpHandler, nil
	}
	signIn, err := oauth.NewHASignIn(oauth.HASignInConfig{PublicURL: s.cfg.PublicURL, BrowserURL: s.cfg.HABrowserURL,
		HTTPURL: s.cfg.HAHTTPURL, WebSocketURL: s.cfg.HAURL, Roots: s.cfg.HARootCAs})
	if err != nil {
		return nil, nil, err
	}
	as := oauth.New(oauth.Config{PublicURL: s.cfg.PublicURL, Resource: resource, SignIn: signIn,
		Clients: oauth.NewCIMDResolver(nil), Admission: s.admission, Tokens: s.agents, Audit: s.log, Logger: logger})
	mux := http.NewServeMux()
	mux.Handle(mcp.Path, mcpHandler)
	mux.Handle("/", as.Handler())
	return as, mux, nil
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

// temperatureUnit is the unit of temperatures in Home Assistant's service calls; empty
// until the configuration was read.
func (g *gateway) temperatureUnit() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.units["temperature"]
}

// householdLanguage is the language of approval requests for approvers without their
// own setting (decision 5: Home Assistant does not reveal a user's language).
func (g *gateway) householdLanguage() i18n.Lang {
	return i18n.Pick(g.language.Load().(string))
}

// serviceUser is Home-Mandate's own Home Assistant user, which never approves.
func (g *gateway) serviceUser() string {
	return g.self.Load().(string)
}

// status is the gateway's state for the UI.
func (g *gateway) status() api.Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	return api.Status{HAConnected: g.client.Connected(), HASince: g.haSince, HAVersion: g.haVersion, ServiceUser: g.serviceUser(),
		TimeZone: g.householdTimeZone(), Language: g.language.Load().(string), Units: maps.Clone(g.units)}
}

// writeTimeout lets a perform_action wait for an approval: the longest wait plus time
// for the action itself.
func writeTimeout(approval time.Duration) time.Duration {
	return max(minWriteTimeout, approval+approvalSlack)
}

// onDisconnect stops decisions until the catalog and the time zone are reloaded.
func (g *gateway) onDisconnect() {
	g.catalog.Invalidate()
	g.timeZone.Store("")
	g.mu.Lock()
	g.haSince = time.Now()
	g.mu.Unlock()
	if g.api != nil {
		go g.api.SystemChanged()
	}
}

// onConnect reloads what may have changed while disconnected.
func (g *gateway) onConnect(ctx context.Context) {
	g.mu.Lock()
	g.haSince = time.Now()
	g.mu.Unlock()
	g.catalog.RequestRefresh()
	defer func() {
		if g.api != nil {
			go g.api.SystemChanged()
		}
	}()
	cfg, err := g.client.GetConfig(ctx)
	if err != nil {
		g.logger.Warn("cannot read the Home Assistant configuration", "error", err)
		return
	}
	g.timeZone.Store(cfg.TimeZone)
	g.language.Store(cfg.Language)
	g.mu.Lock()
	g.haVersion, g.units = cfg.Version, maps.Clone(cfg.UnitSystem)
	g.mu.Unlock()
	if u, err := g.client.CurrentUser(ctx); err != nil {
		g.logger.Warn("cannot read Home-Mandate's own Home Assistant user", "error", err)
	} else {
		g.self.Store(u.ID)
	}
	g.bellClean.Do(func() { go g.clearBells() })
}

// clearBells removes hints of approval requests a crash left in Home Assistant's
// notification bell; requests do not survive a restart.
func (g *gateway) clearBells() {
	ctx, cancel := context.WithTimeout(context.Background(), bellCleanup)
	defer cancel()
	bell := g.client.Bell()
	ids, err := bell.Leftovers(ctx)
	if err != nil {
		g.logger.Warn("cannot list approval hints left in Home Assistant", "error", err)
		return
	}
	for _, id := range ids {
		if err := bell.Clear(ctx, id); err != nil {
			g.logger.Warn("approval hint left in Home Assistant not removed", "error", err)
		}
	}
}

func (g *gateway) run(ctx context.Context) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var serveFailed atomic.Bool
	wg.Go(func() { g.catalog.Run(ctx, catalogDebounce) })
	wg.Go(func() { g.retention(ctx) })
	// Checkpoints anchor the audit log; once a day their position goes to the approvers.
	wg.Go(func() {
		checkpointer{log: g.state.log, settings: g.state.store, now: time.Now, logger: g.logger,
			anchor: approverAnchor{approvers: g.state.approvers, notify: g.client.Notify, language: g.householdLanguage}}.run(ctx)
	})
	wg.Go(func() { g.api.RunTail(ctx) })
	wg.Go(func() { g.api.RunVerifier(ctx) })
	serveOn := func(name string, srv *http.Server, ln net.Listener) {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.logger.Error(name+" stopped", "error", err)
			serveFailed.Store(true)
			cancel()
		}
	}
	wg.Go(func() { serveOn("MCP endpoint", g.server, g.listener) })
	if g.ingress != nil {
		wg.Go(func() { serveOn("UI listener", g.ingress, g.ingressL) })
	}
	g.logger.Info("home-mandate started", "version", version, "mode", g.state.cfg.Mode, "household", g.state.household)

	err := g.client.Run(ctx)
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer stop()
	_ = g.server.Shutdown(shutdownCtx)
	if g.ingress != nil {
		_ = g.ingress.Shutdown(shutdownCtx)
		_ = g.ingress.Close() // event streams are hijacked connections; Shutdown does not wait for them
	}
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
