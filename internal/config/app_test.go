// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// yamlKeys returns the keys directly below a top-level block of a simple YAML file, e.g.
// the options of "schema:". Enough for app/config.yaml and its translations.
func yamlKeys(t *testing.T, file, block string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	in := false
	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\n")
		switch {
		case line == block+":":
			in = true
		case in && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && !strings.HasPrefix(strings.TrimSpace(line), "#"):
			keys = append(keys, strings.TrimSpace(strings.SplitN(line, ":", 2)[0]))
		case in && line != "" && !strings.HasPrefix(line, " "):
			in = false
		}
	}
	slices.Sort(keys)
	return keys
}

// The Supervisor writes the app's options to /data/options.json, which loadApp reads
// strictly: an option of app/config.yaml the code does not know stops the app on Home
// Assistant OS. Schema, defaults, code and both translations name the same options.
func TestAppOptionsMatchTheCode(t *testing.T) {
	var code []string
	typ := reflect.TypeFor[appOptionsFile]()
	for i := range typ.NumField() {
		code = append(code, typ.Field(i).Tag.Get("json"))
	}
	slices.Sort(code)
	schema := yamlKeys(t, "../../app/config.yaml", "schema")
	if !slices.Equal(schema, code) {
		t.Errorf("app/config.yaml schema %v, code %v", schema, code)
	}
	for _, opt := range yamlKeys(t, "../../app/config.yaml", "options") {
		if !slices.Contains(code, opt) {
			t.Errorf("default for unknown option %q", opt)
		}
	}
	for _, lang := range []string{"en", "de"} {
		if got := yamlKeys(t, "../../app/translations/"+lang+".yaml", "configuration"); !slices.Equal(got, schema) {
			t.Errorf("translations/%s.yaml explains %v, schema %v", lang, got, schema)
		}
	}
}

// The Supervisor loads app/apparmor.txt only with exactly one top-level profile, and a
// profile in complain mode would only log what it should refuse.
func TestAppArmorProfileEnforces(t *testing.T) {
	data, err := os.ReadFile("../../app/apparmor.txt")
	if err != nil {
		t.Fatal(err)
	}
	profiles := 0
	for line := range strings.Lines(string(data)) {
		if strings.HasPrefix(line, "profile ") {
			profiles++
			if strings.Contains(line, "complain") {
				t.Errorf("profile in complain mode: %s", line)
			}
		}
	}
	if profiles != 1 {
		t.Errorf("%d top-level profiles, the Supervisor needs exactly one", profiles)
	}
}
