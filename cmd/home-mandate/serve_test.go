// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/config"
	"github.com/home-mandate/ha-home-mandate/internal/pdp"
	"github.com/home-mandate/ha-home-mandate/internal/store"
	"github.com/home-mandate/ha-home-mandate/internal/tlscert"
)

// selfSigned writes a certificate for 127.0.0.1 and hm.example.org and its key into dir.
func selfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "home-mandate test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"hm.example.org"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestListenWithTLSAcceptsOnlyTLS13(t *testing.T) {
	certFile, keyFile := selfSigned(t, t.TempDir())
	srv := &http.Server{Handler: http.NotFoundHandler()}
	ln, _, err := listen(config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile}, srv, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	for _, tc := range []struct {
		name    string
		version uint16
		ok      bool
	}{{"TLS 1.3", tls.VersionTLS13, true}, {"TLS 1.2", tls.VersionTLS12, false}} {
		// Self-signed test certificate: verification is not what this test is about.
		conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, MinVersion: tc.version, MaxVersion: tc.version})
		if err == nil {
			// The server completes the handshake lazily; a read forces it.
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			err = conn.Handshake()
			conn.Close()
		}
		if (err == nil) != tc.ok {
			t.Errorf("%s: handshake error %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestListenRefusesABrokenCertificate(t *testing.T) {
	dir := t.TempDir()
	missing := config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: filepath.Join(dir, "none.pem"), TLSKey: filepath.Join(dir, "none.key")}
	if _, _, err := listen(missing, &http.Server{}, time.Now, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("listen with a missing certificate succeeded in container mode")
	}
	broken := filepath.Join(dir, "broken.pem")
	_ = os.WriteFile(broken, []byte("not a certificate"), 0o600)
	if _, _, err := listen(config.Config{Mode: config.ModeApp, MCPAddr: "127.0.0.1:0", TLSCert: broken, TLSKey: broken}, &http.Server{}, time.Now, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("listen with a broken certificate succeeded")
	}
}

func TestListenWithoutCertificateInAppModeFallsBackToLoopback(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Mode: config.ModeApp, MCPAddr: ":0", TLSCert: filepath.Join(dir, "fullchain.pem"), TLSKey: filepath.Join(dir, "privkey.pem")}
	ln, certs, err := listen(cfg, &http.Server{}, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("port 8765 not available: %v", err)
	}
	defer ln.Close()
	if certs != nil {
		t.Error("a certificate loader without a certificate")
	}
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); host != "127.0.0.1" {
		t.Errorf("listening on %s, want loopback", ln.Addr())
	}
}

// handshake connects with the given TLS settings and returns the connection state.
func handshake(addr string, c *tls.Config) (tls.ConnectionState, error) {
	conn, err := tls.Dial("tcp", addr, c)
	if err != nil {
		return tls.ConnectionState{}, err
	}
	defer conn.Close()
	return conn.ConnectionState(), nil
}

func TestListenNegotiatesTheHybridPostQuantumKeyExchange(t *testing.T) {
	certFile, keyFile := selfSigned(t, t.TempDir())
	srv := &http.Server{Handler: http.NotFoundHandler()}
	ln, _, err := listen(config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile}, srv, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	state, err := handshake(ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, CurvePreferences: []tls.CurveID{tls.X25519MLKEM768, tls.X25519}})
	if err != nil {
		t.Fatal(err)
	}
	if state.CurveID != tls.X25519MLKEM768 {
		t.Errorf("key exchange %v, want X25519MLKEM768", state.CurveID)
	}
}

func TestListenAnswersNoPlaintext(t *testing.T) {
	certFile, keyFile := selfSigned(t, t.TempDir())
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") })}
	ln, _, err := listen(config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile}, srv, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	answer, _ := io.ReadAll(conn)
	if strings.Contains(string(answer), "secret") || strings.HasPrefix(string(answer), "HTTP/1.1 200") {
		t.Errorf("plaintext answer: %q", answer)
	}
}

func TestListenRefusesACertificateForAnotherHost(t *testing.T) {
	certFile, keyFile := selfSigned(t, t.TempDir()) // valid for 127.0.0.1 and hm.example.org
	cfg := config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile, PublicURL: "https://other.example.org:8765"}
	if _, _, err := listen(cfg, &http.Server{}, time.Now, slog.New(slog.DiscardHandler)); !errors.Is(err, tlscert.ErrHostNotCovered) {
		t.Errorf("err = %v, want ErrHostNotCovered", err)
	}
	cfg.PublicURL = "https://127.0.0.1:8765"
	ln, certs, err := listen(cfg, &http.Server{}, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if st := (&gateway{certs: certs}).tlsStatus(); !st.Present || st.ValidUntil.IsZero() || st.RenewalFailed {
		t.Errorf("status = %+v", st)
	}
}

func TestListenTakesARenewedCertificateOver(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := selfSigned(t, dir)
	srv := &http.Server{Handler: http.NotFoundHandler()}
	// The loader looks at the files at most once a minute: its clock runs ahead once set.
	var ahead atomic.Bool
	clock := func() time.Time {
		if ahead.Load() {
			return time.Now().Add(tlscert.CheckInterval)
		}
		return time.Now()
	}
	ln, _, err := listen(config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile}, srv, clock, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	insecure := &tls.Config{InsecureSkipVerify: true}
	before, err := handshake(ln.Addr().String(), insecure)
	if err != nil {
		t.Fatal(err)
	}
	renewed := t.TempDir()
	newCert, newKey := selfSigned(t, renewed)
	for src, dst := range map[string]string{newCert: certFile, newKey: keyFile} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(dst, time.Now().Add(time.Second), time.Now().Add(time.Second))
	}
	ahead.Store(true)
	after, err := handshake(ln.Addr().String(), insecure)
	if err != nil {
		t.Fatal(err)
	}
	if before.PeerCertificates[0].Equal(after.PeerCertificates[0]) {
		t.Error("the listener still serves the previous certificate")
	}
}

func TestWithOAuth(t *testing.T) {
	c := newCLI(t)
	s, err := openStore(context.Background(), c.envVars["HM_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	logger := slog.New(slog.DiscardHandler)

	// Without a public URL, only the MCP endpoint is served.
	as, h, err := withOAuth(s, mcpHandler, ui, "", logger)
	if err != nil || as != nil {
		t.Fatal(as, err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("OAuth off: %d", rec.Code)
	}

	s.cfg = config.Config{PublicURL: "https://hm.example.org", HABrowserURL: "https://ha.example.org",
		HAHTTPURL: "https://ha.example.org", HAURL: "wss://ha.example.org/api/websocket"}
	as, h, err = withOAuth(s, mcpHandler, ui, "https://hm.example.org/mcp", logger)
	if err != nil || as == nil {
		t.Fatal(as, err)
	}
	for path, want := range map[string]int{"/.well-known/oauth-authorization-server": http.StatusOK, "/mcp": http.StatusTeapot,
		"/ui": http.StatusAccepted, "/ui/": http.StatusAccepted, "/ui/api/session": http.StatusAccepted, "/uix": http.StatusNotFound} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}

	s.cfg.HAHTTPURL = "http://ha.example.org" // plaintext on the LAN
	if _, _, err := withOAuth(s, mcpHandler, ui, "https://hm.example.org/mcp", logger); err == nil {
		t.Error("plaintext Home Assistant accepted")
	}

	// In app mode Home Assistant is reached in plaintext on the Supervisor's network.
	options := `{"approval_timeout_seconds":120,"public_url":"https://hm.example.org","ha_browser_url":"https://ha.example.org"}`
	s.cfg, err = config.Load(func(k string) string { return map[string]string{"SUPERVISOR_TOKEN": "s"}[k] },
		func(string) ([]byte, error) { return []byte(options), nil })
	if err != nil {
		t.Fatal(err)
	}
	if as, _, err := withOAuth(s, mcpHandler, ui, "https://hm.example.org/mcp", logger); err != nil || as == nil {
		t.Errorf("app mode with public_url: %v", err)
	}
}

func TestPublicHostNeverTurnsTheCheckOff(t *testing.T) {
	if host, err := publicHost(""); host != "" || err != nil {
		t.Errorf("no public URL: %q, %v", host, err)
	}
	if host, err := publicHost("https://hm.example.org:8765"); host != "hm.example.org" || err != nil {
		t.Errorf("host = %q, %v", host, err)
	}
	for _, bad := range []string{"://broken", "https://", "%zz"} {
		if _, err := publicHost(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestDirectModeNeedsContainerModeTLSAndHTTPS(t *testing.T) {
	certFile, keyFile := selfSigned(t, t.TempDir())
	certs, err := tlscert.New(tlscert.Config{CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		cfg   config.Config
		certs *tlscert.Loader
		want  bool
	}{
		{"container, certificate, https", config.Config{Mode: config.ModeContainer, PublicURL: "https://hm.example.org:8765"}, certs, true},
		{"no certificate", config.Config{Mode: config.ModeContainer, PublicURL: "https://hm.example.org:8765"}, nil, false},
		{"plaintext public URL", config.Config{Mode: config.ModeContainer, PublicURL: "http://localhost:8765"}, certs, false},
		{"no public URL", config.Config{Mode: config.ModeContainer}, certs, false},
		{"app mode: Ingress", config.Config{Mode: config.ModeApp, PublicURL: "https://hm.example.org:8765"}, certs, false},
		{"behind a proxy, no certificate", config.Config{Mode: config.ModeContainer, PublicURL: "https://hm.example.org",
			Proxy: netip.MustParseAddr("192.0.2.10")}, nil, true},
		{"app mode behind a proxy", config.Config{Mode: config.ModeApp, PublicURL: "https://hm.example.org",
			Proxy: netip.MustParseAddr("192.0.2.10")}, nil, false},
	} {
		if got := directMode(tc.cfg, tc.certs); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	g := &gateway{}
	rec := httptest.NewRecorder()
	g.serveDirect(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("direct mode off: %d", rec.Code)
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	g.direct = h
	rec = httptest.NewRecorder()
	g.serveDirect(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusAccepted {
		t.Errorf("direct mode on: %d", rec.Code)
	}
}

func TestWriteTimeoutCoversTheApprovalWait(t *testing.T) {
	if got := writeTimeout(2 * time.Minute); got != 150*time.Second {
		t.Errorf("writeTimeout(2m) = %v", got)
	}
	if got := writeTimeout(10 * time.Second); got != time.Minute {
		t.Errorf("writeTimeout(10s) = %v", got)
	}
}

// The gateway that serve starts serves the authorization server next to /mcp; checked on
// newGateway itself, not only on withOAuth.
func TestNewGatewayServesOAuth(t *testing.T) {
	c := newCLI(t)
	s, err := openStore(context.Background(), c.envVars["HM_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	s.cfg = config.Config{Mode: config.ModeContainer, HAURL: "ws://localhost:1/api/websocket", HAToken: "t",
		MCPAddr: "127.0.0.1:0", PublicURL: "http://localhost:8765", HABrowserURL: "http://localhost:1",
		HAHTTPURL: "http://localhost:1", ApprovalTimeout: 2 * time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g, err := newGateway(ctx, s, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer g.listener.Close()
	for path, want := range map[string]int{"/.well-known/oauth-authorization-server": http.StatusOK, "/mcp": http.StatusUnauthorized} {
		rec := httptest.NewRecorder()
		g.server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	if g.server.WriteTimeout != 150*time.Second {
		t.Errorf("write timeout %v", g.server.WriteTimeout)
	}
}

// A restart must not hand an agent a fresh rate limit (SPEC-v0 section 11.2).
func TestRestoredLimiterCountsTheLastHour(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), "household:t")
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	log.SetClock(func() time.Time { return now.Add(-10 * time.Minute) })
	for range 2 {
		if _, err := log.Append(ctx, audit.Entry{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: "hm-client:a"},
			Request:    &audit.Request{Time: now, Resource: audit.Resource{EntityID: "light.x"}, Action: "turn_on"},
			Evaluation: &audit.Evaluation{Decision: "deny", Reason: "no_match"},
			Result:     &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}}); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := restoredLimiter(ctx, log, func() time.Time { return now }, logger)
	a := pdp.RateKey("hm-client:a", "")
	if !limiter.Allow(a, 3) {
		t.Error("third request within the hour denied")
	}
	if limiter.Allow(a, 3) {
		t.Error("fourth request within the hour allowed after a restart")
	}
	if !limiter.Allow(pdp.RateKey("hm-client:b", ""), 1) {
		t.Error("another agent is affected")
	}
	// A log that cannot be read leaves the limiter empty instead of failing the start.
	_ = st.Close()
	if !restoredLimiter(ctx, log, func() time.Time { return now }, logger).Allow("hm-client:a", 1) {
		t.Error("limiter unusable after a failed restore")
	}
}
