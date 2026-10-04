// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// This file runs last (go test runs the files in name order).

// TESTING.md §4: no tokens or Home Assistant credentials in the logs of any run.
func TestZZLogsContainNoSecrets(t *testing.T) {
	logs := logsOf(env.hm)
	if !strings.Contains(logs, "home-mandate started") {
		t.Fatalf("unexpected gateway logs:\n%s", logs)
	}
	for _, secret := range env.secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Error("a token appears in the gateway logs")
		}
	}
	// The UI's search text, CSRF tokens and pairing codes never appear either (TESTING.md §4).
	for _, prefix := range []string{"hma_", "hmr_", "hmd_", "hmc_", "HM_APPROVE_", "HM_DENY_", "Haus-T\u00fcr-Suche"} {
		if strings.Contains(logs, prefix) {
			t.Errorf("something that looks like a token, code or nonce (%s) appears in the gateway logs", prefix)
		}
	}
}
