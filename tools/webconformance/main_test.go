// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	mandatespec "github.com/mandate-spec/mandate-spec"
)

// TestWebCopyMatchesMandateSpec keeps the UI's copy of the cases equal to the pinned
// mandate-spec version. Fix with: go run ./tools/webconformance
func TestWebCopyMatchesMandateSpec(t *testing.T) {
	want, err := files(mandatespec.FS())
	if err != nil {
		t.Fatal(err)
	}
	problems, err := diff(filepath.Join("..", "..", "web", "src", "lib", "engine", "conformance"), want)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("web copy: %s (run: go run ./tools/webconformance)", p)
	}
}

func TestFilesReferencesEveryMandateOnce(t *testing.T) {
	spec := fstest.MapFS{
		"conformance/cases-v0.json":         {Data: []byte(`{"cases":[{"mandate":"a.json"},{"mandate":"a.json"},{"mandate_inline":{}}]}`)},
		"a.json":                            {Data: []byte(`{}`)},
		"vocabulary/v0.json":                {Data: []byte(`{}`)},
		"data/forbidden-codepoints-v0.json": {Data: []byte(`{}`)},
	}
	got, err := files(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["a.json"] == nil || got["vocabulary/v0.json"] == nil {
		t.Fatalf("files = %v", got)
	}
}

func TestFilesErrors(t *testing.T) {
	for name, spec := range map[string]fstest.MapFS{
		"no cases":      {},
		"no vocabulary": {"conformance/cases-v0.json": {Data: []byte(`{"cases":[]}`)}},
		"broken json":   {"conformance/cases-v0.json": {Data: []byte(`{`)}},
		"missing file":  {"conformance/cases-v0.json": {Data: []byte(`{"cases":[{"mandate":"nope.json"}]}`)}},
		"bad path":      {"conformance/cases-v0.json": {Data: []byte(`{"cases":[{"mandate":"../x.json"}]}`)}},
	} {
		if _, err := files(spec); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestWriteThenDiff(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	want := map[string][]byte{"a/b.json": []byte("1"), "c.json": []byte("2")}
	if err := write(dir, want); err != nil {
		t.Fatal(err)
	}
	if problems, err := diff(dir, want); err != nil || len(problems) != 0 {
		t.Fatalf("diff after write = %v, %v", problems, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.json"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want["d.json"] = []byte("3")
	problems, err := diff(dir, want)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(problems, []string{"differs c.json", "extra extra.json", "missing d.json"}) {
		t.Fatalf("problems = %v", problems)
	}
	// Writing again removes the extra file.
	if err := write(dir, want); err != nil {
		t.Fatal(err)
	}
	if problems, _ := diff(dir, want); len(problems) != 0 {
		t.Fatalf("after rewrite: %v", problems)
	}
}

func TestDiffMissingDirectory(t *testing.T) {
	problems, err := diff(filepath.Join(t.TempDir(), "absent"), map[string][]byte{"a.json": nil})
	if err != nil || !slices.Equal(problems, []string{"missing a.json"}) {
		t.Fatalf("diff = %v, %v", problems, err)
	}
}

func TestRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conformance")
	if err := run([]string{"-out", dir}, mandatespec.FS()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "conformance", "cases-v0.json")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-unknown"}, mandatespec.FS()); err == nil {
		t.Error("unknown flag accepted")
	}
	if err := run([]string{"-out", dir}, fstest.MapFS{}); err == nil {
		t.Error("missing cases accepted")
	}
}

func TestRunRefusesOtherDirectories(t *testing.T) {
	keep := filepath.Join(t.TempDir(), "keep")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(keep, "important")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{keep, ".", ""} {
		if err := run([]string{"-out", out}, mandatespec.FS()); err == nil {
			t.Errorf("-out %q accepted", out)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("directory was touched: %v", err)
	}
}
