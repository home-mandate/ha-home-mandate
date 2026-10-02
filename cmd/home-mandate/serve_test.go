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
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/config"
)

// selfSigned writes a certificate for 127.0.0.1 and its key into dir.
func selfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "home-mandate test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
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
	ln, err := listen(config.Config{Mode: config.ModeContainer, MCPAddr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile}, srv, slog.New(slog.DiscardHandler))
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
	if _, err := listen(missing, &http.Server{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("listen with a missing certificate succeeded in container mode")
	}
	broken := filepath.Join(dir, "broken.pem")
	_ = os.WriteFile(broken, []byte("not a certificate"), 0o600)
	if _, err := listen(config.Config{Mode: config.ModeApp, MCPAddr: "127.0.0.1:0", TLSCert: broken, TLSKey: broken}, &http.Server{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("listen with a broken certificate succeeded")
	}
}

func TestListenWithoutCertificateInAppModeFallsBackToLoopback(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Mode: config.ModeApp, MCPAddr: ":0", TLSCert: filepath.Join(dir, "fullchain.pem"), TLSKey: filepath.Join(dir, "privkey.pem")}
	ln, err := listen(cfg, &http.Server{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("port 8765 not available: %v", err)
	}
	defer ln.Close()
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); host != "127.0.0.1" {
		t.Errorf("listening on %s, want loopback", ln.Addr())
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
	logger := slog.New(slog.DiscardHandler)

	// Without a public URL, only the MCP endpoint is served.
	h, err := withOAuth(s, mcpHandler, "", logger)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("OAuth off: %d", rec.Code)
	}

	s.cfg = config.Config{PublicURL: "https://hm.example.org", HABrowserURL: "https://ha.example.org",
		HAHTTPURL: "https://ha.example.org", HAURL: "wss://ha.example.org/api/websocket"}
	h, err = withOAuth(s, mcpHandler, "https://hm.example.org/mcp", logger)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/.well-known/oauth-authorization-server": http.StatusOK, "/mcp": http.StatusTeapot} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}

	s.cfg.HAHTTPURL = "http://ha.example.org" // plaintext on the LAN
	if _, err := withOAuth(s, mcpHandler, "https://hm.example.org/mcp", logger); err == nil {
		t.Error("plaintext Home Assistant accepted")
	}
}
