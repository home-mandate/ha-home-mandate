// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Direct mode (ARCHITECTURE section 12): the gateway runs in container mode with a
// certificate and an https public URL, so the UI is also served under /ui/ of the MCP
// listener, with the sign-in through the real Home Assistant.

// directSignIn signs user in to the UI through Home Assistant's login flow and returns
// where the callback sent the browser.
func directSignIn(t *testing.T, b *hmBrowser, user string) string {
	t.Helper()
	start := b.get("/ui/signin")
	u, err := url.Parse(start.location)
	if start.status != http.StatusSeeOther || err != nil || u.Path != "/auth/authorize" {
		t.Fatalf("expected the redirect to Home Assistant, got %d %q", start.status, start.location)
	}
	q := u.Query()
	if q.Get("redirect_uri") != env.public+"/ui/signin/callback" || q.Get("client_id") != env.public+"/" {
		t.Fatalf("authorize request %v", q)
	}
	code, err := haLogin(q.Get("client_id"), q.Get("redirect_uri"), env.users[user])
	if err != nil {
		t.Fatal(err)
	}
	end := b.get("/ui/signin/callback?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode())
	if end.status != http.StatusSeeOther {
		t.Fatalf("callback = %d", end.status)
	}
	return end.location
}

// directAPI sends a request of the UI's own page to the API of direct mode.
func directAPI(b *hmBrowser, method, path, csrf string, edit func(*http.Request)) page {
	b.t.Helper()
	req, err := http.NewRequest(method, env.public+path, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("X-HM-CSRF", csrf)
	}
	if edit != nil {
		edit(req)
	}
	return b.do(req)
}

func TestDirectModeSignInThroughHomeAssistant(t *testing.T) {
	b := newBrowser(t)
	if got := directSignIn(t, b, adminApprover); got != "/ui/" {
		t.Fatalf("signed-in administrator sent to %q", got)
	}
	ui := b.get("/ui/")
	if ui.status != http.StatusOK || !strings.Contains(strings.ToLower(ui.body), "<!doctype html") {
		t.Fatalf("UI = %d", ui.status)
	}
	resp, err := b.c.Get(env.public + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("framing allowed: CSP %q", csp)
	}

	sess := directAPI(b, http.MethodGet, "/ui/api/session", "", nil)
	if sess.status != http.StatusOK {
		t.Fatalf("session = %d %s", sess.status, sess.body)
	}
	var s struct {
		User      struct{ ID string } `json:"user"`
		CSRFToken string              `json:"csrf_token"`
		SignOut   bool                `json:"sign_out"`
	}
	if err := json.Unmarshal([]byte(sess.body), &s); err != nil {
		t.Fatalf("session is no JSON: %v", err)
	}
	if s.User.ID != env.users[adminApprover].id || !s.SignOut || s.CSRFToken == "" {
		t.Fatalf("session %+v", s)
	}

	// The cookie the gateway set is a __Host- cookie for this origin only.
	u, _ := url.Parse(env.public)
	var found *http.Cookie
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == "__Host-hm_ui" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("no session cookie")
	}

	if r := directAPI(b, http.MethodPost, "/ui/signout", s.CSRFToken, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); r.status != http.StatusForbidden {
		t.Errorf("sign-out from another site = %d", r.status)
	}
	if r := directAPI(b, http.MethodPost, "/ui/signout", s.CSRFToken, nil); r.status != http.StatusNoContent {
		t.Fatalf("sign-out = %d %s", r.status, r.body)
	}
	replay := newBrowser(t)
	replay.c.Jar.SetCookies(u, []*http.Cookie{{Name: "__Host-hm_ui", Value: found.Value, Path: "/"}})
	if r := directAPI(replay, http.MethodGet, "/ui/api/session", "", nil); r.status != http.StatusUnauthorized {
		t.Errorf("the old cookie after the sign-out = %d", r.status)
	}
}

func TestDirectModeRefusesNonAdministratorsAndProxyHeaders(t *testing.T) {
	b := newBrowser(t)
	if got := directSignIn(t, b, userPlain); got != "/ui/#/signin?error=denied" {
		t.Errorf("non-administrator sent to %q", got)
	}
	if r := directAPI(b, http.MethodGet, "/ui/api/session", "", nil); r.status != http.StatusUnauthorized {
		t.Errorf("session of a non-administrator = %d", r.status)
	}
	spoof := newBrowser(t)
	r := directAPI(spoof, http.MethodGet, "/ui/api/session", "", func(r *http.Request) {
		r.Header.Set("X-Remote-User-Id", env.users[adminApprover].id)
		r.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")
	})
	if r.status != http.StatusUnauthorized {
		t.Errorf("Supervisor headers without a session = %d", r.status)
	}
	// A callback with a state the browser never started.
	if r := spoof.get("/ui/signin/callback?code=x&state=y"); r.status != http.StatusSeeOther || r.location != "/ui/#/signin?error=failed" {
		t.Errorf("forged callback = %d %q", r.status, r.location)
	}
}

// The certificate is renewed while the gateway runs: the next handshake after the
// gateway's look at the files (at most once a minute) uses it, without a restart. The test
// waits up to that minute and more; the renewal stays in place for later tests (same CA
// and names).
func TestDirectModeTakesARenewedCertificateOver(t *testing.T) {
	serial := func() *big.Int {
		conn, err := tls.Dial("tcp", strings.TrimPrefix(env.public, "https://"), &tls.Config{RootCAs: env.roots, ServerName: "localhost", MinVersion: tls.VersionTLS13})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		return conn.ConnectionState().PeerCertificates[0].SerialNumber
	}
	before := serial()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	next := new(big.Int).Add(before, big.NewInt(1000))
	tmpl := &x509.Certificate{SerialNumber: next, Subject: pkix.Name{CommonName: "homeassistant"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{"homeassistant", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, env.ca, &key.PublicKey, env.caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	// As a renewal does: write beside, then rename into place.
	for name, data := range map[string][]byte{
		"cert.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"key.pem":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	} {
		tmp := filepath.Join(env.certs, "."+name+".new")
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(env.certs, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Between the two renames the files do not belong together: the gateway may log that
	// once and keeps the previous pair until its next look.
	eventually(t, "the renewed certificate is served", 150*time.Second, func() bool { return serial().Cmp(next) == 0 })
}
