// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// An update with existing data (RELEASING.md "How a version is tested", make e2e-update):
// the gateway of E2E_IMAGE, the previous version, pairs an agent, stores its mandate and logs
// a read; then the gateway of E2E_UPDATE_IMAGE starts on the same data directory. Agents,
// mandates, the agent's token and the audit log stay, the log stays valid, every stored
// mandate still parses, and the agent reads again. Without E2E_UPDATE_IMAGE the test is
// skipped: it replaces the gateway, so it runs on its own.
func TestUpdateKeepsAgentsMandatesAndLog(t *testing.T) {
	next := os.Getenv("E2E_UPDATE_IMAGE")
	if next == "" {
		t.Skip("E2E_UPDATE_IMAGE not set (make e2e-update FROM=<image> TO=<image>)")
	}
	token := newAgent(t, "Update", readAnything)
	s := session(t, token)
	ready(t, s)
	if _, errText := call(t, s, "get_state", map[string]any{"entity_id": "light.kitchen_lights"}); errText != "" {
		t.Fatalf("get_state before the update: %s", errText)
	}
	agents := cli(t, "", "agent", "list")
	mandates := cli(t, "", "mandate", "list")
	logBefore := strings.TrimRight(auditLog(t), "\n")
	t.Logf("from %s", strings.TrimSpace(cli(t, "", "--version")))

	if _, err := run("rm", "-f", env.hm); err != nil {
		t.Fatal(err)
	}
	if err := runGateway(next); err != nil {
		t.Fatalf("the new version does not start on the data of the previous one: %v", err)
	}
	t.Logf("to %s", strings.TrimSpace(cli(t, "", "--version")))

	if got := cli(t, "", "agent", "list"); got != agents {
		t.Errorf("agents after the update:\n%s\nwant\n%s", got, agents)
	}
	if got := cli(t, "", "mandate", "list"); got != mandates {
		t.Errorf("mandates after the update:\n%s\nwant\n%s", got, mandates)
	}
	cli(t, "", "mandate", "check") // fails the test if a stored mandate no longer parses
	if !strings.HasPrefix(auditLog(t), logBefore) {
		t.Error("the audit log after the update does not start with the log before it")
	}

	s = session(t, token)
	ready(t, s)
	out, errText := call(t, s, "get_state", map[string]any{"entity_id": "light.kitchen_lights"})
	if errText != "" || out["state"] == "" {
		t.Fatalf("get_state after the update = %v, %q", out, errText)
	}
	logAfter := auditLog(t)
	if !strings.HasPrefix(logAfter, logBefore) {
		t.Error("the audit log lost entries after the first request of the new version")
	}
	if !hasLine(strings.TrimPrefix(logAfter, logBefore), `"entity_id":"light.kitchen_lights"`, `"action":"read"`) {
		t.Error("the read after the update is not in the audit log")
	}
}
