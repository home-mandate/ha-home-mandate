// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fuzzRoutes are the routes that read a JSON body, and the audit query; {} is the path
// parameter of the input.
var fuzzRoutes = []string{
	"PUT /api/session/language",
	"POST /api/agents/revoke",
	"POST /api/agents/remove",
	"POST /api/pairing/check",
	"POST /api/pairing/approve",
	"POST /api/pairing/deny",
	"POST /api/pairing/reconnect",
	"PUT /api/devices/critical",
	"POST /api/renames/apply",
	"POST /api/renames/dismiss",
	"POST /api/area-moves/dismiss",
	"POST /api/mandates",
	"PUT /api/mandates/{}",
	"POST /api/mandates/{}/apply-template",
	"POST /api/mandates/{}/audit-mode/confirm",
	"PUT /api/templates/{}",
	"PUT /api/templates/{}/hidden",
	"POST /api/templates/{}/apply",
	"PUT /api/settings",
	"POST /api/approvals/{}/answer",
	"PUT /api/approvers/{}",
	"PUT /api/emergency-stop",
	"GET /api/audit?{}",
}

// FuzzWriteRequests: whatever an administrator's browser sends to a route that reads a
// body (or to the audit query), the answer is a defined error or a success, never a
// panic or an internal error, and a refusal changes nothing in the audit log. In the
// inputs, MANDATE and CLIENT stand for the mandate and the client ID of an admitted
// agent, so that the fuzzer reaches the checks behind them.
func FuzzWriteRequests(f *testing.F) {
	h := newHarness(f)
	a := h.admit("Kitchen")
	info := h.mandateOf(a.ClientID)
	mandateID := info.ID
	route := func(r string) uint8 {
		for i, candidate := range fuzzRoutes {
			if candidate == r {
				return uint8(i)
			}
		}
		f.Fatalf("no route %q", r)
		return 0
	}
	draft, _ := json.Marshal(map[string]any{"name": "Kitchen", "draft": draft(f), "base_digest": info.Digest})
	for _, seed := range []struct {
		route, param, body string
	}{
		{"PUT /api/session/language", "", `{"language":"en"}`},
		{"POST /api/agents/remove", "", `{"client_id":"CLIENT","confirm":"Kitchen"}`},
		{"POST /api/pairing/check", "", `{"code":"ABCD-EFGH"}`},
		{"POST /api/pairing/approve", "", `{"code":"ABCD-EFGH","template":"voice-assistant","name":"x"}`},
		{"PUT /api/devices/critical", "", `{"entity_id":"light.kitchen","critical":true}`},
		{"POST /api/renames/apply", "", `{"from":"light.old","to":"light.kitchen"}`},
		{"POST /api/area-moves/dismiss", "", `{"entity_id":"light.kitchen"}`},
		{"POST /api/mandates", "", `{"client_id":"CLIENT","template":"voice-assistant","name":"x"}`},
		{"PUT /api/mandates/{}", "MANDATE", string(draft)},
		{"PUT /api/mandates/{}", "MANDATE", `{"name":"x","draft":{"rules":[{"id":"r","resource":{"any":true},"actions":["*"],"decision":"allow"}]},"base_digest":"x"}`},
		{"POST /api/mandates/{}/apply-template", "MANDATE", `{"template":"voice-assistant"}`},
		{"POST /api/mandates/{}/audit-mode/confirm", "MANDATE", `{"digest":"sha256:00"}`},
		{"PUT /api/templates/{}", "mine", `{"draft":{"default":"deny","rules":[]}}`},
		{"PUT /api/templates/{}/hidden", "voice-assistant", `{"hidden":true}`},
		{"POST /api/templates/{}/apply", "voice-assistant", `{"mandates":["MANDATE"]}`},
		{"PUT /api/settings", "", `{"approval_timeout_seconds":120}`},
		{"POST /api/approvals/{}/answer", "0123456789abcdef0123456789abcdef", `{"approve":true}`},
		{"PUT /api/approvers/{}", annaID, `{"devices":[{"service":"mobile_app_pixel_9","critical":true}]}`},
		{"PUT /api/emergency-stop", "", `{"active":false}`},
		{"GET /api/audit?{}", "agent=CLIENT&limit=5&before=99&event=decision", ``},
		{"PUT /api/settings", "", `{"x":1} {}`},
		{"PUT /api/settings", "", `[`},
		{"POST /api/agents/revoke", "", `{"client_id":"CLIENT"}`},
	} {
		f.Add(route(seed.route), seed.param, []byte(seed.body))
	}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, which uint8, param string, body []byte) {
		h.now.Add(time.Minute) // past the request limits
		placeholders := strings.NewReplacer("MANDATE", mandateID, "CLIENT", a.ClientID)
		method, path, _ := strings.Cut(fuzzRoutes[int(which)%len(fuzzRoutes)], " ")
		param = placeholders.Replace(param)
		if strings.HasSuffix(path, "?{}") {
			path = strings.Replace(path, "{}", param, 1) // a query that does not parse is refused below
			body = nil
		} else {
			path = strings.Replace(path, "{}", url.PathEscape(param), 1)
			body = []byte(placeholders.Replace(string(body)))
		}
		req, err := http.NewRequest(method, "http://ingress.local"+path, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.RemoteAddr = remote
		req.Header.Set("X-Remote-User-Id", adminID)
		if method != http.MethodGet {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("X-HM-CSRF", h.srv.csrfToken(adminID, h.now.Now()))
		}
		before, err := h.log.LastSeq(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, req)
		if rec.Code >= http.StatusInternalServerError && rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
		}
		if rec.Code < http.StatusBadRequest {
			return
		}
		var e errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || statusOf[e.Code] != rec.Code {
			t.Fatalf("%s %s: %d with an undefined error %s", method, path, rec.Code, rec.Body)
		}
		if after, err := h.log.LastSeq(ctx); err != nil || after != before {
			t.Fatalf("%s %s refused (%s) but the audit log grew from %d to %d (%v)", method, path, e.Code, before, after, err)
		}
	})
}
