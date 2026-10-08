// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testClientID = "https://cimd.example.test/agent.json"

func TestNewDocument(t *testing.T) {
	tests := []struct {
		name      string
		clientID  string
		redirects []string
		wantErr   bool
		wantPath  string
	}{
		{name: "valid", clientID: testClientID, redirects: []string{"http://127.0.0.1/callback"}, wantPath: "/agent.json"},
		{name: "two redirect URIs", clientID: "https://cimd.example.test/a/b.json",
			redirects: []string{"http://127.0.0.1/callback", "http://localhost/callback"}, wantPath: "/a/b.json"},
		{name: "no https", clientID: "http://cimd.example.test/agent.json", redirects: []string{"http://127.0.0.1/cb"}, wantErr: true},
		{name: "no path", clientID: "https://cimd.example.test", redirects: []string{"http://127.0.0.1/cb"}, wantErr: true},
		{name: "root path", clientID: "https://cimd.example.test/", redirects: []string{"http://127.0.0.1/cb"}, wantErr: true},
		{name: "query", clientID: "https://cimd.example.test/a?x=1", redirects: []string{"http://127.0.0.1/cb"}, wantErr: true},
		{name: "unparsable", clientID: "https://[::1/a", redirects: []string{"http://127.0.0.1/cb"}, wantErr: true},
		{name: "no redirect URI", clientID: testClientID, wantErr: true},
		{name: "empty redirect URI", clientID: testClientID, redirects: []string{""}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, body, err := newDocument(tt.clientID, "E2E agent", tt.redirects)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			var doc map[string]any
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatal(err)
			}
			if doc["client_id"] != tt.clientID || doc["client_name"] != "E2E agent" || doc["token_endpoint_auth_method"] != "none" ||
				len(doc["redirect_uris"].([]any)) != len(tt.redirects) {
				t.Errorf("document = %v", doc)
			}
		})
	}
}

func TestHandler(t *testing.T) {
	h, err := newHandler(testClientID, "E2E agent", []string{"http://127.0.0.1/callback"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	tests := []struct {
		name, method, path string
		wantStatus         int
		wantJSON           bool
	}{
		{"document", http.MethodGet, "/agent.json", http.StatusOK, true},
		{"head of the document", http.MethodHead, "/agent.json", http.StatusOK, true},
		{"other path", http.MethodGet, "/other.json", http.StatusNotFound, false},
		{"root", http.MethodGet, "/", http.StatusNotFound, false},
		{"post", http.MethodPost, "/agent.json", http.StatusMethodNotAllowed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, srv.URL+tt.path, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("Content-Type") == "application/json"; got != tt.wantJSON {
				t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
			}
			if tt.wantJSON && tt.method == http.MethodGet && !strings.Contains(string(body), `"client_id":"`+testClientID+`"`) {
				t.Errorf("body = %s", body)
			}
		})
	}
}

func TestNewHandlerRefusesAnInvalidClientID(t *testing.T) {
	if _, err := newHandler("ftp://x/y", "n", []string{"http://127.0.0.1/cb"}); err == nil {
		t.Fatal("no error")
	}
}

func TestRedirectList(t *testing.T) {
	var r redirectList
	if err := r.Set("http://127.0.0.1/a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("http://localhost/b"); err != nil {
		t.Fatal(err)
	}
	if r.String() != "http://127.0.0.1/a,http://localhost/b" || len(r) != 2 {
		t.Errorf("list = %v", r)
	}
}

func TestRunArguments(t *testing.T) {
	cert, key := writeCertificate(t)
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"-nope"}},
		{"no certificate", []string{"-client-id", testClientID, "-redirect-uri", "http://127.0.0.1/cb"}},
		{"invalid client ID", []string{"-cert", cert, "-key", key, "-client-id", "x", "-redirect-uri", "http://127.0.0.1/cb"}},
		{"missing key file", []string{"-cert", cert, "-key", filepath.Join(t.TempDir(), "none.pem"), "-client-id", testClientID,
			"-redirect-uri", "http://127.0.0.1/cb"}},
		{"invalid listen address", []string{"-listen", "256.0.0.1:1", "-cert", cert, "-key", key, "-client-id", testClientID,
			"-redirect-uri", "http://127.0.0.1/cb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(tt.args, io.Discard); err == nil {
				t.Fatal("no error")
			}
		})
	}
}

// TestRunServesOverTLS starts the server on a free port and fetches the document the way
// the gateway does: TLS 1.2 or later with the test CA.
func TestRunServesOverTLS(t *testing.T) {
	cert, key := writeCertificate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	errs := make(chan error, 1)
	go func() {
		errs <- run([]string{"-listen", addr, "-cert", cert, "-key", key, "-client-id", testClientID, "-name", "E2E agent",
			"-redirect-uri", "http://127.0.0.1/callback"}, io.Discard)
	}()
	pemData, _ := os.ReadFile(cert)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pemData)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "cimd.example.test", MinVersion: tls.VersionTLS12}}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-errs:
			t.Fatalf("server stopped: %v", err)
		default:
		}
		resp, err := client.Get("https://" + addr + "/agent.json")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), testClientID) {
				t.Fatalf("answer = %d %s", resp.StatusCode, body)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// writeCertificate writes a self-signed certificate for cimd.example.test.
func writeCertificate(t *testing.T) (string, string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cimd.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"cimd.example.test"},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(k)
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)
	return cert, key
}
