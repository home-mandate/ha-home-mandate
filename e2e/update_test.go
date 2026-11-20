// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"
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
	checkVersionMove(t, logBefore, logAfter)
	checkDowngrade(t, os.Getenv("E2E_IMAGE"), next)
}

// checkVersionMove: a log of audit/v0 moves on in audit/v1 (SPEC-v0 section 14). The
// entries of before stay as they are, and a checkpoint of version 0 is the last entry
// before the first of version 1, so that tools that know only version 0 can verify them.
func checkVersionMove(t *testing.T, before, after string) {
	t.Helper()
	const v0 = `"type":"https://home-mandate.org/audit/v0"`
	if !strings.Contains(before, v0) {
		t.Log("the previous version wrote audit/v1 already: no move to check")
		return
	}
	lines := strings.Split(strings.TrimSpace(after), "\n")
	first := -1
	for i, line := range lines {
		if strings.Contains(line, typeV1) {
			first = i
			break
		}
	}
	if first < 1 {
		t.Fatalf("no entry of audit/v1 after the update, or no entry before it")
	}
	if prev := lines[first-1]; !strings.Contains(prev, v0) || !strings.Contains(prev, `"event":"log.checkpoint"`) {
		t.Errorf("the entry before the first of audit/v1 is no checkpoint of audit/v0: %s", prev)
	}
	for _, line := range lines[first:] {
		if !strings.Contains(line, typeV1) {
			t.Errorf("an entry of audit/v0 after one of audit/v1: %s", line)
		}
	}
}

// checkDowngrade starts the previous version on the data the new one wrote in audit/v1
// (A-E3 of the plan of PR A). The previous version must not append to that log: either it
// refuses the log, or it writes nothing. The new version then starts again on a valid log.
func checkDowngrade(t *testing.T, previous, next string) {
	t.Helper()
	logBefore := strings.TrimRight(auditLog(t), "\n")
	if _, err := run("rm", "-f", env.hm); err != nil {
		t.Fatal(err)
	}
	if err := runGateway(previous); err != nil {
		t.Logf("the previous version refuses the log of the new one: %v", err)
	} else {
		t.Log("the previous version starts on the log of the new one")
	}
	time.Sleep(5 * time.Second) // a start writes its entries at once, if it writes any
	if _, err := run("rm", "-f", env.hm); err != nil {
		t.Fatal(err)
	}
	if err := runGateway(next); err != nil {
		t.Fatalf("the new version does not start again after the downgrade: %v", err)
	}
	logAgain := auditLog(t)
	if !strings.HasPrefix(logAgain, logBefore) {
		t.Fatal("the log changed before its end during the downgrade")
	}
	for _, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(logAgain, logBefore)), "\n") {
		if line != "" && !strings.Contains(line, typeV1) {
			t.Errorf("the previous version appended to the log of the new one: %s", line)
		}
	}
}
