// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// Endpoints (relative to the public URL).
const (
	MetadataPath         = "/.well-known/oauth-authorization-server"
	ResourceMetadataPath = "/.well-known/oauth-protected-resource"
	AuthorizePath        = "/oauth/authorize"
	TokenPath            = "/oauth/token"
	DevicePath           = "/oauth/device_authorization"
	ConsentPath          = "/oauth/consent"
	PairPath             = "/pair"
	stylePath            = "/oauth/static/style.css"
)

const (
	maxFormBytes  = 16 << 10
	deviceGrantID = "urn:ietf:params:oauth:grant-type:device_code"
)

// Interfaces to the rest of Home-Mandate.
type (
	// Admitter admits agents and lists the templates a human picks from.
	Admitter interface {
		Admit(ctx context.Context, req admission.Request) (agent.Agent, agent.TokenPair, error)
		Templates(ctx context.Context) ([]admission.Template, error)
		TemplateDocument(ctx context.Context, name string) ([]byte, admission.Template, error)
	}
	// Refresher rotates refresh tokens.
	Refresher interface {
		Refresh(ctx context.Context, token, resource, oauthClient string) (agent.TokenPair, error)
	}
	// Auditor writes audit entries.
	Auditor interface {
		Append(ctx context.Context, e audit.Entry) (int64, error)
	}
)

// Config wires the authorization server.
type Config struct {
	// PublicURL is the issuer, e.g. https://hm.example.org:8765.
	PublicURL string
	// Resource is the only resource tokens are issued for: the MCP endpoint.
	Resource  string
	SignIn    HomeAssistant
	Clients   ClientResolver
	Admission Admitter
	Tokens    Refresher
	Audit     Auditor
	Logger    *slog.Logger
	Now       func() time.Time
}

// Server is the authorization server.
type Server struct {
	cfg      Config
	sessions *sessions
	secure   bool   // cookies only over TLS
	cookie   string // session cookie name

	// clientAddr identifies the sender of a request for per-sender limits.
	clientAddr func(*http.Request) string

	mu         sync.Mutex
	codes      map[string]*authCode
	grants     map[string]*deviceGrant
	pairing    pairingLimit
	uiFailures map[string]*uiFailures  // wrong codes per UI session (pairing.go)
	uiLocks    map[string]*sessionLock // attempts in progress per UI session
	approvers  ApproverPreview         // who may approve, for the consent page (approvers.go)
}

//go:embed pages
var pageFiles embed.FS

var pages = template.Must(template.ParseFS(pageFiles, "pages/pages.html"))

var styleSheet = func() []byte {
	b, err := pageFiles.ReadFile("pages/style.css")
	if err != nil {
		panic(err)
	}
	return b
}()

// New returns the server for cfg.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Server{cfg: cfg, sessions: newSessions(cfg.Now), codes: map[string]*authCode{},
		grants: map[string]*deviceGrant{}, uiFailures: map[string]*uiFailures{},
		uiLocks: map[string]*sessionLock{}, clientAddr: remoteHost}
	s.secure = strings.HasPrefix(cfg.PublicURL, "https://")
	s.cookie = "hm_session"
	if s.secure {
		s.cookie = "__Host-hm_session"
	}
	return s
}

// Handler serves the metadata, the OAuth endpoints and the pages.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+MetadataPath, s.metadata)
	mux.HandleFunc("GET "+ResourceMetadataPath, s.resourceMetadata)
	mux.HandleFunc("GET "+ResourceMetadataPath+"/mcp", s.resourceMetadata)
	mux.HandleFunc("GET "+AuthorizePath, s.authorize)
	mux.HandleFunc("GET "+CallbackPath, s.callback)
	mux.HandleFunc("GET "+ConsentPath, s.consentPage)
	mux.HandleFunc("POST "+ConsentPath, s.consent)
	mux.HandleFunc("POST "+TokenPath, s.token)
	mux.HandleFunc("POST "+DevicePath, s.deviceAuthorization)
	mux.HandleFunc("GET "+PairPath, s.pairPage)
	mux.HandleFunc("POST "+PairPath, s.pair)
	mux.HandleFunc("GET "+stylePath, s.style)
	return mux
}

func (s *Server) metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.cfg.PublicURL,
		"authorization_endpoint":                         s.cfg.PublicURL + AuthorizePath,
		"token_endpoint":                                 s.cfg.PublicURL + TokenPath,
		"device_authorization_endpoint":                  s.cfg.PublicURL + DevicePath,
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token", deviceGrantID},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

func (s *Server) resourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.cfg.Resource,
		"authorization_servers":    []string{s.cfg.PublicURL},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Home-Mandate",
	})
}

func (s *Server) style(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(styleSheet)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError writes an error response of the token and device endpoints (RFC 6749
// section 5.2): a code and no internal details.
func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// page is the data of a rendered page.
type page struct {
	Lang    i18n.Lang
	Title   i18n.Key
	Message i18n.Key
	Error   i18n.Key
	User    string
	CSRF    string

	Claimed, ClientID, Host, ReturnHost string
	Verified                            bool
	Name, Selected                      string
	Templates                           []consentTemplate
}

// T translates key with name/value pairs as arguments.
func (p page) T(key i18n.Key, kv ...string) string {
	args := i18n.Args{}
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i]] = kv[i+1]
	}
	return i18n.T(p.Lang, key, args)
}

// render writes a page with strict security headers. formTarget, if set, is an origin
// a form on the page may lead to (the agent's redirect URI after the consent).
func (s *Server) render(w http.ResponseWriter, status int, name string, p page, formTarget string) {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, p); err != nil {
		s.cfg.Logger.Error("rendering a page failed", "page", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	formAction := "'self'"
	if formTarget != "" {
		formAction += " " + formTarget
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action "+formAction+
		"; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	// same-origin, not no-referrer: under no-referrer browsers send "Origin: null" with a
	// form post, which the same-origin check of the consent and pairing forms refuses.
	// Towards other origins (the agent, Home Assistant) no referrer is sent either way.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// message renders a page with one message.
func (s *Server) message(w http.ResponseWriter, r *http.Request, status int, title, msg i18n.Key) {
	s.render(w, status, "message", page{Lang: language(r), Title: title, Message: msg}, "")
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, msg i18n.Key) {
	s.message(w, r, status, i18n.PageErrorTitle, msg)
}

// language picks the page language from Accept-Language, in the order given.
func language(r *http.Request) i18n.Lang {
	var tags []string
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		tags = append(tags, tag)
	}
	return i18n.Pick(tags...)
}

// singleValues parses a query or form and rejects repeated parameters (RFC 6749
// section 3.1 and 3.2).
func singleValues(values url.Values) (map[string]string, bool) {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) != 1 {
			return nil, false
		}
		out[k] = v[0]
	}
	return out, true
}

// parseForm reads a bounded application/x-www-form-urlencoded body.
func parseForm(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return nil, false
	}
	return singleValues(r.PostForm)
}

func (s *Server) setCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: value, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteLaxMode})
}

// remoteHost is the IP address of the sender, without the port.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) sessionID(r *http.Request) string {
	c, err := r.Cookie(s.cookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// sameOrigin rejects form posts from other origins; browsers send Origin on POST.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == s.cfg.PublicURL
}

// rejectUser logs a refused sign-in or pairing attempt of a human.
func (s *Server) rejectUser(ctx context.Context, userID, code string) {
	e := audit.Entry{Event: audit.EventAuthRejected,
		Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication, Error: code}}
	if userID != "" {
		e.Actor = &audit.Actor{Kind: audit.ActorUser, ID: userID}
	}
	if _, err := s.cfg.Audit.Append(context.WithoutCancel(ctx), e); err != nil {
		s.cfg.Logger.Error("audit log write failed", "error", err)
	}
}
