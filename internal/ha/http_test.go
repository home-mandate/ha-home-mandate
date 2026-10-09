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
		_, err := HTTPClient(raw, nil, Plaintext{}, "")
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
		_, err := HTTPClient(raw, nil, appPlaintext, "")
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
	c, err := HTTPClient(srv.URL, nil, Plaintext{}, "")
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
	c, err := HTTPClient(srv.URL, roots, Plaintext{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := c.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Error("TLS 1.2 accepted")
	}
}

// In app mode with TLS in Home Assistant, Home-Mandate connects to homeassistant on the
// Supervisor's network, while the certificate names the address browsers use: it is
// checked against that name.
func TestHTTPClientChecksTheCertificateAgainstServerName(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	for name, ok := range map[string]bool{"example.com": true, "ha.example.org": false} {
		c, err := HTTPClient(srv.URL, roots, Plaintext{}, name) // httptest's certificate is for example.com
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		if (err == nil) != ok {
			t.Errorf("server name %q: %v", name, err)
		}
	}
}

// CoreInfo asks the Supervisor how Home Assistant serves its HTTP API.
func TestCoreInfo(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		port       int
		tls, ok    bool
	}{
		{"plaintext", `{"result":"ok","data":{"port":8123,"ssl":false,"version":"2026.9.4"}}`, 200, 8123, false, true},
		{"own certificate", `{"result":"ok","data":{"port":443,"ssl":true}}`, 200, 443, true, true},
		{"error result", `{"result":"error","message":"no"}`, 200, 0, false, false},
		{"no port", `{"result":"ok","data":{"ssl":false}}`, 200, 0, false, false},
		{"port out of range", `{"result":"ok","data":{"port":70000,"ssl":false}}`, 200, 0, false, false},
		{"refused", `{"result":"error"}`, 403, 0, false, false},
		{"not json", `<html>`, 200, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/core/info" || r.Header.Get("Authorization") != "Bearer sup-token" {
					t.Errorf("request %s %v", r.URL.Path, r.Header)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			port, tls, err := CoreInfo(t.Context(), srv.URL, "sup-token", Plaintext{})
			if (err == nil) != tc.ok || port != tc.port || tls != tc.tls {
				t.Errorf("CoreInfo = %d, %v, %v", port, tls, err)
			}
		})
	}
}
