// SPDX-License-Identifier: AGPL-3.0-or-later

// Command conformance is Home-Mandate's side of the test interface of mandate-spec
// (SPEC-v0 section 10), so that the test tool mandate-conformance checks Home-Mandate's
// own decision path: the AuthZEN request handling, the conversion of parameters, the
// resource directory and the selection of the mandate in internal/pdp.
//
//	conformance                    process binding on standard input and output
//	conformance -http 127.0.0.1:0  HTTP binding (class pdp)
//
// It is a development tool. The test interface lets its caller choose mandates,
// directory and clock (SPEC-v0 section 10.1), so it is never part of the home-mandate
// binary or the release image; a test checks that. The HTTP binding listens on loopback
// only and requires the bearer token from HM_CONFORMANCE_TOKEN for every request.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	// tokenEnv holds the bearer token of the HTTP binding.
	tokenEnv = "HM_CONFORMANCE_TOKEN"
	// minTokenLength keeps a guessable token out.
	minTokenLength = 32
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("conformance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("http", "", "serve the HTTP binding on this loopback address instead of the process binding")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return exitUsage
	}
	if *addr == "" {
		if err := serveProcess(ctx, stdin, stdout); err != nil {
			fmt.Fprintln(stderr, "conformance:", err)
			return exitFailure
		}
		return exitOK
	}
	token := getenv(tokenEnv)
	if len(token) < minTokenLength {
		fmt.Fprintf(stderr, "conformance: %s must hold a token of at least %d characters\n", tokenEnv, minTokenLength)
		return exitUsage
	}
	err := serveHTTP(ctx, *addr, token, func(listening string) {
		fmt.Fprintf(stdout, "authzen http://%s\ncontrol http://%s%s\n", listening, listening, controlPath)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "conformance:", err)
		return exitFailure
	}
	return exitOK
}
