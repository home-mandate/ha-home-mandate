// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/config"
)

func TestPermissionHintNamesTheUserAndTheFix(t *testing.T) {
	cause := fmt.Errorf("audit checkpoint key /data/audit-checkpoint.key: %w",
		&fs.PathError{Op: "open", Path: "/data/audit-checkpoint.key", Err: fs.ErrPermission})

	err := permissionHint(cause, 65532, 65532)

	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error = %v, want it to keep fs.ErrPermission", err)
	}
	for _, want := range []string{"/data/audit-checkpoint.key", "runs as uid 65532", "chown 65532:65532", "Running in container mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestPermissionHintLeavesOtherErrorsAlone(t *testing.T) {
	for _, cause := range []error{nil, fs.ErrNotExist, errors.New("not an Ed25519 seed in base64")} {
		if err := permissionHint(cause, 65532, 65532); err != cause {
			t.Errorf("permissionHint(%v) = %v, want it unchanged", cause, err)
		}
	}
}

// A token file the container user may not read stops the start with the fix.
func TestServeExplainsAnUnreadableTokenFile(t *testing.T) {
	e, _, stderr := bareEnv()
	vars := map[string]string{
		"HM_HA_URL":        "ws://127.0.0.1:8123/api/websocket",
		"HM_HA_TOKEN_FILE": "/run/secrets/ha-token",
		"HM_DATA_DIR":      t.TempDir(),
	}
	e.getenv = func(k string) string { return vars[k] }
	e.readFile = func(name string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	// The file itself is private, only reading it fails (it belongs to another user).
	private := filepath.Join(t.TempDir(), "ha-token")
	if err := os.WriteFile(private, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.stat = func(string) (fs.FileInfo, error) { return os.Stat(private) }

	code := run(context.Background(), nil, e)

	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	for _, want := range []string{"/run/secrets/ha-token", fmt.Sprintf("runs as uid %d", os.Getuid()), "chown"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestRootWarningOnlyInContainerMode(t *testing.T) {
	for _, tc := range []struct {
		mode config.Mode
		uid  int
		want bool
	}{
		{config.ModeContainer, 0, true},
		{config.ModeContainer, 65532, false},
		{config.ModeApp, 0, false}, // the Supervisor runs apps as root
	} {
		if got := rootWarning(tc.mode, tc.uid) != ""; got != tc.want {
			t.Errorf("rootWarning(%v, %d) warns = %v, want %v", tc.mode, tc.uid, got, tc.want)
		}
	}
}
