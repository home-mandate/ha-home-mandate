// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	mandatespec "github.com/mandate-spec/mandate-spec"
)

func example(t *testing.T) string {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// exchange sends requests over the process binding and returns the responses.
func exchange(t *testing.T, requests ...any) []map[string]any {
	t.Helper()
	var in bytes.Buffer
	for _, r := range requests {
		if s, ok := r.(string); ok {
			in.WriteString(s + "\n")
			continue
		}
		line, _ := json.Marshal(r)
		in.Write(append(line, '\n'))
	}
	var out bytes.Buffer
	if code := run(context.Background(), nil, &in, &out, &bytes.Buffer{}, func(string) string { return "" }); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		responses = append(responses, r)
	}
	return responses
}

func TestProcessBindingAnswersEveryOperation(t *testing.T) {
	m := example(t)
	keys, err := fs.ReadFile(mandatespec.FS(), "conformance/keys/test-keys.json")
	if err != nil {
		t.Fatal(err)
	}
	req := map[string]any{"resource": map[string]any{"entity_id": "lock.front", "category": "lock", "area": "hallway"},
		"action": "unlock", "time": "2026-10-06T12:00:00Z", "timezone": "Europe/Berlin"}
	revoked := map[string]any{"resource": req["resource"], "action": "unlock", "time": "2026-10-06T12:00:00Z", "revoked": true}
	badTime := map[string]any{"resource": req["resource"], "action": "unlock", "time": "yesterday"}
	got := exchange(t,
		map[string]any{"op": "capabilities"},
		map[string]any{"op": "validate", "mandate": m},
		map[string]any{"op": "validate", "mandate": "{}"},
		map[string]any{"op": "evaluate", "mandate": m, "request": req},
		map[string]any{"op": "evaluate", "mandate": m, "request": revoked},
		map[string]any{"op": "evaluate", "mandate": m, "request": badTime},
		map[string]any{"op": "evaluate", "mandate": "{}", "request": badTime},
		map[string]any{"op": "select", "mandates": []any{map[string]any{"mandate": m}},
			"subject": map[string]any{"client_id": "hm-client:voice-7c21e9a4", "principal": "household:hm-7f3a"}, "request": req},
		map[string]any{"op": "select", "mandates": []any{map[string]any{"mandate": m, "revoked": true}},
			"subject": map[string]any{"client_id": "hm-client:voice-7c21e9a4", "principal": "household:hm-7f3a"}, "request": req},
		map[string]any{"op": "succession", "stored": m, "offered": m},
		map[string]any{"op": "verify_audit", "entries": []string{}},
		map[string]any{"op": "entry_digest", "entry": "{}"},
		map[string]any{"op": "verify_signed", "jws": "x"},
		"not json",
		map[string]any{"op": "evaluate"},
		map[string]any{"op": "select"},
		map[string]any{"op": "validate"},
		map[string]any{"op": "succession", "stored": "{}", "offered": m},
		map[string]any{"op": "succession"},
		map[string]any{"op": "entry_digest"},
		map[string]any{"op": "verify_audit", "jsonl": ""},
		map[string]any{"op": "verify_audit", "jsonl": "", "keys": json.RawMessage(keys)},
		map[string]any{"op": "verify_audit", "entries": []string{}, "keys": json.RawMessage(keys)},
		map[string]any{"op": "verify_audit", "keys": "nonsense"},
		map[string]any{"op": "verify_audit", "entries": []string{"{}"}},
		map[string]any{"op": "select", "mandates": []any{}, "subject": map[string]any{"client_id": "a:b", "principal": "household:x"}, "request": badTime},
		map[string]any{"op": "evaluate", "mandate": m, "request": map[string]any{"resource": req["resource"], "action": "unlock",
			"time": "2026-10-06T12:00:00Z", "revoked": true, "parameters": map[string]any{"position": 1.5}}},
		map[string]any{"op": "evaluate", "mandate": m, "request": map[string]any{"resource": req["resource"], "action": "unlock",
			"time": "2026-10-06T12:00:00Z", "revoked": true, "parameters": map[string]any{"position": 2.0, "volume": 3}}},
	)
	checks := []struct {
		name string
		want map[string]any
	}{
		{"capabilities", map[string]any{"name": "Home-Mandate"}},
		{"valid mandate", map[string]any{"valid": true}},
		{"invalid mandate", map[string]any{"valid": false}},
		{"evaluate through the PDP", map[string]any{"decision": "ask", "reason": "rule", "rule_id": "r-locks"}},
		{"revoked mandate", map[string]any{"decision": "deny", "reason": "revoked"}},
		{"time that is no RFC 3339", map[string]any{"decision": "deny", "reason": "invalid_request"}},
		{"invalid mandate first", map[string]any{"decision": "deny", "reason": "invalid_mandate"}},
		{"select", map[string]any{"decision": "ask", "selected": "m-voice-assistant"}},
		{"only a revoked mandate", map[string]any{"decision": "deny", "reason": "no_mandate"}},
		{"same version is no successor of a mandate with version", map[string]any{"accept": true}},
		{"empty log", map[string]any{"valid": true}},
		{"entry digest", map[string]any{"valid": true}},
		{"signatures are not offered", map[string]any{"error": "unsupported"}},
		{"malformed line", map[string]any{"error": "malformed request"}},
		{"missing members", map[string]any{"error": "missing mandate and request"}},
		{"select without subject", map[string]any{"error": "missing subject and request"}},
		{"validate without mandate", map[string]any{"error": "missing mandate"}},
		{"succession on an invalid stored mandate", map[string]any{"error": "stored mandate is invalid"}},
		{"succession without mandates", map[string]any{"error": "missing stored and offered"}},
		{"entry digest without entry", map[string]any{"error": "missing entry"}},
		{"empty export", map[string]any{"valid": true}},
		{"empty anchored export", map[string]any{"valid": true}},
		{"empty anchored entries", map[string]any{"valid": true}},
		{"keys that are no JWK Set", map[string]any{}},
		{"broken log", map[string]any{"valid": false}},
		{"select at a time that is no RFC 3339", map[string]any{"reason": "invalid_request"}},
		{"revoked mandate with a fraction", map[string]any{"reason": "invalid_request"}},
		{"revoked mandate with integer parameters", map[string]any{"reason": "revoked"}},
	}
	if len(got) != len(checks) {
		t.Fatalf("%d responses for %d requests", len(got), len(checks))
	}
	for i, c := range checks {
		for key, want := range c.want {
			if got[i][key] != want {
				t.Errorf("%s: %s = %v, want %v (%v)", c.name, key, got[i][key], want, got[i])
			}
		}
	}
}

func TestHTTPBindingNeedsTheToken(t *testing.T) {
	token := strings.Repeat("t", minTokenLength)
	srv := httptest.NewServer(&binding{token: token})
	defer srv.Close()
	state := `{"mandates":[{"mandate":` + mustJSON(example(t)) + `}],"directory":[{"entity_id":"lock.front","category":"lock"}],"time":"2026-10-06T12:00:00Z"}`
	evaluation := `{"subject":{"type":"agent","id":"hm-client:voice-7c21e9a4","properties":{"principal":"household:hm-7f3a"}},"action":{"name":"unlock"},"resource":{"id":"lock.front"}}`
	for _, tc := range []struct {
		name, method, path, auth, body string
		want                            int
	}{
		{"no token", http.MethodPut, controlPath, "", state, http.StatusUnauthorized},
		{"wrong token", http.MethodPut, controlPath, "Bearer " + strings.Repeat("x", minTokenLength), state, http.StatusUnauthorized},
		{"invalid state", http.MethodPut, controlPath, "Bearer " + token, "[]", http.StatusBadRequest},
		{"invalid time", http.MethodPut, controlPath, "Bearer " + token, `{"time":"now"}`, http.StatusBadRequest},
		{"state", http.MethodPut, controlPath, "Bearer " + token, state, http.StatusNoContent},
		{"evaluation without token", http.MethodPost, "/access/v1/evaluation", "", evaluation, http.StatusUnauthorized},
		{"evaluation", http.MethodPost, "/access/v1/evaluation", "Bearer " + token, evaluation, http.StatusOK},
		{"other path", http.MethodGet, "/", "Bearer " + token, "", http.StatusNotFound},
		{"too large", http.MethodPut, controlPath, "Bearer " + token, strings.Repeat(" ", maxBodyBytes+1), http.StatusRequestEntityTooLarge},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Context struct {
				Outcome string `json:"outcome"`
			} `json:"context"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
		if tc.name == "evaluation" && out.Context.Outcome != "ask" {
			t.Errorf("evaluation: outcome %q, want ask", out.Context.Outcome)
		}
	}
}

func mustJSON(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func TestHTTPBindingRefusesWeakTokensAndOtherAddresses(t *testing.T) {
	var stderr bytes.Buffer
	weak := func(string) string { return "short" }
	if code := run(context.Background(), []string{"-http", "127.0.0.1:0"}, nil, &bytes.Buffer{}, &stderr, weak); code != exitUsage {
		t.Errorf("weak token: exit %d", code)
	}
	strong := func(string) string { return strings.Repeat("t", minTokenLength) }
	if code := run(context.Background(), []string{"-http", "0.0.0.0:0"}, nil, &bytes.Buffer{}, &stderr, strong); code != exitFailure {
		t.Errorf("all interfaces: exit %d", code)
	}
	if code := run(context.Background(), []string{"extra"}, nil, &bytes.Buffer{}, &stderr, strong); code != exitUsage {
		t.Errorf("argument: exit %d", code)
	}
	// Serves until the context ends.
	ctx, cancel := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	done := make(chan int)
	go func() { done <- run(ctx, []string{"-http", "127.0.0.1:0"}, nil, &stdout, &stderr, strong) }()
	cancel()
	if code := <-done; code != exitOK {
		t.Errorf("serve: exit %d, %s", code, stderr.String())
	}
}

// SPEC-v0 section 10.1: the test interface is never part of the release. The binary and
// the image are built from cmd/home-mandate, which must not depend on this tool or on
// the test harness of mandate-spec.
func TestTheReleaseBinaryHasNoTestInterface(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/home-mandate/home-mandate/cmd/home-mandate").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasSuffix(pkg, "/tools/conformance") || strings.Contains(pkg, "mandate-spec/internal/harness") {
			t.Errorf("the release binary depends on %s", pkg)
		}
	}
	if !strings.Contains(string(out), "github.com/home-mandate/home-mandate/internal/pdp") {
		t.Error("go list did not list the binary's packages")
	}
}
