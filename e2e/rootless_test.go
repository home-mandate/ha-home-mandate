// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// Container mode runs unprivileged (README, "Running in container mode"): the gateway of
// all scenarios runs as the user running the tests and does not warn about root.
func TestGatewayRunsUnprivileged(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("run by root: the gateway runs as root as well (gatewayUser)")
	}
	logs := logsOf(env.hm)
	if !strings.Contains(logs, "MCP endpoint listening") {
		t.Fatalf("unexpected gateway logs:\n%s", logs)
	}
	if strings.Contains(logs, "running as root") {
		t.Error("the gateway runs as root")
	}
}

// A data directory of another user, as after an upgrade from a container that ran as
// root, stops the start with the command that hands it over. The directory is mounted
// read-only: the check comes before anything is written.
func TestForeignDataDirectoryNamesTheFix(t *testing.T) {
	if os.Getuid() == 65532 {
		t.Skip("the data directory belongs to uid 65532 already")
	}
	out, err := run("run", "--rm", "--user", "65532:65532", "--network", "none", "--read-only", "--cap-drop", "ALL",
		"-v", env.data+":/data:ro", env.image, "household")
	if err == nil {
		t.Fatalf("household as uid 65532 on a foreign data directory succeeded: %s", out)
	}
	if want := "chown -R 65532:65532 /data"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to name %q", err, want)
	}
}
