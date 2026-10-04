// SPDX-License-Identifier: AGPL-3.0-or-later

// Command webconformance copies the evaluation cases of mandate-spec, with the mandates
// they reference, into the UI so that its own evaluation (web/src/lib/engine) is tested
// against exactly the cases of the pinned mandate-spec version. The test of this package
// fails if the copy differs. It is a development tool, not part of the binary.
//
// Usage:
//
//	go run ./tools/webconformance -out web/src/lib/engine/conformance
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	mandatespec "github.com/mandate-spec/mandate-spec"
)

// normativeFiles are the vocabulary and the code points of displayed text
// (SPEC-v0 sections 5 and 3.1 item 8).
var normativeFiles = []string{"vocabulary/v0.json", "data/forbidden-codepoints-v0.json"}

// files returns the case file, every mandate file it references and the normative data
// files, path → content.
func files(spec fs.FS) (map[string][]byte, error) {
	cases, err := fs.ReadFile(spec, mandatespec.CasesPath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Cases []struct {
			Mandate string `json:"mandate"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(cases, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", mandatespec.CasesPath, err)
	}
	out := map[string][]byte{mandatespec.CasesPath: cases}
	// The normative data the web engine reads instead of keeping its own copy in code.
	for _, path := range normativeFiles {
		data, err := fs.ReadFile(spec, path)
		if err != nil {
			return nil, err
		}
		out[path] = data
	}
	for _, c := range doc.Cases {
		if c.Mandate == "" || out[c.Mandate] != nil {
			continue
		}
		if !fs.ValidPath(c.Mandate) {
			return nil, fmt.Errorf("case references invalid path %q", c.Mandate)
		}
		data, err := fs.ReadFile(spec, c.Mandate)
		if err != nil {
			return nil, err
		}
		out[c.Mandate] = data
	}
	return out, nil
}

// write replaces the contents of dir with want.
func write(dir string, want map[string][]byte) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for name, data := range want {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// diff reports files in dir that are missing, extra or different from want.
func diff(dir string, want map[string][]byte) ([]string, error) {
	var problems []string
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		seen[name] = true
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch expected, ok := want[name]; {
		case !ok:
			problems = append(problems, "extra "+name)
		case !bytes.Equal(data, expected):
			problems = append(problems, "differs "+name)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for name := range want {
		if !seen[name] {
			problems = append(problems, "missing "+name)
		}
	}
	slices.Sort(problems)
	return problems, nil
}

// run writes the cases of spec to the directory given by -out in args.
func run(args []string, spec fs.FS) error {
	flags := flag.NewFlagSet("webconformance", flag.ContinueOnError)
	out := flags.String("out", "web/src/lib/engine/conformance", "target directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// The target is replaced as a whole; a wrong -out must not wipe anything else.
	if filepath.Base(filepath.Clean(*out)) != "conformance" {
		return fmt.Errorf("-out %q: the target directory must be named conformance", *out)
	}
	want, err := files(spec)
	if err != nil {
		return err
	}
	return write(*out, want)
}

func main() {
	if err := run(os.Args[1:], mandatespec.FS()); err != nil {
		fmt.Fprintln(os.Stderr, "webconformance:", err)
		os.Exit(1)
	}
}
