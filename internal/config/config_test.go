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
		cfg.MCPAddr != ":8765" || cfg.ApprovalTimeout != 2*time.Minute || cfg.LogLevel != slog.LevelInfo {
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
		cfg.LogLevel != slog.LevelWarn {
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
