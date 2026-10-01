// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const profile = `mode: atomic
example.org/m/internal/pdp/pdp.go:10.2,12.3 19 1
example.org/m/internal/pdp/pdp.go:14.2,15.3 1 0
example.org/m/internal/ha/client.go:10.2,12.3 17 3
example.org/m/internal/ha/client.go:14.2,15.3 3 0
example.org/m/cmd/app/main.go:5.2,6.3 2 0
`

func writeProfile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseProfile(t *testing.T) {
	got, err := parseProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]counts{
		"example.org/m/internal/pdp": {covered: 19, total: 20},
		"example.org/m/internal/ha":  {covered: 17, total: 20},
		"example.org/m/cmd/app":      {covered: 0, total: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	for pkg, c := range want {
		if got[pkg] != c {
			t.Errorf("%s = %+v, want %+v", pkg, got[pkg], c)
		}
	}
}

func TestParseProfileMergesDuplicateBlocks(t *testing.T) {
	// The same block can appear several times (one per test binary); covered if any run hit it.
	got, err := parseProfile(strings.NewReader("mode: set\nm/p/a.go:1.1,2.2 4 0\nm/p/a.go:1.1,2.2 4 1\nm/p/a.go:3.1,4.2 1 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c := got["m/p"]; c != (counts{covered: 4, total: 5}) {
		t.Errorf("counts = %+v, want {4 5}", c)
	}
}

func TestParseProfileRejectsMalformedInput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":            "",
		"no mode":          "m/p/a.go:1.1,2.2 1 1\n",
		"too few fields":   "mode: set\nm/p/a.go:1.1,2.2 1\n",
		"bad count":        "mode: set\nm/p/a.go:1.1,2.2 1 x\n",
		"bad statements":   "mode: set\nm/p/a.go:1.1,2.2 -1 1\n",
		"no position":      "mode: set\nm/p/a.go 1 1\n",
		"no blocks at all": "mode: set\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProfile(strings.NewReader(in)); err == nil {
				t.Error("parseProfile succeeded, want error")
			}
		})
	}
}

func TestRunEnforcesPerPackageThresholds(t *testing.T) {
	path := writeProfile(t, profile)
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  []string
	}{
		{
			name:     "all above",
			args:     []string{"-profile", path, "-default", "0", "-min", "internal/pdp=95"},
			wantCode: 0,
			wantOut:  []string{"ok", "95.0%", "example.org/m/internal/pdp"},
		},
		{
			name:     "strict package below",
			args:     []string{"-profile", path, "-default", "0", "-min", "internal/pdp=96"},
			wantCode: 1,
			wantOut:  []string{"FAIL", "95.0% < 96%", "example.org/m/internal/pdp"},
		},
		{
			name:     "default applies to other packages",
			args:     []string{"-profile", path, "-default", "86", "-min", "internal/pdp=95", "-min", "cmd/app=0"},
			wantCode: 1,
			wantOut:  []string{"FAIL  85.0% < 86%  example.org/m/internal/ha", "ok"},
		},
		{
			name:     "override must match a package",
			args:     []string{"-profile", path, "-default", "0", "-min", "internal/missing=95"},
			wantCode: 1,
			wantOut:  []string{"internal/missing"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			out := stdout.String() + stderr.String()
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\n%s", code, tt.wantCode, out)
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out, w) {
					t.Errorf("output does not contain %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestRunDefaultThresholdFailsUncoveredPackage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-profile", writeProfile(t, profile), "-default", "85", "-min", "internal/pdp=95"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "0.0% < 85%") {
		t.Errorf("exit code = %d, output:\n%s", code, stdout.String())
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	path := writeProfile(t, profile)
	for name, args := range map[string][]string{
		"missing profile":   {"-default", "85"},
		"unreadable":        {"-profile", filepath.Join(t.TempDir(), "nope")},
		"bad min":           {"-profile", path, "-min", "internal/pdp"},
		"bad min value":     {"-profile", path, "-min", "internal/pdp=x"},
		"min above 100":     {"-profile", path, "-min", "internal/pdp=101"},
		"default above 100": {"-profile", path, "-default", "150"},
		"unknown flag":      {"-profile", path, "-x"},
		"malformed profile": {"-profile", writeProfile(t, "garbage")},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
		})
	}
}
