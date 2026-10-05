// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webui serves the local UI (web/, built by Vite) from the binary with a strict
// Content Security Policy (docs/ARCHITECTURE.md section 12). The build is copied into
// dist/ before the Go build (make webui, Dockerfile); without it, a placeholder page says
// that the UI was not built. Only GET and HEAD of files in the build are answered: no
// directory listings, no fallback to index.html (the UI routes by hash).
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// CSP is the Content Security Policy of every answer. It must equal the one in
// web/scripts/serve-ingress.ts, which Playwright runs the UI under (a test compares
// them). default-src 'none' also covers object-src, frame-src and worker-src.
// frame-ancestors 'self': Home Assistant shows the UI in an iframe of its own origin.
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// CSPDirect is the policy in direct mode (container mode without Ingress, ARCHITECTURE
// section 12): nobody frames the UI there.
const CSPDirect = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

//go:embed all:dist
var dist embed.FS

//go:embed placeholder.html
var placeholder []byte

// contentTypes are the only file types of the build; anything else is served as
// application/octet-stream, which a browser never runs.
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
	".json":  "application/json",
}

// Hashed assets never change under their name; everything else is checked on every load.
const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
	cacheNever      = "no-store"
)

// Handler serves the UI from the embedded build, for Ingress.
func Handler() http.Handler {
	return NewHandler(embedded(), CSP)
}

// DirectHandler serves the UI from the embedded build in direct mode, never framed.
func DirectHandler() http.Handler {
	return NewHandler(embedded(), CSPDirect)
}

func embedded() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embedded directory always exists
	}
	return sub
}

// NewHandler serves the UI from files under the policy csp.
func NewHandler(files fs.FS, csp string) http.Handler {
	return &handler{files: files, csp: csp}
}

type handler struct {
	files fs.FS
	csp   string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Security-Policy", h.csp)
	if h.csp == CSPDirect {
		header.Set("X-Frame-Options", "DENY")
	}
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		header.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, ok := fileName(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(h.files, name)
	if name == "index.html" && err != nil {
		data, err = placeholder, nil
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := path.Ext(name)
	contentType, known := contentTypes[ext]
	if !known {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	switch {
	case name == "index.html":
		header.Set("Cache-Control", cacheNever)
	case strings.HasPrefix(name, "assets/"):
		header.Set("Cache-Control", cacheImmutable)
	default:
		header.Set("Cache-Control", cacheRevalidate)
	}
	// ServeContent answers HEAD and Range; a zero time sends no Last-Modified.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

// fileName maps a URL path to a file in the build: "/" is index.html; a path that is not
// clean, names a directory, a hidden file or leaves the build is refused.
func fileName(urlPath string) (string, bool) {
	if urlPath == "/" {
		return "index.html", true
	}
	name, ok := strings.CutPrefix(urlPath, "/")
	if !ok || name == "" || strings.HasSuffix(name, "/") || path.Clean(name) != name || !fs.ValidPath(name) {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return "", false
		}
	}
	return name, true
}
