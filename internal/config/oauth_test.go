// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

func containerEnv(kv ...string) map[string]string {
	m := map[string]string{"HM_HA_URL": "wss://ha.example.org/api/websocket", "HM_HA_TOKEN": "t"}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestOAuthURLsContainerMode(t *testing.T) {
	tests := map[string]struct {
		env                     map[string]string
		public, browser, haHTTP string
	}{
		"oauth off": {containerEnv(), "", "https://ha.example.org", "https://ha.example.org"},
		"public url normalized": {containerEnv("HM_PUBLIC_URL", "https://hm.example.org:8765/"),
			"https://hm.example.org:8765", "https://ha.example.org", "https://ha.example.org"},
		"localhost without tls": {containerEnv("HM_PUBLIC_URL", "http://localhost:8765"),
			"http://localhost:8765", "https://ha.example.org", "https://ha.example.org"},
		"loopback ip": {containerEnv("HM_PUBLIC_URL", "http://127.0.0.1:8765"),
			"http://127.0.0.1:8765", "https://ha.example.org", "https://ha.example.org"},
		"browser url": {containerEnv("HM_PUBLIC_URL", "https://hm.lan", "HM_HA_BROWSER_URL", "http://homeassistant.local:8123/"),
			"https://hm.lan", "http://homeassistant.local:8123", "https://ha.example.org"},
		"plaintext ha": {containerEnv("HM_HA_URL", "ws://localhost:8123/api/websocket"),
			"", "http://localhost:8123", "http://localhost:8123"},
		"ha with port": {containerEnv("HM_HA_URL", "wss://homeassistant:8123/api/websocket"),
			"", "https://homeassistant:8123", "https://homeassistant:8123"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(env(tc.env), files(nil))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PublicURL != tc.public || cfg.HABrowserURL != tc.browser || cfg.HAHTTPURL != tc.haHTTP {
				t.Errorf("public=%q browser=%q haHTTP=%q", cfg.PublicURL, cfg.HABrowserURL, cfg.HAHTTPURL)
			}
		})
	}
}

func TestOAuthURLsRejected(t *testing.T) {
	tests := map[string]map[string]string{
		"plaintext on the lan":  containerEnv("HM_PUBLIC_URL", "http://hm.lan:8765"),
		"plaintext private ip":  containerEnv("HM_PUBLIC_URL", "http://192.168.1.5:8765"),
		"other scheme":          containerEnv("HM_PUBLIC_URL", "ftp://hm.lan"),
		"relative":              containerEnv("HM_PUBLIC_URL", "hm.lan:8765"),
		"no host":               containerEnv("HM_PUBLIC_URL", "https://"),
		"query":                 containerEnv("HM_PUBLIC_URL", "https://hm.lan/?a=b"),
		"fragment":              containerEnv("HM_PUBLIC_URL", "https://hm.lan/#x"),
		"user info":             containerEnv("HM_PUBLIC_URL", "https://u:p@hm.lan"),
		"path":                  containerEnv("HM_PUBLIC_URL", "https://hm.lan/hm"),
		"control characters":    containerEnv("HM_PUBLIC_URL", "https://hm.lan/\n"),
		"browser url scheme":    containerEnv("HM_HA_BROWSER_URL", "wss://ha.lan"),
		"browser url query":     containerEnv("HM_HA_BROWSER_URL", "https://ha.lan/?x=1"),
		"browser url user info": containerEnv("HM_HA_BROWSER_URL", "https://u@ha.lan"),
		"browser url path":      containerEnv("HM_HA_BROWSER_URL", "https://ha.lan/lovelace"),
		"browser url relative":  containerEnv("HM_HA_BROWSER_URL", "ha.lan"),
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(m), files(nil)); !errors.Is(err, ErrInvalid) {
				t.Errorf("Load = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestOAuthURLsAppMode(t *testing.T) {
	opts := `{"tls_certfile":"fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":120,"log_level":"info",` +
		`"public_url":"https://hm.example.org:8765","ha_browser_url":"https://ha.example.org"}`
	cfg, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": opts}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://hm.example.org:8765" || cfg.HABrowserURL != "https://ha.example.org" ||
		cfg.HAHTTPURL != "http://homeassistant:8123" {
		t.Errorf("config = %+v", cfg)
	}

	// Without the options, OAuth is off: no public URL and no browser URL to guess.
	cfg, err = Load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": options}))
	if err != nil || cfg.PublicURL != "" || cfg.HABrowserURL != "" {
		t.Errorf("without options: %+v, %v", cfg, err)
	}

	noBrowser := strings.Replace(opts, `,"ha_browser_url":"https://ha.example.org"`, ``, 1)
	if _, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": noBrowser})); !errors.Is(err, ErrInvalid) {
		t.Errorf("public_url without ha_browser_url: %v", err)
	}

	bad := strings.Replace(opts, "https://hm.example.org:8765", "http://hm.lan", 1)
	if _, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": bad})); !errors.Is(err, ErrInvalid) {
		t.Errorf("plaintext public_url: %v", err)
	}
}

// The Home Assistant URLs of each mode are accepted by the Home Assistant client: in app
// mode plaintext is allowed to the Supervisor and Home Assistant on the hassio network
// only, in container mode to loopback only.
func TestPlaintextHostsPerMode(t *testing.T) {
	opts := `{"approval_timeout_seconds":120,"public_url":"https://hm.example.org:8765","ha_browser_url":"https://ha.example.org"}`
	app, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": opts}))
	if err != nil {
		t.Fatal(err)
	}
	want := ha.Plaintext{Hosts: []string{"supervisor", "homeassistant"}, Network: netip.MustParsePrefix("172.30.32.0/23")}
	if !reflect.DeepEqual(app.HAPlaintext, want) || !want.Network.Contains(SupervisorAddr) {
		t.Errorf("app mode plaintext = %+v", app.HAPlaintext)
	}
	if _, err := ha.HTTPClient(app.HAHTTPURL, nil, app.HAPlaintext); err != nil {
		t.Errorf("app mode Home Assistant HTTP URL refused: %v", err)
	}
	if _, err := ha.New(ha.Config{URL: app.HAURL, Token: "t", Plaintext: app.HAPlaintext}); err != nil {
		t.Errorf("app mode Home Assistant URL refused: %v", err)
	}

	container, err := Load(env(containerEnv("HM_HA_URL", "ws://localhost:8123/api/websocket")), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(container.HAPlaintext, ha.Plaintext{}) {
		t.Errorf("container mode plaintext = %+v, want loopback only", container.HAPlaintext)
	}
	if _, err := ha.HTTPClient("http://homeassistant:8123", nil, container.HAPlaintext); !errors.Is(err, ha.ErrInsecureURL) {
		t.Errorf("container mode plaintext to homeassistant: %v", err)
	}
	if _, err := ha.HTTPClient("http://supervisor", nil, container.HAPlaintext); !errors.Is(err, ha.ErrInsecureURL) {
		t.Errorf("container mode plaintext to supervisor: %v", err)
	}
}

func TestStringShowsThePublicURL(t *testing.T) {
	cfg, err := Load(env(containerEnv("HM_PUBLIC_URL", "https://hm.lan")), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.String(), "public=https://hm.lan") {
		t.Errorf("String() = %s", cfg.String())
	}
}
