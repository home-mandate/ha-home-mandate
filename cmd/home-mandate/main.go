// SPDX-License-Identifier: AGPL-3.0-or-later

// Command home-mandate is the gateway between AI agents and Home Assistant.
//
// For now it only reports its version and exits cleanly on SIGINT or SIGTERM;
// the services are added package by package (see docs/TASKS-v0.1.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

const (
	exitOK    = 0
	exitUsage = 2
)

func main() {
	os.Exit(runUntilSignal(os.Args[1:], os.Stdout, os.Stderr))
}

// runUntilSignal calls run with a context that is cancelled on SIGINT or SIGTERM.
func runUntilSignal(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, args, stdout, stderr)
}

// run parses args and runs until ctx is cancelled. It returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("home-mandate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", flags.Arg(0))
		flags.Usage()
		return exitUsage
	}

	if *showVersion {
		fmt.Fprintln(stdout, "home-mandate", version)
		return exitOK
	}

	<-ctx.Done()
	return exitOK
}
