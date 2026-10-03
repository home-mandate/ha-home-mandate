// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunAppendsTheLicenses(t *testing.T) {
	root := t.TempDir()
	goroot, a, b := filepath.Join(root, "go"), filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, goroot, "LICENSE", "BSD-3-Clause Go")
	write(t, a, "LICENSE.md", "MIT A")
	write(t, a, "NOTICE", "Notice A")
	write(t, a, "README", "not a license")
	write(t, b, "COPYING", "Apache B")
	file := filepath.Join(root, "licenses.txt")
	write(t, root, "licenses.txt", "Home-Mandate includes the following third-party code.\n")
	list := func() (string, error) {
		return "example.org/b\tv1.0.0\t" + b + "\nexample.org/a\tv2.1.0\t" + a + "\nexample.org/a\tv2.1.0\t" + a + "\n\n", nil
	}
	if err := run(file, pending{}, list, func() (string, error) { return goroot, nil }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	out := string(data)
	for _, want := range []string{"third-party code.\n\n" + rule, "Go standard library\n\nBSD-3-Clause Go",
		"example.org/a v2.1.0\n\nMIT A\n\nNotice A", "example.org/b v1.0.0\n\nApache B"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Index(out, "example.org/a") > strings.Index(out, "example.org/b") || strings.Count(out, "example.org/a") != 1 ||
		strings.Contains(out, "not a license") {
		t.Errorf("order, duplicates or README:\n%s", out)
	}
}

func TestRunRefuses(t *testing.T) {
	root := t.TempDir()
	goroot, a := filepath.Join(root, "go"), filepath.Join(root, "a")
	write(t, goroot, "LICENSE", "Go")
	write(t, a, "README", "no license")
	file := filepath.Join(root, "licenses.txt")
	write(t, root, "licenses.txt", "x")
	ok := func() (string, error) { return goroot, nil }
	for name, tc := range map[string]struct {
		list func() (string, error)
		root func() (string, error)
		file string
	}{
		"module without license": {func() (string, error) { return "example.org/a\tv1\t" + a, nil }, ok, file},
		"go list failed":         {func() (string, error) { return "", errors.New("boom") }, ok, file},
		"malformed list":         {func() (string, error) { return "just one field", nil }, ok, file},
		"module dir missing":     {func() (string, error) { return "example.org/x\tv1\t" + filepath.Join(root, "none"), nil }, ok, file},
		"no Go license":          {func() (string, error) { return "", nil }, func() (string, error) { return a, nil }, file},
		"go env failed":          {func() (string, error) { return "", nil }, func() (string, error) { return "", errors.New("boom") }, file},
		"no licenses.txt":        {func() (string, error) { return "", nil }, ok, filepath.Join(root, "missing.txt")},
	} {
		if err := run(tc.file, pending{}, tc.list, tc.root); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The real module list of cmd/home-mandate parses and every module has a license.
func TestTheBinaryHasLicensesForEverything(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	t.Chdir("../..")
	out, err := goList()
	if err != nil {
		t.Fatal(err)
	}
	modules, err := parseList(out)
	if err != nil || len(modules) < 3 {
		t.Fatalf("modules = %v, %v", modules, err)
	}
	goroot, err := goRoot()
	if err != nil {
		t.Fatal(err)
	}
	// mandate-spec ships its license files with its release (tasks); until then declared.
	text, err := licenses(modules, goroot, pending{"github.com/mandate-spec/mandate-spec": "Apache-2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "github.com/mandate-spec/mandate-spec") || !strings.Contains(text, "modernc.org/sqlite") {
		t.Errorf("licenses miss modules:\n%.500s", text)
	}
}

func TestPendingModules(t *testing.T) {
	root := t.TempDir()
	goroot, a := filepath.Join(root, "go"), filepath.Join(root, "a")
	write(t, goroot, "LICENSE", "Go")
	write(t, a, "README", "no license")
	text, err := licenses([]module{{Path: "example.org/a", Version: "v1", Dir: a}}, goroot, pending{"example.org/a": "Apache-2.0"})
	if err != nil || !strings.Contains(text, "example.org/a v1\n\nLicense: Apache-2.0 (declared") {
		t.Errorf("text = %q, %v", text, err)
	}
	p := pending{}
	for _, bad := range []string{"no-equals", "=Apache-2.0", "x=", "x=a b", "x=" + strings.Repeat("a", 65)} {
		if err := p.Set(bad); err == nil {
			t.Errorf("Set(%q) accepted", bad)
		}
	}
	if err := p.Set("example.org/a=MIT"); err != nil || p.String() != "map[example.org/a:MIT]" {
		t.Errorf("Set = %v, %s", err, p.String())
	}
}
