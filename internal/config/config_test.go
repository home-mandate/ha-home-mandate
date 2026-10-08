// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"
)

// env returns a getenv for a fixed map.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// files returns a readFile for a fixed map; missing files are fs.ErrNotExist.
func files(m map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if s, ok := m[name]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
}

const options = `{"tls_certfile":"fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":120,"log_level":"info"}`

func TestLoadAppMode(t *testing.T) {
	cfg, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "sup-secret"}), files(map[string]string{"/data/options.json": options}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeApp || cfg.HAURL != "ws://supervisor/core/websocket" || string(cfg.HAToken) != "sup-secret" ||
		cfg.DataDir != "/data" || cfg.TLSCert != "/ssl/fullchain.pem" || cfg.TLSKey != "/ssl/privkey.pem" ||
		cfg.MCPAddr != ":8765" || cfg.IngressAddr != ":8099" || cfg.IngressProxy.String() != "172.30.32.2" || cfg.ApprovalTimeout != 2*time.Minute || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoadAppModeRejectsBadOptions(t *testing.T) {
	tests := map[string]string{
		"missing options":   "",
		"cert path":         `{"tls_certfile":"../etc/shadow","tls_keyfile":"privkey.pem","approval_timeout_seconds":120,"log_level":"info"}`,
		"cert subdirectory": `{"tls_certfile":"a/fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":120,"log_level":"info"}`,
		"hidden key":        `{"tls_certfile":"fullchain.pem","tls_keyfile":".key","approval_timeout_seconds":120,"log_level":"info"}`,
		"timeout too short": `{"tls_certfile":"fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":5,"log_level":"info"}`,
		"timeout too long":  `{"tls_certfile":"fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":601,"log_level":"info"}`,
		"unknown level":     `{"tls_certfile":"fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":120,"log_level":"trace"}`,
		"unknown field":     `{"tls_certfile":"fullchain.pem","tls_keyfile":"privkey.pem","approval_timeout_seconds":120,"log_level":"info","x":1}`,
		"malformed":         `{`,
		"only one tls file": `{"tls_certfile":"fullchain.pem","tls_keyfile":"","approval_timeout_seconds":120,"log_level":"info"}`,
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			fsys := map[string]string{}
			if opts != "" {
				fsys["/data/options.json"] = opts
			}
			_, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "sup-secret"}), files(fsys))
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Load = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLoadAppModeWithoutTLSListensOnLoopback(t *testing.T) {
	opts := `{"tls_certfile":"","tls_keyfile":"","approval_timeout_seconds":120,"log_level":"debug"}`
	cfg, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": opts}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPAddr != "127.0.0.1:8765" || cfg.TLSCert != "" || cfg.LogLevel != slog.LevelDebug {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoadContainerMode(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"HM_HA_URL":    "wss://ha.example.org/api/websocket",
		"HM_HA_TOKEN":  "long-lived",
		"HM_DATA_DIR":  "/var/lib/home-mandate",
		"HM_TLS_CERT":  "/certs/cert.pem",
		"HM_TLS_KEY":   "/certs/key.pem",
		"HM_MCP_ADDR":  "0.0.0.0:9000",
		"HM_LOG_LEVEL": "warning",
	}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeContainer || cfg.HAURL != "wss://ha.example.org/api/websocket" || string(cfg.HAToken) != "long-lived" ||
		cfg.DataDir != "/var/lib/home-mandate" || cfg.MCPAddr != "0.0.0.0:9000" || cfg.ApprovalTimeout != DefaultApprovalTimeout ||
		cfg.LogLevel != slog.LevelWarn || cfg.IngressAddr != "" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoadContainerModeTokenFile(t *testing.T) {
	cfg, err := Load(env(map[string]string{"HM_HA_URL": "ws://localhost:8123/api/websocket", "HM_HA_TOKEN_FILE": "/run/secrets/ha"}),
		files(map[string]string{"/run/secrets/ha": "from-file\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.HAToken) != "from-file" || cfg.DataDir != "/data" || cfg.MCPAddr != "127.0.0.1:8765" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoadContainerModeRejects(t *testing.T) {
	base := map[string]string{"HM_HA_URL": "ws://localhost:8123/api/websocket", "HM_HA_TOKEN": "t"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	tests := map[string]map[string]string{
		"no url":                   with("HM_HA_URL", ""),
		"no token":                 with("HM_HA_TOKEN", ""),
		"token and token file":     with("HM_HA_TOKEN_FILE", "/run/secrets/ha"),
		"missing token file":       with("HM_HA_TOKEN", "", "HM_HA_TOKEN_FILE", "/nope"),
		"plaintext on the lan":     with("HM_MCP_ADDR", "0.0.0.0:8765"),
		"plaintext all interfaces": with("HM_MCP_ADDR", ":8765"),
		"bad address":              with("HM_MCP_ADDR", "nonsense"),
		"only cert":                with("HM_TLS_CERT", "/c.pem"),
		"relative cert":            with("HM_TLS_CERT", "c.pem", "HM_TLS_KEY", "/k.pem"),
		"relative data dir":        with("HM_DATA_DIR", "data"),
		"approval timeout":         with("HM_APPROVAL_TIMEOUT", "5"),
		"approval timeout text":    with("HM_APPROVAL_TIMEOUT", "two minutes"),
		"log level":                with("HM_LOG_LEVEL", "loud"),
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(m), files(nil))
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Load = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestErrorsNeverContainTheToken(t *testing.T) {
	_, err := Load(env(map[string]string{"HM_HA_URL": "http://x", "HM_HA_TOKEN": "very-secret-token"}), files(nil))
	if err == nil || strings.Contains(err.Error(), "very-secret-token") {
		t.Errorf("error = %v", err)
	}
	cfg, _ := Load(env(map[string]string{"HM_HA_URL": "ws://localhost/api/websocket", "HM_HA_TOKEN": "very-secret-token"}), files(nil))
	if strings.Contains(cfg.String(), "very-secret-token") {
		t.Errorf("String() leaks the token: %s", cfg.String())
	}
}

// testCA returns a freshly generated self-signed certificate in PEM.
func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestLoadContainerModeCAFile(t *testing.T) {
	base := map[string]string{"HM_HA_URL": "wss://ha.local:8123/api/websocket", "HM_HA_TOKEN": "t"}
	with := func(k, v string) map[string]string {
		m := map[string]string{k: v}
		for kk, vv := range base {
			m[kk] = vv
		}
		return m
	}
	cfg, err := Load(env(base), files(nil))
	if err != nil || cfg.HARootCAs != nil {
		t.Fatalf("without CA file: %v, %v", cfg.HARootCAs, err)
	}
	for name, tc := range map[string]struct {
		file    string
		content string
		ok      bool
	}{
		"relative":    {"ca.pem", testCA(t), false},
		"missing":     {"/missing.pem", "", false},
		"not PEM":     {"/ca.pem", "hello", false},
		"certificate": {"/ca.pem", testCA(t), true},
	} {
		fsys := map[string]string{}
		if tc.content != "" {
			fsys[tc.file] = tc.content
		}
		cfg, err := Load(env(with("HM_HA_CA_FILE", tc.file)), files(fsys))
		if tc.ok != (err == nil) || tc.ok && cfg.HARootCAs == nil {
			t.Errorf("%s: %v, %v", name, cfg.HARootCAs, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestDataDirNeedsNoCredentials(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
		ok   bool
	}{
		{map[string]string{}, "/data", true},
		{map[string]string{"SUPERVISOR_TOKEN": "x", "HM_DATA_DIR": "/elsewhere"}, "/data", true},
		{map[string]string{"HM_DATA_DIR": "/var/lib/hm"}, "/var/lib/hm", true},
		{map[string]string{"HM_DATA_DIR": "data"}, "", false},
	} {
		got, err := DataDir(env(tc.env))
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("DataDir(%v) = %q, %v", tc.env, got, err)
		}
	}
}

func TestPDPAddrMustBeLoopback(t *testing.T) {
	base := map[string]string{"HM_HA_URL": "ws://localhost:8123/api/websocket", "HM_HA_TOKEN": "t"}
	for addr, ok := range map[string]bool{"127.0.0.1:9000": true, "0.0.0.0:9000": false, ":9000": false, "x": false} {
		m := map[string]string{"HM_PDP_ADDR": addr}
		for k, v := range base {
			m[k] = v
		}
		cfg, err := Load(env(m), files(nil))
		if (err == nil) != ok || ok && cfg.PDPAddr != addr {
			t.Errorf("HM_PDP_ADDR=%s: %+v, %v", addr, cfg.PDPAddr, err)
		}
	}
}

// The UI listener is opt-in in container mode (decision U3); its address is checked,
// the source of requests is checked by internal/api whatever the address.
func TestIngressAddr(t *testing.T) {
	base := map[string]string{"HM_HA_URL": "ws://localhost:8123/api/websocket", "HM_HA_TOKEN": "t"}
	for addr, ok := range map[string]bool{
		"":               true, // no listener, no proxy
		":8099":          true,
		"0.0.0.0:8099":   true,
		"[::]:8099":      true,
		"8099":           false,
		":0":             false,
		":65536":         false,
		":http":          false,
		"host:":          false,
		"a:b:8099":       false,
		"172.30.32.1:-1": false,
	} {
		m := map[string]string{"HM_INGRESS_ADDR": addr}
		if addr != "" {
			m["HM_INGRESS_PROXY"] = "10.0.0.2"
		}
		for k, v := range base {
			m[k] = v
		}
		cfg, err := Load(env(m), files(nil))
		if (err == nil) != ok || ok && cfg.IngressAddr != addr || !ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("HM_INGRESS_ADDR=%q: %q, %v", addr, cfg.IngressAddr, err)
		}
	}
}

// Decision U2: in container mode the proxy in front of the UI is named, exactly one IP.
func TestIngressProxy(t *testing.T) {
	base := map[string]string{"HM_HA_URL": "ws://localhost:8123/api/websocket", "HM_HA_TOKEN": "t"}
	for _, tc := range []struct {
		addr, proxy, want string
		ok                bool
	}{
		{":8099", "10.0.0.2", "10.0.0.2", true},
		{":8099", "::ffff:10.0.0.2", "10.0.0.2", true},
		{":8099", "fd00::2", "fd00::2", true},
		{":8099", "", "", false},
		{":8099", "10.0.0.0/24", "", false},
		{":8099", "10.0.0.2,10.0.0.3", "", false},
		{":8099", "0.0.0.0", "", false},
		{":8099", "::", "", false},
		{":8099", "fe80::1%eth0", "", false},
		{":8099", "224.0.0.1", "", false},
		{":8099", "proxy.local", "", false},
		{"", "10.0.0.2", "", false},
		{"", "", "invalid IP", true},
	} {
		m := map[string]string{"HM_INGRESS_ADDR": tc.addr, "HM_INGRESS_PROXY": tc.proxy}
		for k, v := range base {
			m[k] = v
		}
		cfg, err := Load(env(m), files(nil))
		if (err == nil) != tc.ok || tc.ok && cfg.IngressProxy.String() != tc.want || !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v, %v", tc, cfg.IngressProxy, err)
		}
	}
}

// Behind a reverse proxy (HM_PROXY) the proxy ends TLS: the listener may then serve
// plaintext beyond loopback, but only to that one address, and the public URL is https.
func TestProxy(t *testing.T) {
	base := map[string]string{"HM_HA_URL": "ws://localhost:8123/api/websocket", "HM_HA_TOKEN": "t",
		"HM_PUBLIC_URL": "https://hm.example.org"}
	for _, tc := range []struct {
		name               string
		set                map[string]string
		wantProxy, wantMCP string
		ok                 bool
	}{
		{"default address", map[string]string{"HM_PROXY": "192.0.2.10"}, "192.0.2.10", ":8765", true},
		{"plaintext on the lan", map[string]string{"HM_PROXY": "192.0.2.10", "HM_MCP_ADDR": "0.0.0.0:8765"}, "192.0.2.10", "0.0.0.0:8765", true},
		{"ipv4-mapped", map[string]string{"HM_PROXY": "::ffff:192.0.2.10"}, "192.0.2.10", ":8765", true},
		{"ipv6", map[string]string{"HM_PROXY": "2001:db8::10"}, "2001:db8::10", ":8765", true},
		{"with a certificate too", map[string]string{"HM_PROXY": "192.0.2.10", "HM_TLS_CERT": "/c.pem", "HM_TLS_KEY": "/k.pem"}, "192.0.2.10", ":8765", true},
		{"no proxy", nil, "", "127.0.0.1:8765", true},
		{"range", map[string]string{"HM_PROXY": "192.0.2.0/24"}, "", "", false},
		{"two addresses", map[string]string{"HM_PROXY": "192.0.2.10,192.0.2.11"}, "", "", false},
		{"unspecified", map[string]string{"HM_PROXY": "0.0.0.0"}, "", "", false},
		{"zone", map[string]string{"HM_PROXY": "fe80::1%eth0"}, "", "", false},
		{"multicast", map[string]string{"HM_PROXY": "224.0.0.1"}, "", "", false},
		{"mapped unspecified", map[string]string{"HM_PROXY": "::ffff:0.0.0.0"}, "", "", false},
		{"mapped multicast", map[string]string{"HM_PROXY": "::ffff:224.0.0.1"}, "", "", false},
		{"host name", map[string]string{"HM_PROXY": "traefik"}, "", "", false},
		{"no public url", map[string]string{"HM_PROXY": "192.0.2.10", "HM_PUBLIC_URL": ""}, "", "", false},
		{"plaintext public url", map[string]string{"HM_PROXY": "127.0.0.1", "HM_PUBLIC_URL": "http://localhost:8765"}, "", "", false},
		{"bad address", map[string]string{"HM_PROXY": "192.0.2.10", "HM_MCP_ADDR": "nonsense"}, "", "", false},
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range tc.set {
			m[k] = v
		}
		cfg, err := Load(env(m), files(nil))
		if (err == nil) != tc.ok || !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		got := ""
		if cfg.Proxy.IsValid() {
			got = cfg.Proxy.String()
		}
		if tc.ok && (got != tc.wantProxy || cfg.MCPAddr != tc.wantMCP) {
			t.Errorf("%s: proxy %v, mcp %q", tc.name, cfg.Proxy, cfg.MCPAddr)
		}
	}
}

// HM_PROXY is not available in app mode yet; set there, it stops the start rather than
// being ignored.
func TestProxyRefusedInAppMode(t *testing.T) {
	_, err := Load(env(map[string]string{"SUPERVISOR_TOKEN": "s", "HM_PROXY": "192.0.2.10"}), files(map[string]string{appOptions: options}))
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Load = %v, want ErrInvalid", err)
	}
}
