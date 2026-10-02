// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	mandatespec "github.com/mandate-spec/mandate-spec"
)

// cli runs commands against one temporary data directory in container mode.
type cli struct {
	t       *testing.T
	envVars map[string]string
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &cli{t: t, envVars: map[string]string{
		"HM_HA_URL":   "ws://localhost:1/api/websocket",
		"HM_HA_TOKEN": "test-token",
		"HM_DATA_DIR": dir,
	}}
}

func (c *cli) env(stdin string) (env, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return env{
		getenv:   func(k string) string { return c.envVars[k] },
		readFile: os.ReadFile,
		stdin:    strings.NewReader(stdin),
		stdout:   &stdout,
		stderr:   &stderr,
	}, &stdout, &stderr
}

func (c *cli) run(stdin string, args ...string) (int, string, string) {
	c.t.Helper()
	e, stdout, stderr := c.env(stdin)
	code := run(context.Background(), args, e)
	return code, stdout.String(), stderr.String()
}

func (c *cli) mustRun(stdin string, args ...string) string {
	c.t.Helper()
	code, out, errOut := c.run(stdin, args...)
	if code != exitOK {
		c.t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

func field(t *testing.T, out, key string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + key + `=(\S+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no %s in %q", key, out)
	}
	return m[1]
}

func mandateFor(t *testing.T, household, clientID string) string {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	doc["principal"] = household
	doc["agent"] = map[string]any{"client_id": clientID, "display_name": "Voice assistant"}
	out, _ := json.Marshal(doc)
	return string(out)
}

// tamper changes the first audit entry behind the store's back.
func tamper(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE audit_log SET entry = replace(entry, '"display_name":"A"', '"display_name":"Z"') WHERE seq = 1`)
	return err
}

func TestAgentAndMandateLifecycle(t *testing.T) {
	c := newCLI(t)

	household := strings.TrimSpace(c.mustRun("", "household"))
	if !regexp.MustCompile(`^household:hm-[0-9a-f]{12}$`).MatchString(household) {
		t.Fatalf("household = %q", household)
	}

	out := c.mustRun("", "agent", "add", "--name", "Voice assistant", "--days", "7")
	clientID, token := field(t, out, "client_id"), field(t, out, "token")
	if !strings.HasPrefix(clientID, "hm-client:voice-assistant-") || !strings.HasPrefix(token, "hma_") {
		t.Errorf("agent add output: %q", out)
	}
	if list := c.mustRun("", "agent", "list"); !strings.Contains(list, clientID) || !strings.Contains(list, "active") || strings.Contains(list, token) {
		t.Errorf("agent list: %q", list)
	}

	path := filepath.Join(t.TempDir(), "mandate.json")
	if err := os.WriteFile(path, []byte(mandateFor(t, household, clientID)), 0o600); err != nil {
		t.Fatal(err)
	}
	out = c.mustRun("", "mandate", "import", path)
	if field(t, out, "id") != "m-voice-assistant" || !strings.HasPrefix(field(t, out, "digest"), "sha256:") {
		t.Errorf("mandate import: %q", out)
	}
	// From stdin, unchanged: same digest.
	if again := c.mustRun(mandateFor(t, household, clientID), "mandate", "import", "-"); field(t, again, "digest") != field(t, out, "digest") {
		t.Errorf("reimport changed the digest: %q", again)
	}
	if list := c.mustRun("", "mandate", "list"); !strings.Contains(list, "m-voice-assistant") || !strings.Contains(list, clientID) {
		t.Errorf("mandate list: %q", list)
	}

	c.mustRun("", "mandate", "revoke", "m-voice-assistant")
	c.mustRun("", "agent", "revoke", clientID)
	if list := c.mustRun("", "agent", "list"); !strings.Contains(list, "revoked") {
		t.Errorf("agent list after revoke: %q", list)
	}

	if out := c.mustRun("", "audit", "verify"); !strings.Contains(out, "valid") {
		t.Errorf("audit verify: %q", out)
	}
	export := c.mustRun("", "audit", "export")
	if strings.Count(export, "\n") != 4 || strings.Contains(export, token) {
		t.Errorf("audit export (agent.registered, mandate.created, mandate.revoked, agent.revoked):\n%s", export)
	}
}

func TestAuditVerifyReportsABrokenChain(t *testing.T) {
	c := newCLI(t)
	c.mustRun("", "agent", "add", "--name", "A")
	c.mustRun("", "agent", "add", "--name", "B")
	if err := tamper(filepath.Join(c.envVars["HM_DATA_DIR"], "home-mandate.db")); err != nil {
		t.Fatal(err)
	}
	code, out, _ := c.run("", "audit", "verify")
	if code != exitFailure || !strings.Contains(out, "broken at seq 2") {
		t.Errorf("audit verify = %d, %q", code, out)
	}
}

func TestCommandErrors(t *testing.T) {
	c := newCLI(t)
	tests := []struct {
		args []string
		code int
	}{
		{[]string{"agent"}, exitUsage},
		{[]string{"agent", "fly"}, exitUsage},
		{[]string{"agent", "add"}, exitUsage},
		{[]string{"agent", "add", "--name", "x", "--days", "0"}, exitUsage},
		{[]string{"agent", "add", "--name", "x", "--days", "400"}, exitUsage},
		{[]string{"agent", "add", "--name", "bad\u202ename"}, exitFailure},
		{[]string{"agent", "revoke"}, exitUsage},
		{[]string{"agent", "revoke", "hm-client:nobody-00000000"}, exitFailure},
		{[]string{"mandate"}, exitUsage},
		{[]string{"mandate", "import"}, exitUsage},
		{[]string{"mandate", "import", "/does/not/exist.json"}, exitFailure},
		{[]string{"mandate", "revoke", "m-none"}, exitFailure},
		{[]string{"audit"}, exitUsage},
		{[]string{"audit", "rewrite"}, exitUsage},
		{[]string{"household", "extra"}, exitUsage},
		{[]string{"frobnicate"}, exitUsage},
	}
	for _, tt := range tests {
		code, _, stderr := c.run("", tt.args...)
		if code != tt.code {
			t.Errorf("%v: exit %d, want %d (stderr %q)", tt.args, code, tt.code, stderr)
		}
	}
	code, _, _ := c.run("{", "mandate", "import", "-")
	if code != exitFailure {
		t.Errorf("import of malformed JSON: exit %d", code)
	}
}

func TestCommandsNeedAValidConfiguration(t *testing.T) {
	c := newCLI(t)
	c.envVars["HM_HA_URL"] = ""
	code, _, stderr := c.run("", "agent", "list")
	if code != exitFailure || !strings.Contains(stderr, "HM_HA_URL") || strings.Contains(stderr, "test-token") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}
