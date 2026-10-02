// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
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
