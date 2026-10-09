// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientValidatesTheOrigin(t *testing.T) {
	for raw, want := range map[string]error{
		"http://homeassistant:8123":      ErrInsecureURL, // container mode
		"https://ha.example.org":         nil,
		"http://localhost:8123":          nil,
		"http://127.0.0.1:8123":          nil,
		"http://ha.example.org":          ErrInsecureURL,
		"http://192.168.1.10:8123":       ErrInsecureURL,
		"ftp://ha.example.org":           ErrInvalidURL,
		"https://user:pw@ha.example.org": ErrInvalidURL,
		"https://ha.example.org/?x=1":    ErrInvalidURL,
		"":                               ErrInvalidURL,
	} {
		_, err := HTTPClient(raw, nil, Plaintext{})
		if want == nil && err != nil || want != nil && !errors.Is(err, want) {
			t.Errorf("HTTPClient(%q) = %v, want %v", raw, err, want)
		}
	}
}

// In app mode Home Assistant's HTTP API is reached in plaintext on the Supervisor's
// network, and nothing else is.
func TestHTTPClientInAppMode(t *testing.T) {
	for raw, want := range map[string]error{
		"http://homeassistant:8123": nil,
		"https://ha.example.org":    nil,
		"http://localhost:8123":     ErrInsecureURL,
		"http://172.30.32.1:8123":   ErrInsecureURL,
		"http://ha.example.org":     ErrInsecureURL,
	} {
		_, err := HTTPClient(raw, nil, appPlaintext)
		if want == nil && err != nil || want != nil && !errors.Is(err, want) {
			t.Errorf("HTTPClient(%q) = %v, want %v", raw, err, want)
		}
	}
}

func TestHTTPClientDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.org/", http.StatusFound)
	}))
	defer srv.Close()
	c, err := HTTPClient(srv.URL, nil, Plaintext{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL + "/auth/token")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status %d, want the redirect itself", resp.StatusCode)
	}
}

func TestHTTPClientRequiresTLS13(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MaxVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c, err := HTTPClient(srv.URL, roots, Plaintext{})
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := c.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Error("TLS 1.2 accepted")
	}
}
