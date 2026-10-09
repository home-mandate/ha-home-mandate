// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trustOverrides make Go's TLS trust another CA than the system's. The end-to-end tests
// set SSL_CERT_FILE on the gateway so that it trusts the test CA of this server; nothing
// that ships may set either, or the test CA's trust could reach a release.
var trustOverrides = []string{"SSL_CERT_FILE", "SSL_CERT_DIR"}

// repoRoot is the module root, seen from this package.
const repoRoot = "../.."

func mentionsTrustOverride(content string) string {
	for _, name := range trustOverrides {
		if strings.Contains(content, name) {
			return name
		}
	}
	return ""
}

func TestMentionsTrustOverride(t *testing.T) {
	tests := []struct {
		content, want string
	}{
		{`"-e", "SSL_CERT_FILE=/certs/ca.pem"`, "SSL_CERT_FILE"},
		{"ENV SSL_CERT_DIR=/certs", "SSL_CERT_DIR"},
		{"ENV GOWORK=off", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := mentionsTrustOverride(tt.content); got != tt.want {
			t.Errorf("mentionsTrustOverride(%q) = %q, want %q", tt.content, got, tt.want)
		}
	}
}

// releaseSources are the files the release image and the deployment are built from: the
// Dockerfile, the app's configuration, the compose example and every Go source that is no
// test, outside e2e/ and tools/.
func releaseSources(t *testing.T) []string {
	t.Helper()
	files := []string{"Dockerfile", "app/config.yaml", "docs/deploy/compose.yaml"}
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "e2e" || rel == "tools" || rel == "web" || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNoReleaseSourceSetsTheTrustedCAs(t *testing.T) {
	files := releaseSources(t)
	found := map[string]bool{}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		found[rel] = true
		if name := mentionsTrustOverride(string(data)); name != "" {
			t.Errorf("%s mentions %s: the trust of the test CA must never reach a release", rel, name)
		}
	}
	// The walk saw the binary's sources, so a green result is no empty search.
	for _, want := range []string{"cmd/home-mandate/serve.go", "internal/oauth/cimd.go", "Dockerfile"} {
		if !found[want] {
			t.Errorf("%s was not checked", want)
		}
	}
	// And the end-to-end environment, outside the release, does set it.
	e2e, err := os.ReadFile(filepath.Join(repoRoot, "e2e/env_test.go"))
	if err != nil || mentionsTrustOverride(string(e2e)) != "SSL_CERT_FILE" {
		t.Errorf("e2e/env_test.go no longer sets SSL_CERT_FILE (%v): update this guard", err)
	}
}
