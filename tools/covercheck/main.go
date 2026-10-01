// SPDX-License-Identifier: AGPL-3.0-or-later

// Command covercheck enforces per-package statement coverage thresholds on a Go cover
// profile (docs/TESTING.md section 2). It is a development tool, not part of the binary.
//
// Usage:
//
//	covercheck -profile cover.out -default 85 -min internal/pdp=95 -min internal/api=95
//
// A -min key matches the package whose import path ends with "/key".
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

const (
	exitOK    = 0
	exitBelow = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type counts struct {
	covered, total int
}

func (c counts) percent() float64 {
	return 100 * float64(c.covered) / float64(c.total)
}

// thresholds collects repeated -min key=percent flags.
type thresholds map[string]float64

func (t thresholds) String() string { return fmt.Sprint(map[string]float64(t)) }

func (t thresholds) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("want package=percent, got %q", v)
	}
	p, err := parsePercent(value)
	if err != nil {
		return err
	}
	t[key] = p
	return nil
}

func parsePercent(s string) (float64, error) {
	p, err := strconv.ParseFloat(s, 64)
	if err != nil || p < 0 || p > 100 {
		return 0, fmt.Errorf("want a percentage between 0 and 100, got %q", s)
	}
	return p, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("covercheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "cover profile written by go test -coverprofile")
	defaultMin := flags.String("default", "85", "threshold for packages without -min")
	mins := thresholds{}
	flags.Var(mins, "min", "package=percent, repeatable")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	fallback, err := parsePercent(*defaultMin)
	if err != nil || *profilePath == "" {
		fmt.Fprintln(stderr, "covercheck: -profile and a valid -default are required")
		return exitUsage
	}

	f, err := os.Open(*profilePath)
	if err != nil {
		fmt.Fprintln(stderr, "covercheck:", err)
		return exitUsage
	}
	defer f.Close()
	pkgs, err := parseProfile(f)
	if err != nil {
		fmt.Fprintln(stderr, "covercheck:", err)
		return exitUsage
	}
	return report(pkgs, mins, fallback, stdout, stderr)
}

func report(pkgs map[string]counts, mins thresholds, fallback float64, stdout, stderr io.Writer) int {
	code := exitOK
	used := map[string]bool{}
	names := slices.Sorted(func(yield func(string) bool) {
		for name := range pkgs {
			if !yield(name) {
				return
			}
		}
	})
	for _, pkg := range names {
		want := fallback
		for key, p := range mins {
			if pkg == key || strings.HasSuffix(pkg, "/"+key) {
				want, used[key] = p, true
			}
		}
		got := pkgs[pkg].percent()
		if got < want {
			fmt.Fprintf(stdout, "FAIL %5.1f%% < %g%%  %s\n", got, want, pkg)
			code = exitBelow
		} else {
			fmt.Fprintf(stdout, "ok   %5.1f%% ≥ %g%%  %s\n", got, want, pkg)
		}
	}
	for key := range mins {
		if !used[key] {
			// A threshold for a package that does not exist (yet) or has no tests.
			fmt.Fprintf(stderr, "covercheck: no coverage for %s\n", key)
			code = exitBelow
		}
	}
	return code
}

// parseProfile sums statements per package. Blocks listed more than once count as
// covered if any listing has a non-zero count.
func parseProfile(r io.Reader) (map[string]counts, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "mode: ") {
		return nil, errors.New("profile does not start with a mode line")
	}
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]block{}
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed line %q", line)
		}
		stmts, err1 := strconv.Atoi(fields[1])
		hits, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || stmts < 0 || hits < 0 || !strings.Contains(fields[0], ".go:") {
			return nil, fmt.Errorf("malformed line %q", line)
		}
		b := blocks[fields[0]]
		blocks[fields[0]] = block{stmts: stmts, covered: b.covered || hits > 0}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, errors.New("profile has no blocks")
	}
	pkgs := map[string]counts{}
	for pos, b := range blocks {
		file, _, _ := strings.Cut(pos, ":")
		pkg := path.Dir(file)
		c := pkgs[pkg]
		c.total += b.stmts
		if b.covered {
			c.covered += b.stmts
		}
		pkgs[pkg] = c
	}
	return pkgs, nil
}
