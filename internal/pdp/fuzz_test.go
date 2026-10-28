// SPDX-License-Identifier: AGPL-3.0-or-later

package pdp

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/home-mandate/spec"
)

// FuzzEvaluationEndpoint: whatever body arrives at the AuthZEN endpoint, the answer is
// 400 or a well-formed decision, never a panic or a 5xx; anything that is no request of
// an agent of this household on a known device is a deny, and the same body gives the
// same answer.
func FuzzEvaluationEndpoint(f *testing.F) {
	cfg, clientID := voice(f)
	h := New(cfg).Handler()
	request := func(subjectType, id, principal, entity, action, properties string) []byte {
		return []byte(`{"subject":{"type":"` + subjectType + `","id":"` + id + `","properties":{"principal":"` + principal + `"}},` +
			`"action":{"name":"` + action + `","properties":` + properties + `},"resource":{"type":"x","id":"` + entity + `"},"context":{"time":"2026-10-06T12:00:00Z"}}`)
	}
	for _, seed := range [][]byte{
		request("agent", clientID, cfg.Principal, "light.kitchen", "turn_on", `{}`),
		request("agent", clientID, cfg.Principal, "light.kitchen", "set", `{"brightness":50}`),
		request("agent", clientID, cfg.Principal, "light.kitchen", "set", `{"brightness":50.00000000000000000001}`),
		request("agent", clientID, cfg.Principal, "lock.front", "unlock", `{"code":"1234"}`),
		request("agent", clientID, cfg.Principal, "camera.porch", "snapshot", `{"x":[1,{"y":null}]}`),
		request("agent", clientID, "household:other", "light.kitchen", "turn_on", `null`),
		request("user", clientID, cfg.Principal, "light.kitchen", "turn_on", `{"a":1e400}`),
		[]byte(`{}`), []byte(`{} {}`), []byte(`[]`), []byte(`{"subject":{"type":"agent","id":"\ud800"}}`),
	} {
		f.Add(seed)
	}
	// Every conformance case of the specification as a request of the voice assistant.
	data, err := fs.ReadFile(spec.FS(), spec.CasesPath)
	if err != nil {
		f.Fatal(err)
	}
	var file struct {
		Cases []conformanceCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		f.Fatal(err)
	}
	for _, c := range file.Cases {
		params, _ := json.Marshal(c.Parameters)
		f.Add(request("agent", clientID, cfg.Principal, c.RawResource.EntityID, c.Action, string(params)))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		code, got := evaluate(t, h, body)
		if code == http.StatusBadRequest {
			return
		}
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		switch got.Context.Outcome {
		case "allow", "ask", "deny":
		default:
			t.Fatalf("unknown outcome %q", got.Context.Outcome)
		}
		if got.Decision != (got.Context.Outcome == "allow") {
			t.Fatalf("decision %v with outcome %q", got.Decision, got.Context.Outcome)
		}
		var req Request
		_ = json.Unmarshal(body, &req)
		_, known := cfg.Catalog.Lookup(req.Resource.ID)
		ours := req.Subject.Type == "agent" && req.Subject.ID == clientID && req.Subject.Properties.Principal == cfg.Principal
		if (!ours || !known) && got.Context.Outcome != "deny" {
			t.Fatalf("request of no agent of this household or on an unknown device was not denied: %s", body)
		}
		if again, other := evaluate(t, h, body); again != code || !equalResponses(got, other) {
			t.Fatalf("not deterministic: %+v, then %+v", got, other)
		}
	})
}

func evaluate(t *testing.T, h http.Handler, body []byte) (int, Response) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, evaluationPath, bytes.NewReader(body)))
	var out Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("answer is no JSON: %v\n%s", err, rec.Body)
		}
	}
	return rec.Code, out
}

func equalResponses(a, b Response) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
