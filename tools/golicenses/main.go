// SPDX-License-Identifier: AGPL-3.0-or-later

// Command golicenses appends the licenses of the Go code in the binary to the UI's
// licenses.txt (decision B10, AGPL section 13 offer in the UI's "About"): the Go
// standard library and every module that cmd/home-mandate links, found with go list,
// with the texts of their LICENSE, LICENCE, COPYING and NOTICE files. A module without
// a license text stops the build: its notice must ship. The only exception is a module
// named with -pending and its SPDX license, for a module that declares its license but
// does not ship the file yet (the specification until its release); the flag goes once it does.
//
//	go run ./tools/golicenses -file internal/webui/dist/licenses.txt \
//	  -pending github.com/home-mandate/spec=Apache-2.0
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// module is a linked module with its source directory.
type module struct {
	Path, Version, Dir string
}

var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice)([-.].*)?$`)

const rule = "========================================================================"

// pending are modules that declare a license but ship no file yet: module → SPDX.
type pending map[string]string

func (p pending) String() string { return fmt.Sprint(map[string]string(p)) }

func (p pending) Set(v string) error {
	path, spdx, ok := strings.Cut(v, "=")
	if !ok || path == "" || !spdxPattern.MatchString(spdx) {
		return fmt.Errorf("want module=SPDX, got %q", v)
	}
	p[path] = spdx
	return nil
}

var spdxPattern = regexp.MustCompile(`^[A-Za-z0-9.+-]{2,64}$`)

func main() {
	file := flag.String("file", "internal/webui/dist/licenses.txt", "licenses.txt to append to")
	declared := pending{}
	flag.Var(declared, "pending", "module=SPDX of a module that ships no license file yet (repeatable)")
	flag.Parse()
	if err := run(*file, declared, goList, goRoot); err != nil {
		fmt.Fprintln(os.Stderr, "golicenses:", err)
		os.Exit(1)
	}
}

func run(file string, declared pending, list func() (string, error), root func() (string, error)) error {
	out, err := list()
	if err != nil {
		return err
	}
	modules, err := parseList(out)
	if err != nil {
		return err
	}
	goroot, err := root()
	if err != nil {
		return err
	}
	text, err := licenses(modules, goroot, declared)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("\n" + rule + "\n\n" + text); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// goList lists the modules of the packages cmd/home-mandate links, one per line.
func goList() (string, error) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}",
		"./cmd/home-mandate").Output()
	if err != nil {
		return "", fmt.Errorf("go list: %w", err)
	}
	return string(out), nil
}

func goRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("go env: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// parseList reads the output of goList: distinct modules, sorted by path.
func parseList(out string) ([]module, error) {
	seen := map[string]module{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
			return nil, fmt.Errorf("unexpected line %q", line)
		}
		seen[parts[0]] = module{Path: parts[0], Version: parts[1], Dir: parts[2]}
	}
	list := make([]module, 0, len(seen))
	for _, m := range seen {
		list = append(list, m)
	}
	slices.SortFunc(list, func(a, b module) int { return strings.Compare(a.Path, b.Path) })
	return list, nil
}

// licenses writes one block per module after the Go standard library's, in the same
// form as the UI's own list (web/scripts/licenses.ts).
func licenses(modules []module, goroot string, declared pending) (string, error) {
	std, err := texts(goroot)
	if err != nil || std == "" {
		return "", fmt.Errorf("no license text of the Go standard library in %s: %w", goroot, errors.Join(err, fs.ErrNotExist))
	}
	blocks := []string{"Home-Mandate includes the following Go code.", "Go standard library\n\n" + std}
	var missing []string
	for _, m := range modules {
		text, err := texts(m.Dir)
		if err != nil {
			return "", err
		}
		if text == "" {
			spdx, ok := declared[m.Path]
			if !ok {
				missing = append(missing, m.Path)
				continue
			}
			text = "License: " + spdx + " (declared by the module; it ships no license file yet)."
		}
		blocks = append(blocks, strings.TrimSpace(m.Path+" "+m.Version)+"\n\n"+text)
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("no license text for %s", strings.Join(missing, ", "))
	}
	return strings.Join(blocks, "\n\n"+rule+"\n\n") + "\n", nil
}

// texts returns the license and notice texts in the top directory of dir.
func texts(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !licenseFile.MatchString(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", err
		}
		out = append(out, strings.TrimSpace(string(data)))
	}
	return strings.Join(out, "\n\n"), nil
}
