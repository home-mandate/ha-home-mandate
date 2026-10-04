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

	"github.com/home-mandate/home-mandate/internal/approval"
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
		"HM_MCP_ADDR": "127.0.0.1:0",
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

// register admits an agent directly in the store, as the OAuth admission does; the
// administration commands cannot add agents.
func (c *cli) register(name string) string {
	c.t.Helper()
	s, err := openStore(context.Background(), c.envVars["HM_DATA_DIR"])
	if err != nil {
		c.t.Fatal(err)
	}
	defer s.store.Close()
	a, err := s.agents.Register(context.Background(), name, localAdmin)
	if err != nil {
		c.t.Fatal(err)
	}
	return a.ClientID
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

	clientID := c.register("Voice assistant")
	if list := c.mustRun("", "agent", "list"); !strings.Contains(list, clientID) || !strings.Contains(list, "active") {
		t.Errorf("agent list: %q", list)
	}

	path := filepath.Join(t.TempDir(), "mandate.json")
	if err := os.WriteFile(path, []byte(mandateFor(t, household, clientID)), 0o600); err != nil {
		t.Fatal(err)
	}
	out := c.mustRun("", "mandate", "import", path)
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
	if strings.Count(export, "\n") != 4 {
		t.Errorf("audit export (agent.registered, mandate.created, mandate.revoked, agent.revoked):\n%s", export)
	}
}

func TestAuditVerifyReportsABrokenChain(t *testing.T) {
	c := newCLI(t)
	c.register("A")
	c.register("B")
	if err := tamper(filepath.Join(c.envVars["HM_DATA_DIR"], databaseFile)); err != nil {
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
		{[]string{"agent", "add", "--name", "x"}, exitUsage}, // agents are admitted via OAuth only
		{[]string{"emergency-stop"}, exitUsage},
		{[]string{"approver"}, exitUsage},
		{[]string{"approver", "add", "u1"}, exitUsage},
		{[]string{"approver", "add", "u1", "notify.x"}, exitFailure},
		{[]string{"approver", "add", "u1", "mobile_app_a,mobile_app_a"}, exitFailure},
		{[]string{"approver", "add", "u1", ","}, exitFailure},
		{[]string{"approver", "add", "u1", "mobile_app_a:critical"}, exitFailure},
		{[]string{"approver", "add", "u1", "mobile_app_a:no-critical:no-critical"}, exitFailure},
		{[]string{"approver", "remove", "none"}, exitFailure},
		{[]string{"emergency-stop", "maybe"}, exitUsage},
		{[]string{"emergency-stop", "on", "now"}, exitUsage},
		{[]string{"agent", "revoke"}, exitUsage},
		{[]string{"agent", "revoke", "hm-client:nobody-00000000"}, exitFailure},
		{[]string{"mandate"}, exitUsage},
		{[]string{"mandate", "import"}, exitUsage},
		{[]string{"mandate", "import", "/does/not/exist.json"}, exitFailure},
		{[]string{"mandate", "revoke", "m-none"}, exitFailure},
		{[]string{"mandate", "template"}, exitUsage},
		{[]string{"mandate", "template", "import", "x"}, exitUsage},
		{[]string{"mandate", "template", "remove", "none"}, exitFailure},
		{[]string{"mandate", "template", "import", "Bad Name", "-"}, exitFailure},
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

func TestAdministrationNeedsNoHomeAssistantCredentials(t *testing.T) {
	c := newCLI(t)
	c.envVars["HM_HA_URL"], c.envVars["HM_HA_TOKEN"] = "", ""
	if code, _, stderr := c.run("", "agent", "list"); code != exitOK {
		t.Errorf("agent list without HA credentials: exit %d, %s", code, stderr)
	}
	c.envVars["HM_DATA_DIR"] = "relative"
	if code, _, stderr := c.run("", "agent", "list"); code != exitFailure || !strings.Contains(stderr, "HM_DATA_DIR") {
		t.Errorf("relative data dir: exit %d, %s", code, stderr)
	}
}

func TestImportLimitsTheFileSize(t *testing.T) {
	c := newCLI(t)
	path := filepath.Join(t.TempDir(), "huge.json")
	if err := os.WriteFile(path, []byte(`{"x":"`+strings.Repeat("a", 300<<10)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := c.run("", "mandate", "import", path); code != exitFailure || !strings.Contains(stderr, "too large") {
		t.Errorf("exit %d, %s", code, stderr)
	}
}

func TestEmergencyStopCommand(t *testing.T) {
	c := newCLI(t)
	if out := c.mustRun("", "emergency-stop", "status"); strings.TrimSpace(out) != "emergency stop: off" {
		t.Errorf("status = %q", out)
	}
	if out := c.mustRun("", "emergency-stop", "on"); !strings.Contains(out, "activated") {
		t.Errorf("on = %q", out)
	}
	if out := c.mustRun("", "emergency-stop", "on"); !strings.Contains(out, "already on") {
		t.Errorf("second on = %q", out)
	}
	if out := c.mustRun("", "emergency-stop", "status"); strings.TrimSpace(out) != "emergency stop: on" {
		t.Errorf("status = %q", out)
	}
	if out := c.mustRun("", "emergency-stop", "off"); !strings.Contains(out, "released") {
		t.Errorf("off = %q", out)
	}
	export := c.mustRun("", "audit", "export")
	if !strings.Contains(export, `"event":"emergency_stop.activated"`) || !strings.Contains(export, `"event":"emergency_stop.released"`) ||
		!strings.Contains(export, `"id":"local-admin"`) {
		t.Errorf("audit export:\n%s", export)
	}
}

func TestTemplateCommands(t *testing.T) {
	c := newCLI(t)
	household := strings.TrimSpace(c.mustRun("", "household"))
	c.mustRun(mandateFor(t, household, "hm-client:placeholder-00000000"), "mandate", "template", "import", "voice-assistant", "-")
	if out := c.mustRun("", "mandate", "template", "list"); !strings.HasPrefix(out, "voice-assistant\t") || !strings.Contains(out, "local-admin") {
		t.Errorf("template list = %q", out)
	}
	c.mustRun("", "mandate", "template", "remove", "voice-assistant")
	if out := c.mustRun("", "mandate", "template", "list"); out != "" {
		t.Errorf("template list after remove = %q", out)
	}
}

func TestApproverCommands(t *testing.T) {
	c := newCLI(t)
	c.mustRun("", "approver", "add", "1a2b3c", "mobile_app_pixel_9,mobile_app_mac:no-critical", "de")
	c.mustRun("", "approver", "add", "4d5e6f", "mobile_app_iphone")
	out := c.mustRun("", "approver", "list")
	if out != "1a2b3c\tnotify.mobile_app_mac:no-critical,notify.mobile_app_pixel_9\tde\t-\n4d5e6f\tnotify.mobile_app_iphone\thousehold\t-\n" {
		t.Errorf("approver list = %q", out)
	}
	// The UI channel is set in the UI only (administrators); adding devices on the
	// command line keeps it.
	e, _, _ := c.env("")
	s, err := openState(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.approvers.Put(context.Background(), approval.Approver{UserID: "4d5e6f", Devices: []approval.Device{{Service: "mobile_app_iphone", Critical: true}},
		UI: true, UICritical: true}); err != nil {
		t.Fatal(err)
	}
	_ = s.store.Close()
	c.mustRun("", "approver", "add", "4d5e6f", "mobile_app_ipad")
	if out := c.mustRun("", "approver", "list"); !strings.Contains(out, "4d5e6f\tnotify.mobile_app_ipad\thousehold\tui+critical\n") {
		t.Errorf("UI channel lost: %q", out)
	}
	c.mustRun("", "approver", "remove", "1a2b3c")
	if out := c.mustRun("", "approver", "list"); strings.Contains(out, "1a2b3c") {
		t.Errorf("after remove: %q", out)
	}
}

// "mandate check" tells before an update of the specification which stored mandates and
// templates would deny everything afterwards.
func TestMandateCheck(t *testing.T) {
	c := newCLI(t)
	clientID := c.register("Voice assistant")
	household := strings.TrimSpace(c.mustRun("", "household"))
	c.mustRun(mandateFor(t, household, clientID), "mandate", "import", "-")
	if out := c.mustRun("", "mandate", "check"); !strings.Contains(out, "ok") {
		t.Errorf("check on valid mandates: %q", out)
	}
	db, err := sql.Open("sqlite", filepath.Join(c.envVars["HM_DATA_DIR"], databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE mandate_versions SET document = replace(document, '"default"', '"unknown_member":1,"default"')`); err != nil {
		t.Fatal(err)
	}
	code, out, _ := c.run("", "mandate", "check")
	if code != exitFailure || !strings.Contains(out, "m-voice-assistant") || !strings.Contains(out, clientID) {
		t.Errorf("check with an invalid mandate: exit %d, %q", code, out)
	}
	if code, _, _ := c.run("", "mandate", "check", "extra"); code != exitUsage {
		t.Errorf("check with an argument: exit %d", code)
	}
}

// "audit key" prints what a verifier outside this device needs; "audit verify" says how
// far the log is anchored by checkpoints.
func TestAuditKeyAndAnchoredVerify(t *testing.T) {
	c := newCLI(t)
	c.register("Voice assistant")
	key := c.mustRun("", "audit", "key")
	logID := field(t, key, "log_id")
	if len(logID) != 36 || !strings.Contains(key, `"kty": "OKP"`) || strings.Contains(key, `"d"`) {
		t.Fatalf("audit key: %q", key)
	}
	out := c.mustRun("", "audit", "verify")
	if field(t, out, "log_id") != logID || field(t, out, "anchored_up_to") != "0" || field(t, out, "entries") == "0" {
		t.Errorf("audit verify: %q", out)
	}
	if again := c.mustRun("", "audit", "key"); again != key {
		t.Error("the key or the log ID changed between two calls")
	}
	c.envVars[envAuditKeyFile] = filepath.Join(c.envVars["HM_DATA_DIR"], "no-such-directory", "key")
	if code, _, errOut := c.run("", "audit", "verify"); code != exitFailure || !strings.Contains(errOut, "audit checkpoint key") {
		t.Errorf("key file that cannot be created: exit %d, %q", code, errOut)
	}
}

// The issuer of the mandates (SPEC-v0 section 3.5) is created once and never changes:
// a mandate with a version only follows one of the same issuer.
func TestTheMandateIssuerStaysTheSame(t *testing.T) {
	dir := t.TempDir()
	issuers := make([]string, 2)
	for i := range issuers {
		s, err := openStore(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		issuers[i], err = mandateIssuer(context.Background(), s.store)
		_ = s.store.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(issuers[0]) ||
		issuers[0] != issuers[1] {
		t.Errorf("issuers = %v", issuers)
	}
}
