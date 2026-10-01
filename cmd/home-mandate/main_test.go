// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	mandatespec "github.com/mandate-spec/mandate-spec"
)

func TestRunVersion(t *testing.T) {
	old := version
	version = "1.2.3-test"
	t.Cleanup(func() { version = old })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--version"}, &stdout, &stderr)

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
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-h"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stderr.String(), "-version") {
		t.Errorf("usage does not mention -version: %q", stderr.String())
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--unlock-everything"}},
		{"positional argument", []string{"serve"}},
		{"flag value for boolean", []string{"--version=maybe"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)

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

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer

	go func() { done <- run(ctx, nil, &stdout, &stderr) }()

	select {
	case code := <-done:
		t.Fatalf("run returned %d before the context was cancelled", code)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the context was cancelled")
	}
}

func TestRunUntilSignalStopsOnSIGTERM(t *testing.T) {
	// Our own handler keeps SIGTERM from terminating the test process.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() { done <- runUntilSignal(nil, &stdout, &stderr) }()

	// runUntilSignal installs its handler asynchronously, so repeat the signal until it returns.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(5 * time.Second)
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

// The gateway is bound to one pinned version of the specification; its conformance cases
// run against the PDP from week 2 on. This guards that the pinned module provides them.
func TestPinnedSpecificationProvidesSchemaAndConformanceCases(t *testing.T) {
	for _, path := range []string{
		mandatespec.MandateSchemaPath,
		mandatespec.CasesPath,
		mandatespec.InvalidCasesPath,
	} {
		data, err := fs.ReadFile(mandatespec.FS(), path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", path)
		}
	}
}
