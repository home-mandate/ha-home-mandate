// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// cimdServer serves client metadata documents; the resolver may reach it on loopback
// and any port, unlike in production.
type cimdServer struct {
	srv     *httptest.Server
	fetches atomic.Int64
	docs    map[string]func(w http.ResponseWriter, id string)
}

func newCIMDServer(t *testing.T) (*cimdServer, *CIMDResolver) {
	t.Helper()
	c := &cimdServer{docs: map[string]func(http.ResponseWriter, string){}}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.fetches.Add(1)
		h, ok := c.docs[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, c.srv.URL+r.URL.Path)
	}))
	t.Cleanup(c.srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(c.srv.Certificate())
	r := NewCIMDResolver(roots)
	r.allowAddr = func(netip.Addr) bool { return true }
	r.anyPort = true
	return c, r
}

func (c *cimdServer) json(path, body string) string {
	c.docs[path] = func(w http.ResponseWriter, id string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "$ID", id)))
	}
	return c.srv.URL + path
}

const goodDoc = `{"client_id":"$ID","client_name":"Claude Code","redirect_uris":["http://127.0.0.1:33418/callback","https://app.example.org/cb"],
"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`

func TestResolveFetchesTheMetadataDocument(t *testing.T) {
	c, r := newCIMDServer(t)
	id := c.json("/client.json", goodDoc)
	got, err := r.Resolve(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Name != "Claude Code" || !got.Verified || len(got.RedirectURIs) != 2 {
		t.Errorf("client = %+v", got)
	}
	// Cached: a second resolve does not fetch again.
	if _, err := r.Resolve(context.Background(), id); err != nil || c.fetches.Load() != 1 {
		t.Errorf("fetches = %d, %v", c.fetches.Load(), err)
	}
	r.now = func() time.Time { return time.Now().Add(cimdCacheTTL + time.Second) }
	if _, err := r.Resolve(context.Background(), id); err != nil || c.fetches.Load() != 2 {
		t.Errorf("after expiry: fetches = %d, %v", c.fetches.Load(), err)
	}
}

// Negative catalog: client metadata unreachable, wrong format, client ID ≠ URL → rejected.
func TestResolveRejectsBadDocuments(t *testing.T) {
	c, r := newCIMDServer(t)
	cases := map[string]string{
		"client id differs":     c.json("/a", strings.Replace(goodDoc, `"client_id":"$ID"`, `"client_id":"https://evil.example.org/c"`, 1)),
		"client id with suffix": c.json("/b", strings.Replace(goodDoc, `"client_id":"$ID"`, `"client_id":"$ID/"`, 1)),
		"not json":              c.json("/c", `<html>`),
		"array":                 c.json("/d", `[]`),
		"secret auth":           c.json("/e", strings.Replace(goodDoc, `"none"`, `"client_secret_basic"`, 1)),
		"redirect not a string": c.json("/f", strings.Replace(goodDoc, `"https://app.example.org/cb"`, `42`, 1)),
		"plaintext redirect":    c.json("/g", strings.Replace(goodDoc, `"https://app.example.org/cb"`, `"http://app.example.org/cb"`, 1)),
		"redirect fragment":     c.json("/h", strings.Replace(goodDoc, `"https://app.example.org/cb"`, `"https://app.example.org/cb#x"`, 1)),
		"relative redirect":     c.json("/i", strings.Replace(goodDoc, `"https://app.example.org/cb"`, `"/cb"`, 1)),
		"custom scheme":         c.json("/j", strings.Replace(goodDoc, `"https://app.example.org/cb"`, `"com.example:/cb"`, 1)),
		"directive in host":     c.json("/m", strings.Replace(goodDoc, `"https://app.example.org/cb"`, `"https://a.example;sandbox/cb"`, 1)),
		"too large":             c.json("/k", `{"client_id":"$ID","pad":"`+strings.Repeat("x", 6<<10)+`"}`),
		"unknown":               c.srv.URL + "/missing",
		"duplicate keys":        c.json("/l", `{"client_id":"https://evil.example.org/c","client_id":"$ID","redirect_uris":[]}`),
	}
	c.docs["/redirect"] = func(w http.ResponseWriter, id string) {
		w.Header().Set("Location", "/a")
		w.WriteHeader(http.StatusFound)
	}
	cases["redirect"] = c.srv.URL + "/redirect"
	c.docs["/html"] = func(w http.ResponseWriter, id string) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.ReplaceAll(goodDoc, "$ID", id)))
	}
	cases["wrong content type"] = c.srv.URL + "/html"
	for name, id := range cases {
		if _, err := r.Resolve(context.Background(), id); !errors.Is(err, ErrInvalidClient) {
			t.Errorf("%s: %v, want ErrInvalidClient", name, err)
		}
	}
}

// A failed fetch is remembered for a short while, so that repeated requests for a slow
// or broken document do not tie up the fetch slots; failures that are not the
// document's are never remembered.
func TestResolveRemembersFailuresBriefly(t *testing.T) {
	c, r := newCIMDServer(t)
	now := time.Now()
	r.now = func() time.Time { return now }
	var broken atomic.Bool
	broken.Store(true)
	c.docs["/flaky.json"] = func(w http.ResponseWriter, id string) {
		if broken.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(goodDoc, "$ID", id)))
	}
	id := c.srv.URL + "/flaky.json"
	for range 3 {
		if _, err := r.Resolve(context.Background(), id); !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("broken document: %v", err)
		}
	}
	broken.Store(false)
	if _, err := r.Resolve(context.Background(), id); !errors.Is(err, ErrInvalidClient) || c.fetches.Load() != 1 {
		t.Errorf("within the failure time: fetches = %d, %v", c.fetches.Load(), err)
	}
	now = now.Add(cimdFailTTL)
	if _, err := r.Resolve(context.Background(), id); err != nil || c.fetches.Load() != 2 {
		t.Errorf("after the failure time: fetches = %d, %v", c.fetches.Load(), err)
	}

	// The caller gave up, or all fetch slots were busy: not the document's failure.
	good := c.json("/good.json", goodDoc)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(cancelled, good); !errors.Is(err, ErrInvalidClient) {
		t.Errorf("cancelled: %v", err)
	}
	for range cimdParallel {
		r.fetching <- struct{}{}
	}
	other := c.json("/other.json", goodDoc)
	if _, err := r.Resolve(context.Background(), other); !errors.Is(err, ErrInvalidClient) {
		t.Errorf("all slots busy: %v", err)
	}
	for range cimdParallel {
		<-r.fetching
	}
	for _, id := range []string{good, other} {
		if _, err := r.Resolve(context.Background(), id); err != nil {
			t.Errorf("%s afterwards: %v", id, err)
		}
	}

	// The memory of failures is bounded.
	for i := range cimdFailSize + 10 {
		_, _ = r.Resolve(context.Background(), fmt.Sprintf("%s/missing-%d", c.srv.URL, i))
	}
	if n := len(r.failed); n > cimdFailSize {
		t.Errorf("%d failures remembered", n)
	}
}

func TestResolveSanitizesTheName(t *testing.T) {
	c, r := newCIMDServer(t)
	for name, doc := range map[string]string{
		"bidi override": strings.Replace(goodDoc, `"Claude Code"`, `"Claude\u202eedoC"`, 1),
		"too long":      strings.Replace(goodDoc, `"Claude Code"`, `"`+strings.Repeat("n", 81)+`"`, 1),
		"missing":       strings.Replace(goodDoc, `"client_name":"Claude Code",`, ``, 1),
	} {
		id := c.json("/"+strings.ReplaceAll(name, " ", "-"), doc)
		got, err := r.Resolve(context.Background(), id)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Name != "127.0.0.1" {
			t.Errorf("%s: name %q, want the host", name, got.Name)
		}
	}
}

func TestResolveFreeIdentifiers(t *testing.T) {
	r := NewCIMDResolver(nil)
	got, err := r.Resolve(context.Background(), "n8n-kitchen")
	if err != nil || got.Verified || got.Name != "n8n-kitchen" || len(got.RedirectURIs) != 0 {
		t.Errorf("free identifier = %+v, %v", got, err)
	}
	for _, id := range []string{"", "N8N", "-x", "a b", strings.Repeat("a", 65), "http://plain.example.org/c",
		"https://example.org", "https://example.org/", "https://u:p@example.org/c", "https://example.org/c#f",
		"https://example.org:8443/c", "https://example.org/a/../c", "https://example.org/./c", "hm-client:x"} {
		if _, err := r.Resolve(context.Background(), id); !errors.Is(err, ErrInvalidClient) {
			t.Errorf("Resolve(%q) = %v, want ErrInvalidClient", id, err)
		}
	}
}

// The production resolver connects only to public addresses: the check runs on the
// address actually dialled, after DNS resolution.
func TestResolverRefusesNonPublicAddresses(t *testing.T) {
	r := NewCIMDResolver(nil)
	for addr, ok := range map[string]bool{
		"93.184.215.14:443": true, "[2606:2800:21f:cb07:6820:80da:af6b:8b2c]:443": true,
		"127.0.0.1:443": false, "[::1]:443": false, "10.0.0.5:443": false, "192.168.1.2:443": false,
		"172.16.0.1:443": false, "169.254.169.254:443": false, "100.64.0.1:443": false, "0.0.0.0:443": false,
		"[fd00::1]:443": false, "[fe80::1]:443": false, "[::ffff:127.0.0.1]:443": false, "[64:ff9b::a00:1]:443": false,
		"224.0.0.1:443": false, "198.18.0.1:443": false, "not-an-ip:443": false,
		"[::a00:1]:443": false, "[fec0::1]:443": false, "[2001:0:4136:e378:8000:63bf:3fff:fdd2]:443": false,
		"192.88.99.10:443": true, // deprecated 6to4 relay anycast, the E2E suite's public stand-in
	} {
		if err := r.control("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("%s: %v", addr, err)
		}
	}
	// End to end: a metadata server on loopback is not reached.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("loopback server was contacted")
	}))
	defer srv.Close()
	r.anyPort = true
	if _, err := r.Resolve(context.Background(), srv.URL+"/client.json"); !errors.Is(err, ErrInvalidClient) {
		t.Errorf("loopback: %v", err)
	}
}

func TestRedirectAllowed(t *testing.T) {
	registered := []string{"https://app.example.org/cb", "http://127.0.0.1:33418/callback", "http://localhost/cb", "http://[::1]:1/cb"}
	for uri, ok := range map[string]bool{
		"https://app.example.org/cb":        true,
		"https://APP.example.org/cb":        false,
		"https://app.example.org/CB":        false,
		"https://app.example.org/cb/x":      false,
		"https://app.example.org/cb?x=1":    false,
		"https://app.example.org/cb#x":      false,
		"https://app.example.org:8443/cb":   false,
		"http://127.0.0.1:50123/callback":   true, // RFC 8252 section 7.3: any port on loopback
		"http://127.0.0.1/callback":         true,
		"http://127.0.0.1:50123/callback/x": false,
		"http://127.0.0.1:50123/callback?a": false,
		"https://127.0.0.1:50123/callback":  false,
		"http://localhost:9000/cb":          true,
		"http://[::1]:9/cb":                 true,
		"http://127.0.0.2:1/callback":       false,
		"http://u@127.0.0.1:1/callback":     false,
		"":                                  false,
		"::":                                false,
	} {
		if got := redirectAllowed(registered, uri); got != ok {
			t.Errorf("redirectAllowed(%q) = %v, want %v", uri, got, ok)
		}
	}
}
