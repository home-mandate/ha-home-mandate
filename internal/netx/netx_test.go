// SPDX-License-Identifier: AGPL-3.0-or-later

package netx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"syscall"
	"testing"
	"time"
)

func TestCheckAddressAllowsOnlyPublicAddresses(t *testing.T) {
	for addr, ok := range map[string]bool{
		"93.184.215.14:443": true, "[2606:2800:21f:cb07:6820:80da:af6b:8b2c]:443": true,
		"127.0.0.1:443": false, "[::1]:443": false, "10.0.0.5:443": false, "192.168.1.2:443": false,
		"172.16.0.1:443": false, "169.254.169.254:443": false, "100.64.0.1:443": false, "0.0.0.0:443": false,
		"[fd00::1]:443": false, "[fe80::1]:443": false, "[::ffff:127.0.0.1]:443": false, "[64:ff9b::a00:1]:443": false,
		"224.0.0.1:443": false, "198.18.0.1:443": false, "not-an-ip:443": false, "no-port": false,
		"[::a00:1]:443": false, "[fec0::1]:443": false, "[2001:0:4136:e378:8000:63bf:3fff:fdd2]:443": false,
		"192.88.99.10:443": true, // deprecated 6to4 relay anycast, the E2E suite's public stand-in
	} {
		err := CheckAddress(addr, nil)
		if (err == nil) != ok || (err != nil && !errors.Is(err, ErrNotPublic)) {
			t.Errorf("%s: %v", addr, err)
		}
	}
	if err := CheckAddress("127.0.0.1:443", func(netip.Addr) bool { return true }); err != nil {
		t.Errorf("own rule ignored: %v", err)
	}
}

// TestClientRefusesLoopback: the default client does not reach a server on loopback,
// whatever name points there.
func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("loopback server was contacted")
	}))
	defer srv.Close()
	c := NewClient(Config{Timeout: 5 * time.Second})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(req); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("loopback: %v", err)
	}
}

func TestClientFollowsNoRedirectAndTrustsOnlyItsRoots(t *testing.T) {
	srv := httptest.NewTLSServer(http.RedirectHandler("https://elsewhere.example/", http.StatusFound))
	defer srv.Close()
	allowAll := func(string, string, syscall.RawConn) error { return nil }
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c := NewClient(Config{Timeout: 5 * time.Second, Roots: roots, Control: allowAll})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d: the redirect was followed", resp.StatusCode)
	}
	if tr := c.Transport.(*http.Transport); tr.Proxy != nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("proxy from the environment or TLS below 1.2")
	}
	// The system roots do not include the test CA.
	if _, err := NewClient(Config{Timeout: 5 * time.Second, Control: allowAll}).Get(srv.URL); err == nil {
		t.Fatal("an untrusted certificate was accepted")
	}
}
