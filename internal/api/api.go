// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api is the JSON API of the local UI (contract: web/src/lib/api/types.ts) and
// serves the UI itself. It is reachable only through Home Assistant Ingress: every
// request must come from the Supervisor (172.30.32.2; in container mode the configured
// proxy), carry the Home Assistant user the
// Supervisor set (X-Remote-User-Id), and that user must be an administrator now. Writes
// also need the CSRF token of the session, Sec-Fetch-Site: same-origin and a JSON body
// of at most 64 KiB without unknown fields. Answers carry an error code and never
// internal details. Administration is never reachable over MCP: this package is served
// on the Ingress listener only.
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/oauth"
	"github.com/home-mandate/home-mandate/internal/store"
)

// Interfaces to the rest of Home-Mandate, as far as the API uses them.
type (
	// HA is what the API asks Home Assistant.
	HA interface {
		ListUsers(ctx context.Context) ([]ha.AuthUser, error)
		GetStates(ctx context.Context) ([]ha.State, error)
		ListEntities(ctx context.Context) ([]ha.EntityEntry, error)
		ListDevices(ctx context.Context) ([]ha.Device, error)
		NotifyServices(ctx context.Context) ([]string, error)
		Notify(ctx context.Context, service string, n ha.Notification) error
	}
	// Catalog is the device catalog.
	Catalog interface {
		Ready() bool
		All() []catalog.Device
		Areas() []catalog.Area
		Lookup(entityID string) (catalog.Device, bool)
	}
	// Approvals are the open approval requests.
	Approvals interface {
		Open() []approval.Open
		Answer(ctx context.Context, id, user string, approve bool) (approval.Result, error)
		CancelAgent(clientID string) int
		CancelAll() int
		Withdraw(userID string) int
	}
	// Pairing admits agents by pairing code (decision D5).
	Pairing interface {
		Check(ctx context.Context, session, code string) (oauth.PairingCandidate, error)
		Approve(ctx context.Context, session string, a oauth.PairingApproval) (agent.Agent, error)
		Deny(ctx context.Context, session, code, pairingID string) error
	}
)

// Status is the state of the gateway that the API reports.
type Status struct {
	HAConnected bool
	HASince     time.Time // when the connection was made or lost
	HAVersion   string
	ServiceUser string // Home-Mandate's own Home Assistant user ID
	TimeZone    string // household time zone; empty until Home Assistant answered
	Language    string // household language
	Units       map[string]string
}

// Config wires the API.
type Config struct {
	Store     *store.Store
	Log       *audit.Log
	Agents    *agent.Store
	Mandates  *mandate.Store
	Admission *admission.Store
	Approvers *approval.Approvers
	Approvals Approvals
	// Pairing is nil when OAuth is off (no public URL): agents cannot be admitted then.
	Pairing Pairing
	HA      HA
	Catalog Catalog
	// Marks stores which devices the household marked as critical.
	Marks interface {
		Set(ctx context.Context, entityID string, critical bool, by string) error
	}
	// Renames are the renamed entities a human has not resolved (catalog.Renames).
	Renames interface {
		Open() map[string][]string
		Resolve(ctx context.Context, entityID string, expected []string, resolution, by string) error
		ResolveTx(ctx context.Context, tx *sql.Tx, entityID string, expected []string, resolution, by string) (func(), error)
		Store(ctx context.Context) error
		FailingSince() time.Time
		Overflowing() bool
	}
	Status func() Status
	// UI serves everything outside /api/; nil answers 404.
	UI http.Handler
	// Proxy is the one address requests may come from: the Supervisor in app mode, the
	// configured proxy in container mode (decision U2). The zero value serves nobody.
	Proxy netip.Addr

	Principal string
	Mode      string // "app" or "container"
	Version   string
	Commit    string
	// MCPURL is the URL agents connect to, from the configuration only; empty without TLS.
	MCPURL string
	// TLS reports whether the MCP endpoint has a certificate and until when it is valid.
	TLS       func() (present bool, validUntil time.Time)
	Retention time.Duration

	Logger *slog.Logger
	Now    func() time.Time
}

// Server is the API.
type Server struct {
	cfg     Config
	users   *users
	csrfKey []byte
	limits  *limits
	hub     *hub
	chain   *chainStatus
	answers *waiters
	bell    bellSetting
	mux     *http.ServeMux

	mu       sync.Mutex
	lastSeen int64 // newest audit seq announced by the tail

	// hooks run what other packages report (new requests, written entries) one after the
	// other, outside their callers: an agent's request never waits for the UI.
	hooks chan func()
}

// hookQueue bounds the work waiting for the UI; beyond it an announcement is dropped (the
// UI reloads on every reconnect and its lists are always read from the server).
const hookQueue = 256

func (s *Server) runHooks() {
	for fn := range s.hooks {
		fn()
	}
}

// later queues fn for the hook worker without blocking.
func (s *Server) later(fn func()) {
	select {
	case s.hooks <- fn:
	default:
		s.cfg.Logger.Warn("UI announcement dropped, queue full")
	}
}

// New returns the API for cfg.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Status == nil {
		cfg.Status = func() Status { return Status{} }
	}
	if cfg.TLS == nil {
		cfg.TLS = func() (bool, time.Time) { return false, time.Time{} }
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key) // crypto/rand.Read never fails (Go ≥ 1.24)
	s := &Server{cfg: cfg, users: newUsers(cfg.HA, cfg.Now), csrfKey: key, limits: newLimits(cfg.Now), hub: newHub(),
		chain: &chainStatus{}, answers: newWaiters(), hooks: make(chan func(), hookQueue)}
	s.mux = s.routes()
	go s.runHooks() // lives as long as the process
	return s
}

// Handler serves the API under /api/ and the UI elsewhere, both only to the Supervisor.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromProxy(r, s.cfg.Proxy) {
			// Not the Supervisor: nothing, not even the UI (TESTING.md section 4, UI). Logged
			// at most once a minute, so that nobody can fill the log with it.
			if ok, _ := s.limits.allow("log:foreign", 1, time.Minute); ok {
				s.cfg.Logger.Warn("request from outside the Supervisor refused", "remote", r.RemoteAddr)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/api" || len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			s.mux.ServeHTTP(w, r)
			return
		}
		if s.cfg.UI == nil {
			http.NotFound(w, r)
			return
		}
		s.cfg.UI.ServeHTTP(w, r)
	})
}

func (s *Server) now() time.Time { return s.cfg.Now().UTC() }
