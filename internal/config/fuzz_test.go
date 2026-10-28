// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"testing"
)

// FuzzAppOptions: the app's options.json is written by the Supervisor from what the
// owner typed. Whatever it holds, Load refuses it with ErrInvalid or returns a
// configuration whose certificate paths stay in /ssl and whose URLs are origins.
func FuzzAppOptions(f *testing.F) {
	for _, seed := range []string{
		options, `{}`, `{"log_level":"debug","approval_wait_seconds":30}`,
		`{"tls_certfile":"../etc/shadow","tls_keyfile":"privkey.pem"}`, `{"tls_certfile":"a.pem"}`,
		`{"public_url":"https://hm.example.org","ha_browser_url":"https://ha.example.org:8123"}`,
		`{"public_url":"http://hm.example.org"}`, `{"public_url":"https://user@hm.example.org/x?y#z"}`,
		`{"approval_timeout_seconds":-1}`, `{"approval_timeout_seconds":1e99}`, `{"unknown":true}`, `{} {}`, `[`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := load(env(map[string]string{"SUPERVISOR_TOKEN": "s"}), files(map[string]string{"/data/options.json": string(data)}))
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("undefined error %v", err)
			}
			return
		}
		for _, p := range []string{cfg.TLSCert, cfg.TLSKey} {
			if p != "" && (path.Dir(p) != "/ssl" || path.Clean(p) != p) {
				t.Fatalf("certificate path %q outside /ssl", p)
			}
		}
		for _, s := range []string{cfg.PublicURL, cfg.HABrowserURL} {
			if s == "" {
				continue
			}
			u, err := url.Parse(s)
			if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" ||
				u.Scheme != "https" && u.Scheme != "http" {
				t.Fatalf("URL %q is no origin", s)
			}
		}
		if cfg.ApprovalTimeout <= 0 || cfg.ApprovalWait <= 0 {
			t.Fatalf("timeouts %v, %v", cfg.ApprovalTimeout, cfg.ApprovalWait)
		}
	})
}
