// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/config"
	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
	"github.com/home-mandate/ha-home-mandate/internal/tlscert"
)

// Issue #66: the monitoring runs when switched on and the public URL has a public https
// name; otherwise the system status says why.
func TestCTWatchOffOrNotApplicable(t *testing.T) {
	for _, tc := range []struct {
		cfg           config.Config
		state, reason string
	}{
		{config.Config{CTWatch: false, PublicURL: "https://hm.example.org"}, "off", ""},
		{config.Config{CTWatch: true}, "not_applicable", "no_public_url"},
		{config.Config{CTWatch: true, PublicURL: "https://localhost:8765"}, "not_applicable", "not_public_name"},
	} {
		g := &gateway{state: &state{cfg: tc.cfg}}
		got, err := g.newCTWatch(context.Background(), nil, nil, slog.New(slog.DiscardHandler))
		if err != nil || got.watcher != nil || got.state != tc.state || got.reason != tc.reason {
			t.Errorf("%+v: %+v %v", tc.cfg, got, err)
		}
	}
}

// The own key comes from the certificate files, or from the reverse proxy at its
// configured address with the public URL's port, never through DNS.
func TestCTKeySource(t *testing.T) {
	g := &gateway{state: &state{cfg: config.Config{}}}
	if k, err := g.ctKey("hm.example.org"); k != nil || err != nil {
		t.Fatalf("TLS elsewhere: %v %v", k, err)
	}
	g.certs = &tlscert.Loader{}
	if k, _ := g.ctKey("hm.example.org"); k != (ctwatch.FileKey{Loader: g.certs}) {
		t.Fatalf("files: %#v", k)
	}
	g.certs = nil
	proxy := netip.MustParseAddr("192.0.2.10")
	for publicURL, port := range map[string]uint16{"https://hm.example.org": 443, "https://hm.example.org:8443/x": 8443} {
		g.state.cfg = config.Config{Proxy: proxy, PublicURL: publicURL}
		k, err := g.ctKey("hm.example.org")
		p, ok := k.(ctwatch.ProxyKey)
		if err != nil || !ok || p.Addr != netip.AddrPortFrom(proxy, port) || p.ServerName != "hm.example.org" {
			t.Errorf("%s: %#v %v", publicURL, k, err)
		}
	}
	for _, bad := range []string{"https://hm.example.org:0", "https://hm.example.org:99999", "https://%zz"} {
		g.state.cfg = config.Config{Proxy: proxy, PublicURL: bad}
		if _, err := g.ctKey("hm.example.org"); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestCTLink(t *testing.T) {
	g := &gateway{state: &state{cfg: config.Config{PublicURL: "https://hm.example.org"}}}
	if g.uiLink() != "" {
		t.Fatal("no direct mode, no app")
	}
	g.slug = "abc_home_mandate"
	if g.uiLink() != "/app/abc_home_mandate" {
		t.Fatalf("app: %q", g.uiLink())
	}
	g.direct = http.NotFoundHandler()
	if g.uiLink() != "https://hm.example.org/ui/" {
		t.Fatalf("direct: %q", g.uiLink())
	}
}
