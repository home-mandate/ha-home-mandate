// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// bareEnv has no configuration at all.
func bareEnv() (env, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return env{
		getenv:   func(string) string { return "" },
		readFile: os.ReadFile,
		stdin:    strings.NewReader(""),
		stdout:   &stdout,
		stderr:   &stderr,
	}, &stdout, &stderr
}

func TestRunVersion(t *testing.T) {
	old := version
	version = "1.2.3-test"
	t.Cleanup(func() { version = old })

	e, stdout, stderr := bareEnv()
	code := run(context.Background(), []string{"--version"}, e)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if got, want := stdout.String(), "home-mandate 1.2.3-test\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHelpExitsCleanly(t *testing.T) {
	e, _, stderr := bareEnv()
	code := run(context.Background(), []string{"-h"}, e)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stderr.String(), "-version") || !strings.Contains(stderr.String(), "mandate import") {
		t.Errorf("usage is incomplete: %q", stderr.String())
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--unlock-everything"}},
		{"unknown command", []string{"frobnicate"}},
		{"flag value for boolean", []string{"--version=maybe"}},
		{"argument for serve", []string{"serve", "now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, stdout, stderr := bareEnv()
			code := run(context.Background(), tt.args, e)

			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Error("stderr is empty, want an error message")
			}
		})
	}
}

func TestServeNeedsAValidConfiguration(t *testing.T) {
	e, _, stderr := bareEnv()
	if code := run(context.Background(), nil, e); code != exitFailure || stderr.Len() == 0 {
		t.Errorf("exit code = %d, stderr %q", code, stderr.String())
	}
}

func TestServeStopsWhenContextIsCancelled(t *testing.T) {
	c := newCLI(t)
	e, _, stderr := c.env("")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)

	go func() { done <- run(ctx, []string{"serve"}, e) }()

	select {
	case code := <-done:
		t.Fatalf("run returned %d before the context was cancelled: %s", code, stderr)
	case <-time.After(200 * time.Millisecond):
	}

	cancel()

	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the context was cancelled")
	}
}

func TestServeRefusesABrokenAuditLog(t *testing.T) {
	c := newCLI(t)
	c.mustRun("", "agent", "add", "--name", "A")
	c.mustRun("", "agent", "add", "--name", "B")
	if err := tamper(c.envVars["HM_DATA_DIR"] + "/home-mandate.db"); err != nil {
		t.Fatal(err)
	}
	e, _, stderr := c.env("")
	if code := run(context.Background(), []string{"serve"}, e); code != exitFailure || !strings.Contains(stderr.String(), "audit log") {
		t.Errorf("exit code = %d, stderr %q", code, stderr.String())
	}
}

func TestRunUntilSignalStopsOnSIGTERM(t *testing.T) {
	// Our own handler keeps SIGTERM from terminating the test process.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	c := newCLI(t)
	e, _, _ := c.env("")
	done := make(chan int, 1)
	go func() { done <- runUntilSignal([]string{"serve"}, e) }()

	// runUntilSignal installs its handler asynchronously, so repeat the signal until it returns.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case code := <-done:
			if code != exitOK {
				t.Errorf("exit code = %d, want %d", code, exitOK)
			}
			return
		case <-ticker.C:
			if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
				t.Fatalf("send SIGTERM: %v", err)
			}
		case <-timeout:
			t.Fatal("runUntilSignal did not return after SIGTERM")
		}
	}
}
