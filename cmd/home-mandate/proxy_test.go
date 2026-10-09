// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Behind a reverse proxy (HM_PROXY) only the proxy is served, and the sender is the last
// entry of X-Forwarded-For: the one the proxy itself appended. Earlier entries come from
// the client and are never trusted.
func TestOnlyProxy(t *testing.T) {
	for _, tc := range []struct {
		name, proxy, peer string
		forwarded         []string
		wantStatus        int
		wantSender        string
	}{
		{"proxy, one entry", "192.0.2.10", "192.0.2.10:41000", []string{"198.51.100.7"}, http.StatusOK, "198.51.100.7"},
		{"proxy, spoofed first entries", "192.0.2.10", "192.0.2.10:41000", []string{"203.0.113.9, 198.51.100.7"}, http.StatusOK, "198.51.100.7"},
		{"proxy, two header lines", "192.0.2.10", "192.0.2.10:41000", []string{"203.0.113.9", "198.51.100.7"}, http.StatusOK, "198.51.100.7"},
		{"proxy, ipv6 sender", "192.0.2.10", "192.0.2.10:41000", []string{"2001:db8::7"}, http.StatusOK, "2001:db8::7"},
		{"proxy, ipv4-mapped sender", "192.0.2.10", "192.0.2.10:41000", []string{"::ffff:198.51.100.7"}, http.StatusOK, "198.51.100.7"},
		{"proxy, ipv4-mapped peer", "192.0.2.10", "[::ffff:192.0.2.10]:41000", []string{"198.51.100.7"}, http.StatusOK, "198.51.100.7"},
		{"ipv6 proxy", "2001:db8::10", "[2001:db8::10]:41000", []string{"198.51.100.7"}, http.StatusOK, "198.51.100.7"},
		{"proxy, no header", "192.0.2.10", "192.0.2.10:41000", nil, http.StatusOK, "192.0.2.10"},
		{"proxy, not an address", "192.0.2.10", "192.0.2.10:41000", []string{"unknown"}, http.StatusOK, "192.0.2.10"},
		{"proxy, empty last entry", "192.0.2.10", "192.0.2.10:41000", []string{"198.51.100.7, "}, http.StatusOK, "192.0.2.10"},
		{"proxy, only a comma", "192.0.2.10", "192.0.2.10:41000", []string{","}, http.StatusOK, "192.0.2.10"},
		{"proxy, only spaces", "192.0.2.10", "192.0.2.10:41000", []string{"   "}, http.StatusOK, "192.0.2.10"},
		{"proxy, address with port", "192.0.2.10", "192.0.2.10:41000", []string{"198.51.100.7:5000"}, http.StatusOK, "192.0.2.10"},
		{"proxy, address with zone", "192.0.2.10", "192.0.2.10:41000", []string{"fe80::1%eth0"}, http.StatusOK, "192.0.2.10"},
		{"another host", "192.0.2.10", "192.0.2.11:41000", []string{"198.51.100.7"}, http.StatusForbidden, ""},
		{"another host claims to be the proxy", "192.0.2.10", "198.51.100.7:41000", []string{"192.0.2.10"}, http.StatusForbidden, ""},
		{"broken peer", "192.0.2.10", "nonsense", nil, http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sender string
			h := onlyProxy(netip.MustParseAddr(tc.proxy), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sender = remoteIP(r)
				w.WriteHeader(http.StatusOK)
			}), time.Now, slog.New(slog.DiscardHandler))
			req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			req.RemoteAddr = tc.peer
			for _, v := range tc.forwarded {
				req.Header.Add("X-Forwarded-For", v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus || sender != tc.wantSender {
				t.Errorf("status %d, sender %q; want %d, %q", rec.Code, sender, tc.wantStatus, tc.wantSender)
			}
			if req.RemoteAddr != tc.peer {
				t.Errorf("the server's request changed: %q", req.RemoteAddr)
			}
			if tc.wantStatus == http.StatusForbidden && rec.Body.Len() != 0 {
				t.Errorf("refusal has a body: %q", rec.Body)
			}
		})
	}
}

// Refusals are logged at most once a minute, so that nobody can fill the log.
func TestOnlyProxyLogsRefusalsOnceAMinute(t *testing.T) {
	var log bytes.Buffer
	clock := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	h := onlyProxy(netip.MustParseAddr("192.0.2.10"), http.NotFoundHandler(), func() time.Time { return clock },
		slog.New(slog.NewTextHandler(&log, nil)))
	refuse := func() {
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.RemoteAddr = "198.51.100.7:41000"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	for range 5 {
		refuse()
	}
	clock = clock.Add(59 * time.Second)
	refuse()
	if n := strings.Count(log.String(), "refused"); n != 1 {
		t.Errorf("%d log lines within a minute: %s", n, log.String())
	}
	clock = clock.Add(time.Second)
	refuse()
	if n := strings.Count(log.String(), "refused"); n != 2 || !strings.Contains(log.String(), "remote=198.51.100.7:41000") {
		t.Errorf("after a minute: %s", log.String())
	}
}

// remoteIP is the host part of RemoteAddr, as the OAuth server and the UI read it.
func remoteIP(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return ap.Addr().String()
}
