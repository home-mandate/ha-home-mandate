// SPDX-License-Identifier: AGPL-3.0-or-later

// Command home-mandate is the gateway between AI agents and Home Assistant.
//
//	home-mandate [serve]            run the gateway (default)
//	home-mandate household          print the household principal
//	home-mandate agent …            list and revoke agents (local administration only)
//	home-mandate emergency-stop …   block all agents at once, or release the stop
//	home-mandate approver …         who receives approval requests, on which phone
//	home-mandate mandate …          manage mandates (local administration only)
//	home-mandate audit verify|export|key
//
// The administration commands work on the local database only; they are never
// reachable over the network.
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

// version and commit are set at build time via -ldflags "-X main.version=… -X main.commit=…".
var (
	version = "dev"
	commit  = "unknown"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `Usage:
  home-mandate [-version] [serve]
  home-mandate household
  home-mandate agent list | revoke CLIENT_ID
  home-mandate emergency-stop on | off | status
  home-mandate approver add USER_ID NOTIFY_SERVICE [de|en] | list | remove USER_ID
  home-mandate mandate import FILE|- | list | revoke ID | check
  home-mandate mandate template import NAME FILE|- | list | remove NAME
  home-mandate audit verify | export | key
`

// env is the process environment, replaceable in tests.
type env struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
}

func main() {
	os.Exit(runUntilSignal(os.Args[1:], env{
		getenv: os.Getenv, readFile: os.ReadFile, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
	}))
}

// runUntilSignal calls run with a context that is cancelled on SIGINT or SIGTERM.
func runUntilSignal(args []string, e env) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, args, e)
}

// run parses args and runs the command. It returns the process exit code.
func run(ctx context.Context, args []string, e env) int {
	flags := flag.NewFlagSet("home-mandate", flag.ContinueOnError)
	flags.SetOutput(e.stderr)
	flags.Usage = func() {
		fmt.Fprint(e.stderr, usage)
		flags.PrintDefaults()
	}
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *showVersion {
		fmt.Fprintln(e.stdout, "home-mandate", version)
		return exitOK
	}

	rest := flags.Args()
	command := "serve"
	if len(rest) > 0 {
		command, rest = rest[0], rest[1:]
	}
	switch command {
	case "serve":
		if len(rest) > 0 {
			return usageError(e, "serve takes no arguments")
		}
		return serve(ctx, e)
	case "household":
		if len(rest) > 0 {
			return usageError(e, "household takes no arguments")
		}
		return withState(ctx, e, func(s *state) error {
			fmt.Fprintln(e.stdout, s.household)
			return nil
		})
	case "agent":
		return agentCommand(ctx, e, rest)
	case "emergency-stop":
		return emergencyStopCommand(ctx, e, rest)
	case "approver":
		return approverCommand(ctx, e, rest)
	case "mandate":
		return mandateCommand(ctx, e, rest)
	case "audit":
		return auditCommand(ctx, e, rest)
	default:
		return usageError(e, fmt.Sprintf("unknown command %q", command))
	}
}

func usageError(e env, msg string) int {
	fmt.Fprintf(e.stderr, "home-mandate: %s\n%s", msg, usage)
	return exitUsage
}
