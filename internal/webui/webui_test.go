// SPDX-License-Identifier: AGPL-3.0-or-later

package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var build = fstest.MapFS{
	"index.html":              {Data: []byte(`<!doctype html><script type="module" src="./assets/index-abc.js"></script>`)},
	"assets/index-abc.js":     {Data: []byte(`console.log(1)`)},
	"assets/index-abc.css":    {Data: []byte(`body{}`)},
	"assets/font.woff2":       {Data: []byte(`wOF2`)},
	"assets/odd.bin":          {Data: []byte(`?`)},
	"hm-logo-32.png":          {Data: []byte("\x89PNG")},
	"licenses.txt":            {Data: []byte("<script>alert(1)</script> MIT")},
	".keep":                   {Data: []byte{}},
	"assets/.hidden.js":       {Data: []byte(`x`)},
	"assets/sub/deep-xyz.svg": {Data: []byte(`<svg/>`)},
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://ingress.local"+target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesTheBuildWithHeaders(t *testing.T) {
	h := NewHandler(build, CSP)
	tests := []struct {
		target, contentType, cache, body string
	}{
		{"/", "text/html; charset=utf-8", "no-store", "<!doctype html>"},
		{"/index.html", "text/html; charset=utf-8", "no-store", "<!doctype html>"},
		{"/assets/index-abc.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", "console.log"},
		{"/assets/index-abc.css", "text/css; charset=utf-8", "public, max-age=31536000, immutable", "body"},
		{"/assets/font.woff2", "font/woff2", "public, max-age=31536000, immutable", "wOF2"},
		{"/assets/odd.bin", "application/octet-stream", "public, max-age=31536000, immutable", "?"},
		{"/assets/sub/deep-xyz.svg", "image/svg+xml", "public, max-age=31536000, immutable", "<svg"},
		{"/hm-logo-32.png", "image/png", "no-cache", "PNG"},
		// licenses.txt holds third-party text: plain text with nosniff, never HTML (5e).
		{"/licenses.txt", "text/plain; charset=utf-8", "no-cache", "<script>"},
	}
	for _, tc := range tests {
		rec := get(t, h, http.MethodGet, tc.target)
		hdr := rec.Header()
		if rec.Code != http.StatusOK || hdr.Get("Content-Type") != tc.contentType || hdr.Get("Cache-Control") != tc.cache ||
			!strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("GET %s: %d %v %q", tc.target, rec.Code, hdr, rec.Body.String())
		}
		checkSecurityHeaders(t, tc.target, hdr)
	}
}

func checkSecurityHeaders(t *testing.T, target string, hdr http.Header) {
	t.Helper()
	if hdr.Get("Content-Security-Policy") != CSP || hdr.Get("X-Content-Type-Options") != "nosniff" ||
		hdr.Get("Referrer-Policy") != "no-referrer" || hdr.Get("Last-Modified") != "" || hdr.Get("Set-Cookie") != "" {
		t.Errorf("%s: security headers %v", target, hdr)
	}
}

func TestRefusesEverythingElse(t *testing.T) {
	h := NewHandler(build, CSP)
	for _, target := range []string{
		"/missing.js", "/assets/", "/assets", "/assets//index-abc.js", "/assets/../index.html", "/./index.html",
		"/.keep", "/assets/.hidden.js", "/%2e%2e/etc/passwd", "/assets/%2e%2e/index.html", "//index.html",
		"/index.html/", "/agents", "/api/session",
	} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", target, rec.Code, rec.Body.String())
		}
		checkSecurityHeaders(t, target, rec.Header())
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions, "TRACE"} {
		rec := get(t, h, method, "/")
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s / = %d", method, rec.Code)
		}
		checkSecurityHeaders(t, method, rec.Header())
	}
}

func TestHead(t *testing.T) {
	rec := get(t, NewHandler(build, CSP), http.MethodHead, "/assets/index-abc.js")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "14" {
		t.Errorf("HEAD = %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestPlaceholderWithoutBuild(t *testing.T) {
	h := NewHandler(fstest.MapFS{".keep": {}}, CSP)
	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "make webui") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("placeholder = %d %q", rec.Code, rec.Body.String())
	}
	checkSecurityHeaders(t, "/", rec.Header())
	// The placeholder may not need anything the CSP forbids: no inline script or style.
	if regexp.MustCompile(`(?i)<script|<style|style=|on[a-z]+=`).Match(placeholder) {
		t.Error("placeholder uses inline script or style")
	}
}

func TestEmbeddedHandler(t *testing.T) {
	rec := get(t, Handler(), http.MethodGet, "/")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") != CSP {
		t.Errorf("embedded / = %d", rec.Code)
	}
	if rec := get(t, Handler(), http.MethodGet, "/.keep"); rec.Code != http.StatusNotFound {
		t.Errorf("embedded /.keep = %d", rec.Code)
	}
}

// The UI is developed and tested under the CSP of web/scripts/serve-ingress.ts; the
// gateway must send exactly that one.
func TestCSPMatchesTheUIDevServer(t *testing.T) {
	src, err := os.ReadFile("../../web/scripts/serve-ingress.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)export const CSP =\s*((?:'[^']*'|"[^"]*"|\s|\+)+);`).FindSubmatch(src)
	if m == nil {
		t.Fatal("CSP not found in serve-ingress.ts")
	}
	var b strings.Builder
	for _, part := range regexp.MustCompile(`"([^"]*)"`).FindAllSubmatch(m[1], -1) {
		b.Write(part[1])
	}
	if b.String() != CSP {
		t.Errorf("serve-ingress.ts CSP = %q\nwebui CSP           = %q", b.String(), CSP)
	}
	for _, forbidden := range []string{"unsafe-inline", "unsafe-eval", "http:", "https:", "*", "data:"} {
		if strings.Contains(CSP, forbidden) {
			t.Errorf("CSP contains %q", forbidden)
		}
	}
}

func TestDirectModeIsNeverFramed(t *testing.T) {
	rec := get(t, DirectHandler(), http.MethodGet, "/")
	if got := rec.Header().Get("Content-Security-Policy"); got != CSPDirect || !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", got)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q", rec.Header().Get("X-Frame-Options"))
	}
	if strings.Replace(CSPDirect, "frame-ancestors 'none'", "frame-ancestors 'self'", 1) != CSP {
		t.Error("the policies differ in more than framing")
	}
	ingress := get(t, Handler(), http.MethodGet, "/")
	if ingress.Header().Get("Content-Security-Policy") != CSP || ingress.Header().Get("X-Frame-Options") != "" {
		t.Error("the Ingress policy changed")
	}
}
