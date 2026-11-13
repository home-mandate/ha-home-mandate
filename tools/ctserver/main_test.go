// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
)

func writeCert(t *testing.T, dir, file string, names ...string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The gateway's own sources read what the stand-in serves: both find the name, the
// wildcard of its parent, and continue after their cursors.
func TestSourcesReadTheStandIn(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "1.pem", "hm.e2e.example.net")
	writeCert(t, dir, "2.pem", "*.e2e.example.net")
	writeCert(t, dir, "3.pem", "other.example.net")
	srv := httptest.NewServer(newHandler(dir))
	defer srv.Close()
	ctx := context.Background()
	client := &http.Client{Transport: toServer{srv.URL}}
	for _, src := range []ctwatch.Source{ctwatch.NewCertSpotter(client, ""), ctwatch.NewCrtSh(client)} {
		b, err := src.Fetch(ctx, "hm.e2e.example.net", "")
		if err != nil || len(b.Certs) != 2 || b.Cursor != "2" {
			t.Fatalf("%s: %d certificates, cursor %q, %v", src.Name(), len(b.Certs), b.Cursor, err)
		}
		writeCert(t, dir, "4.pem", "hm.e2e.example.net")
		if b, err = src.Fetch(ctx, "hm.e2e.example.net", b.Cursor); err != nil || len(b.Certs) != 1 || b.Cursor != "4" {
			t.Fatalf("%s after the cursor: %d, %q, %v", src.Name(), len(b.Certs), b.Cursor, err)
		}
		_ = os.Remove(filepath.Join(dir, "4.pem"))
	}
}

// toServer sends every request to the test server, whatever host it names, as
// --add-host does in the end-to-end tests.
type toServer struct{ base string }

func (s toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(s.base)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestBrokenDirectoryAndUnknownID(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(newHandler(dir))
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/?d=9")
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("unknown ID: %v %v", resp, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "x.pem"), []byte("not PEM"), 0o600)
	for _, path := range []string{"/v1/issuances?domain=a.example.net", "/?q=a.example.net&output=json"} {
		if resp, err := srv.Client().Get(srv.URL + path); err != nil || resp.StatusCode != 500 {
			t.Fatalf("%s: %v %v", path, resp, err)
		}
	}
}

func TestRunNeedsItsFlags(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-dir", "/x"}, &out); err == nil {
		t.Fatal("ran without certificate")
	}
	if err := run([]string{"-cert", "/nope", "-key", "/nope", "-dir", "/x"}, &out); err == nil {
		t.Fatal("ran with missing files")
	}
}
