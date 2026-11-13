// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

type fakeLoader struct{ recheck bool }

func (f *fakeLoader) PublicKeySHA256(recheck bool) [sha256.Size]byte {
	f.recheck = recheck
	return sha256.Sum256([]byte("spki"))
}

func TestFileKeyAsksTheLoader(t *testing.T) {
	l := &fakeLoader{}
	k, err := FileKey{Loader: l}.Key(context.Background(), true)
	if err != nil || k != Digest(sha256.Sum256([]byte("spki"))) || !l.recheck {
		t.Fatalf("%v %v %v", k, err, l.recheck)
	}
}

// proxyServer is a TLS server whose certificate (httptest's) covers example.com.
func proxyServer(t *testing.T) (netip.AddrPort, *x509.Certificate) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	t.Cleanup(srv.Close)
	addr, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return addr, srv.Certificate()
}

func TestProxyKeyReadsTheKeyTheProxyServes(t *testing.T) {
	addr, cert := proxyServer(t)
	k, err := ProxyKey{Addr: addr, ServerName: "example.com", Timeout: 5 * time.Second}.Key(context.Background(), false)
	if err != nil || k != Digest(sha256.Sum256(cert.RawSubjectPublicKeyInfo)) {
		t.Fatalf("%v %v", k, err)
	}
}

func TestProxyKeyRefuses(t *testing.T) {
	addr, _ := proxyServer(t)
	// A certificate that does not cover the name is not the gateway's.
	if _, err := (ProxyKey{Addr: addr, ServerName: "home.example.org", Timeout: 5 * time.Second}).Key(context.Background(), false); !errors.Is(err, ErrKey) {
		t.Fatalf("other name: %v", err)
	}
	// Nothing listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := netip.MustParseAddrPort(ln.Addr().String())
	_ = ln.Close()
	if _, err := (ProxyKey{Addr: closed, ServerName: "example.com", Timeout: time.Second}).Key(context.Background(), false); !errors.Is(err, ErrKey) {
		t.Fatalf("closed port: %v", err)
	}
	// A server that speaks no TLS.
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	if _, err := (ProxyKey{Addr: netip.MustParseAddrPort(plain.Listener.Addr().String()), ServerName: "example.com",
		Timeout: time.Second}).Key(context.Background(), false); !errors.Is(err, ErrKey) {
		t.Fatalf("plain HTTP: %v", err)
	}
}
