// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// spotterIssuances reads the recorded Cert Spotter answer for example.com.
func spotterIssuances(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(certSpotterFile)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// fakeSpotter serves issuances in pages of pageSize after the cursor.
func fakeSpotter(t *testing.T, issuances []map[string]any, pageSize int, check func(*http.Request)) (*CertSpotter, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if check != nil {
			check(r)
		}
		start := 0
		if after := r.URL.Query().Get("after"); after != "" {
			for i, is := range issuances {
				if is["id"] == after {
					start = i + 1
				}
			}
		}
		end := min(start+pageSize, len(issuances))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(issuances[start:end])
	}))
	t.Cleanup(srv.Close)
	c := NewCertSpotter(srv.Client(), "")
	c.baseURL = srv.URL
	return c, &calls
}

func TestCertSpotterReadsAllPagesAndKeepsTheCursor(t *testing.T) {
	all := spotterIssuances(t)
	c, calls := fakeSpotter(t, all, 3, func(r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/v1/issuances" || q.Get("domain") != "example.com" || q.Get("match_wildcards") != "true" ||
			strings.Join(q["expand"], ",") != "dns_names,cert_der,issuer" || r.Header.Get("Authorization") != "" ||
			r.Header.Get("User-Agent") != userAgent {
			t.Errorf("request %s %v", r.URL, r.Header)
		}
	})
	b, err := c.Fetch(context.Background(), "example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	// 9 issuances in pages of 3: three full pages and the empty one that ends the list.
	if len(b.Certs) != 9 || b.Skipped != 0 || b.Cursor != all[8]["id"] || calls.Load() != 4 {
		t.Fatalf("%d certificates, %d skipped, cursor %s, %d calls", len(b.Certs), b.Skipped, b.Cursor, calls.Load())
	}
	b, err = c.Fetch(context.Background(), "example.com", b.Cursor)
	if err != nil || len(b.Certs) != 0 || b.Cursor != all[8]["id"] {
		t.Fatalf("after the end: %+v %v", b, err)
	}
}

func TestCertSpotterReadsAtMostMaxPagesARound(t *testing.T) {
	all := spotterIssuances(t)
	c, calls := fakeSpotter(t, all, 1, nil)
	b, err := c.Fetch(context.Background(), "example.com", "")
	if err != nil || len(b.Certs) != maxPages || calls.Load() != maxPages || b.Cursor != all[maxPages-1]["id"] {
		t.Fatalf("%d certificates, %d calls, cursor %s, %v", len(b.Certs), calls.Load(), b.Cursor, err)
	}
}

func TestCertSpotterSendsTheKey(t *testing.T) {
	c, _ := fakeSpotter(t, nil, 1, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k3y" {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
		}
	})
	c.key = "k3y"
	if _, err := c.Fetch(context.Background(), "example.com", ""); err != nil {
		t.Fatal(err)
	}
}

// TestCertSpotterIssuancesThatCannotBeRead: an issuance whose certificate does not match
// Cert Spotter's own digests is skipped; one without a readable certificate is taken
// from Cert Spotter's fields, without a time of logging.
func TestCertSpotterIssuancesThatCannotBeRead(t *testing.T) {
	all := spotterIssuances(t)
	wrongDigest := maps.Clone(all[0])
	wrongDigest["tbs_sha256"] = strings.Repeat("0", 64)
	noDER := maps.Clone(all[1])
	noDER["cert_der"] = "!!"
	badDigest := maps.Clone(all[2])
	badDigest["pubkey_sha256"] = "XYZ"
	badDates := maps.Clone(all[3])
	badDates["cert_der"], badDates["not_after"] = "", "soon"
	c, _ := fakeSpotter(t, []map[string]any{wrongDigest, noDER, badDigest, badDates}, 10, nil)
	b, err := c.Fetch(context.Background(), "example.com", "")
	if err != nil || len(b.Certs) != 1 || b.Skipped != 3 || b.Cursor != badDates["id"] {
		t.Fatalf("%+v %v", b, err)
	}
	got := b.Certs[0]
	if !got.Logged.IsZero() || got.Names[0] != "example.com" || got.NotAfter.IsZero() || got.Digest == (Digest{}) {
		t.Fatalf("from the fields: %+v", got)
	}
	badID := maps.Clone(all[0])
	badID["id"] = "../x"
	c, _ = fakeSpotter(t, []map[string]any{badID}, 10, nil)
	if _, err := c.Fetch(context.Background(), "example.com", ""); !errors.Is(err, ErrSource) {
		t.Fatalf("bad ID: %v", err)
	}
}

func TestSourceFailures(t *testing.T) {
	rateLimited := func(want time.Duration) func(error) bool {
		return func(err error) bool {
			var rl *RateLimitedError
			return errors.As(err, &rl) && rl.RetryAfter == want && rl.Source == SourceCertSpotter && strings.Contains(rl.Error(), "rate limit")
		}
	}
	status := func(code int, retry string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			w.WriteHeader(code)
		}
	}
	isSource := func(err error) bool { return errors.Is(err, ErrSource) }
	cases := map[string]struct {
		handler http.HandlerFunc
		check   func(error) bool
	}{
		"429 with Retry-After":           {status(http.StatusTooManyRequests, "120"), rateLimited(2 * time.Minute)},
		"503 with an absurd Retry-After": {status(http.StatusServiceUnavailable, "99999999"), rateLimited(maxRetryAfter)},
		"403 with Retry-After":           {status(http.StatusForbidden, "60"), rateLimited(time.Minute)},
		"429 with a date":                {status(http.StatusTooManyRequests, "Mon, 01 Jan 2030 00:00:00 GMT"), rateLimited(0)},
		"503 without Retry-After":        {status(http.StatusServiceUnavailable, ""), isSource},
		"not JSON": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>"))
		}, isSource},
		"too large": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("[" + strings.Repeat(" ", maxListBytes) + "]"))
		}, isSource},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			s := NewCertSpotter(srv.Client(), "")
			s.baseURL = srv.URL
			b, err := s.Fetch(context.Background(), "example.com", "7")
			if !c.check(err) || len(b.Certs) != 0 || b.Cursor != "" {
				t.Fatalf("%+v %v", b, err)
			}
		})
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	s := NewCertSpotter(closed.Client(), "")
	s.baseURL = closed.URL
	if _, err := s.Fetch(context.Background(), "example.com", ""); err == nil {
		t.Fatal("no connection, no error")
	}
	s.baseURL = "http://bad host"
	if _, err := s.Fetch(context.Background(), "example.com", ""); err == nil {
		t.Fatal("bad URL, no error")
	}
}

// fakeCrtSh serves a list per query and certificates by ID.
type fakeCrtSh struct {
	lists     map[string][]crtshEntry
	pems      map[int64]string
	failFrom  int64 // downloads from this ID on fail with 502
	downloads atomic.Int32
}

func (f *fakeCrtSh) source(t *testing.T) *CrtSh {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if d := q.Get("d"); d != "" {
			f.downloads.Add(1)
			id, _ := strconv.ParseInt(d, 10, 64)
			if f.failFrom != 0 && id >= f.failFrom {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(f.pems[id]))
			return
		}
		if q.Get("output") != "json" || q.Get("exclude") != "expired" || r.Header.Get("User-Agent") != userAgent {
			t.Errorf("list %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(f.lists[q.Get("q")])
	}))
	t.Cleanup(srv.Close)
	c := NewCrtSh(srv.Client())
	c.baseURL = srv.URL
	return c
}

func pemOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCrtShQueriesNameAndWildcardAndDownloadsAfterTheCursor(t *testing.T) {
	f := &fakeCrtSh{
		lists: map[string][]crtshEntry{
			"www.example.com": {{ID: 5}, {ID: 7}},
			"*.example.com":   {{ID: 7}, {ID: 9}, {ID: 3}},
		},
		pems: map[int64]string{5: pemOf(t, crtshCert), 7: pemOf(t, crtshPrecert), 9: "not PEM", 3: pemOf(t, crtshCert)},
	}
	b, err := f.source(t).Fetch(context.Background(), "www.example.com", "4")
	if err != nil {
		t.Fatal(err)
	}
	// 3 is before the cursor; 7 is downloaded once; 9 cannot be read and is skipped.
	if len(b.Certs) != 2 || b.Skipped != 1 || b.Cursor != "9" || f.downloads.Load() != 3 {
		t.Fatalf("%d certificates, %d skipped, cursor %s, %d downloads", len(b.Certs), b.Skipped, b.Cursor, f.downloads.Load())
	}
	if b.Certs[0].Digest != b.Certs[1].Digest {
		t.Fatal("precertificate and certificate are one")
	}
}

func TestCrtShQueriesNoWildcardOfATopLevelDomain(t *testing.T) {
	f := &fakeCrtSh{lists: map[string][]crtshEntry{"*.com": {{ID: 1}}}}
	b, err := f.source(t).Fetch(context.Background(), "example.com", "")
	if err != nil || len(b.Certs) != 0 || f.downloads.Load() != 0 {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestCrtShDownloadsAtMostMaxDownloadsARound(t *testing.T) {
	f := &fakeCrtSh{lists: map[string][]crtshEntry{}, pems: map[int64]string{}}
	for i := int64(1); i <= maxDownloads+5; i++ {
		f.lists["a.example.org"] = append(f.lists["a.example.org"], crtshEntry{ID: i})
		f.pems[i] = pemOf(t, crtshCert)
	}
	b, err := f.source(t).Fetch(context.Background(), "a.example.org", "")
	if err != nil || len(b.Certs) != maxDownloads || b.Cursor != strconv.Itoa(maxDownloads) {
		t.Fatalf("%d certificates, cursor %s, %v", len(b.Certs), b.Cursor, err)
	}
}

func TestCrtShFailures(t *testing.T) {
	// A failed download after a good one: what was read counts, the cursor stops before.
	f := &fakeCrtSh{lists: map[string][]crtshEntry{"a.example.org": {{ID: 1}, {ID: 2}, {ID: 3}}},
		pems: map[int64]string{1: pemOf(t, crtshCert)}, failFrom: 2}
	b, err := f.source(t).Fetch(context.Background(), "a.example.org", "")
	if err != nil || len(b.Certs) != 1 || b.Cursor != "1" {
		t.Fatalf("partial: %+v %v", b, err)
	}
	// A failed first download is a failed query.
	f = &fakeCrtSh{lists: map[string][]crtshEntry{"a.example.org": {{ID: 2}}}, failFrom: 2}
	if b, err := f.source(t).Fetch(context.Background(), "a.example.org", "1"); !errors.Is(err, ErrSource) || b.Cursor != "" {
		t.Fatalf("failed download: %+v %v", b, err)
	}
	f = &fakeCrtSh{lists: map[string][]crtshEntry{"a.example.org": {{ID: -1}}}}
	if _, err := f.source(t).Fetch(context.Background(), "a.example.org", ""); !errors.Is(err, ErrSource) {
		t.Fatalf("bad ID: %v", err)
	}
	for _, cursor := range []string{"x", "-5"} {
		if _, err := f.source(t).Fetch(context.Background(), "a.example.org", cursor); !errors.Is(err, ErrSource) {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv.Close()
	c := NewCrtSh(srv.Client())
	c.baseURL = srv.URL
	if _, err := c.Fetch(context.Background(), "a.example.org", ""); !errors.Is(err, ErrSource) {
		t.Fatalf("502: %v", err)
	}
	c.baseURL = "http://bad host"
	if _, err := c.Fetch(context.Background(), "a.example.org", ""); err == nil {
		t.Fatal("bad URL, no error")
	}
	if _, err := c.download(context.Background(), 1); err == nil {
		t.Fatal("bad URL, no error")
	}
	if c.Name() != SourceCrtSh || NewCertSpotter(nil, "").Name() != SourceCertSpotter {
		t.Fatal("names")
	}
}
