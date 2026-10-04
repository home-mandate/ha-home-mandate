// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Negative catalog, UI: Ingress requests from anywhere but 172.30.32.2 get nothing, not
// the API and not the UI. Forwarding headers change nothing.
func TestOnlyTheSupervisorIsServed(t *testing.T) {
	h := newHarness(t)
	for _, addr := range []string{"172.30.32.1:1234", "172.30.32.3:1", "127.0.0.1:80", "[::1]:80", "192.168.1.10:5000",
		"172.30.32.2", "garbage", "[fe80::1%eth0]:1"} {
		for _, path := range []string{"/api/session", "/", "/assets/x.js"} {
			r := h.do(http.MethodGet, path, nil, from(addr), header("X-Forwarded-For", supervisorAddr), header("Forwarded", "for="+supervisorAddr))
			if r.code != http.StatusForbidden || len(r.body) != 0 || strings.Contains(string(r.body), "ui") {
				t.Errorf("%s %s = %d %q", addr, path, r.code, r.body)
			}
		}
	}
	// The Supervisor, also as an IPv4-mapped IPv6 address.
	for _, addr := range []string{remote, "[::ffff:172.30.32.2]:9"} {
		if r := h.do(http.MethodGet, "/api/session", nil, from(addr)); r.code != http.StatusOK {
			t.Errorf("%s = %d", addr, r.code)
		}
	}
	if r := h.do(http.MethodGet, "/", nil); r.code != http.StatusOK || string(r.body) != "ui" {
		t.Errorf("UI = %d %q", r.code, r.body)
	}
	h.srv.cfg.UI = nil
	if r := h.do(http.MethodGet, "/index.html", nil); r.code != http.StatusNotFound {
		t.Errorf("without UI = %d", r.code)
	}
}

// Negative catalog, UI: API calls without a valid Ingress user or as a non-admin.
func TestAPINeedsAnAdministrator(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name string
		opts []reqOpt
		code string
	}{
		{"no user", []reqOpt{header("X-Remote-User-Id", "")}, codeUnauthenticated},
		{"malformed user", []reqOpt{as("u1; DROP")}, codeUnauthenticated},
		{"long user", []reqOpt{as(strings.Repeat("a", 65))}, codeUnauthenticated},
		{"unknown user", []reqOpt{as("ffffffffffffffffffffffffffffffff")}, codeForbidden},
		{"no administrator", []reqOpt{as(guestID)}, codeForbidden},
	}
	for _, tc := range tests {
		for _, path := range []string{"/api/session", "/api/agents", "/api/audit", "/api/events", "/api/nothing"} {
			if r := h.do(http.MethodGet, path, nil, tc.opts...); r.errCode() != tc.code {
				t.Errorf("%s %s = %d %s", tc.name, path, r.code, r.body)
			}
		}
	}
	// Two user headers: the Supervisor sets exactly one.
	req := func(r *http.Request) { r.Header.Add("X-Remote-User-Id", guestID) }
	if r := h.do(http.MethodGet, "/api/session", nil, req); r.errCode() != codeUnauthenticated {
		t.Errorf("two users = %d", r.code)
	}
}

// Without an answer from Home Assistant nobody is an administrator (fail closed); a
// failed refresh is never answered from an older list.
func TestAdministratorCheckFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.ok(http.MethodGet, "/api/session", nil, nil)
	h.ha.set(func(f *fakeHA) { f.usersErr = errHA })
	h.now.Add(usersTTL)
	if r := h.do(http.MethodGet, "/api/session", nil); r.code != http.StatusServiceUnavailable || r.errCode() != codeUnavailable {
		t.Errorf("Home Assistant down = %d %s", r.code, r.body)
	}
	if admin, err := h.srv.IsAdmin(t.Context(), adminID); admin || err == nil {
		t.Errorf("IsAdmin while down = %v, %v", admin, err)
	}
	// After a failure Home Assistant is asked again only after usersRetry: requests do
	// not queue up behind one slow call each, and nobody is let in meanwhile.
	calls := h.ha.userCalls
	h.ha.set(func(f *fakeHA) { f.usersErr = nil })
	if r := h.do(http.MethodGet, "/api/session", nil); r.errCode() != codeUnavailable || h.ha.userCalls != calls {
		t.Errorf("within the retry time = %d, %d calls", r.code, h.ha.userCalls-calls)
	}
	h.now.Add(usersRetry)
	h.ok(http.MethodGet, "/api/session", nil, nil)
}

// Rights withdrawn in Home Assistant take effect within usersTTL.
func TestWithdrawnAdministratorRights(t *testing.T) {
	h := newHarness(t)
	h.ok(http.MethodGet, "/api/agents", nil, nil, as(annaID))
	calls := h.ha.userCalls
	h.ok(http.MethodGet, "/api/agents", nil, nil, as(annaID))
	if h.ha.userCalls != calls {
		t.Error("users asked again within the cache time")
	}
	h.ha.set(func(f *fakeHA) { f.users[1].GroupIDs = []string{"system-users"} })
	h.now.Add(usersTTL)
	if r := h.do(http.MethodGet, "/api/agents", nil, as(annaID)); r.errCode() != codeForbidden {
		t.Errorf("after the rights were withdrawn = %d", r.code)
	}
	// A clock going backwards refreshes too.
	h.now.Add(-time.Hour)
	h.ok(http.MethodGet, "/api/agents", nil, nil)
}

// Negative catalog, UI: a write without the CSRF token, with another user's token, an
// old token, or not from the page itself.
func TestWritesNeedCSRFAndSameOrigin(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{"language": "de"}
	path := "/api/session/language"
	for name, tc := range map[string]struct {
		opts []reqOpt
		code string
	}{
		"no token":          {[]reqOpt{header("X-HM-CSRF", "")}, codeCSRF},
		"garbage token":     {[]reqOpt{header("X-HM-CSRF", "not-base64!")}, codeCSRF},
		"short token":       {[]reqOpt{header("X-HM-CSRF", "YWJj")}, codeCSRF},
		"token of another":  {[]reqOpt{header("X-HM-CSRF", h.srv.csrfToken(annaID, h.now.Now()))}, codeCSRF},
		"cross site":        {[]reqOpt{header("Sec-Fetch-Site", "cross-site")}, codeForbidden},
		"same site":         {[]reqOpt{header("Sec-Fetch-Site", "same-site")}, codeForbidden},
		"no Sec-Fetch-Site": {[]reqOpt{header("Sec-Fetch-Site", "")}, codeForbidden},
		"two tokens": {[]reqOpt{func(r *http.Request) {
			r.Header["X-Hm-Csrf"] = []string{h.srv.csrfToken(adminID, h.now.Now()), "x"}
		}}, codeCSRF},
		"token of an old run": {[]reqOpt{header("X-HM-CSRF", newHarness(t).srv.csrfToken(adminID, h.now.Now()))}, codeCSRF},
	} {
		r := h.do(http.MethodPut, path, body, tc.opts...)
		if r.errCode() != tc.code || r.code != http.StatusForbidden {
			t.Errorf("%s = %d %s", name, r.code, r.body)
		}
	}
	// A token of the previous 12-hour period still works, one of before not.
	token := h.srv.csrfToken(adminID, h.now.Now())
	h.now.Add(csrfPeriod)
	h.ok(http.MethodPut, path, body, nil, header("X-HM-CSRF", token))
	h.now.Add(csrfPeriod)
	if r := h.do(http.MethodPut, path, body, header("X-HM-CSRF", token)); r.errCode() != codeCSRF {
		t.Errorf("token two periods old = %d", r.code)
	}
}

func TestRequestBodies(t *testing.T) {
	h := newHarness(t)
	path := "/api/session/language"
	tests := []struct {
		name   string
		body   any
		opts   []reqOpt
		status int
		code   string
		field  string
	}{
		{"too large", `{"language":"` + strings.Repeat("x", maxBody) + `"}`, nil, http.StatusRequestEntityTooLarge, codeTooLarge, ""},
		{"not JSON content type", `{"language":"de"}`, []reqOpt{header("Content-Type", "text/plain")}, http.StatusBadRequest, codeInvalidInput, ""},
		{"form", "language=de", []reqOpt{header("Content-Type", "application/x-www-form-urlencoded")}, http.StatusBadRequest, codeInvalidInput, ""},
		{"unknown field", `{"language":"de","admin":true}`, nil, http.StatusBadRequest, codeInvalidInput, "/admin"},
		{"wrong type", `{"language":7}`, nil, http.StatusBadRequest, codeInvalidInput, "/language"},
		{"two objects", `{"language":"de"}{}`, nil, http.StatusBadRequest, codeInvalidInput, ""},
		{"trailing garbage", `{"language":"de"} x`, nil, http.StatusBadRequest, codeInvalidInput, ""},
		{"array", `[]`, nil, http.StatusBadRequest, codeInvalidInput, ""},
		{"empty", ``, nil, http.StatusBadRequest, codeInvalidInput, ""},
		{"deep nesting", strings.Repeat("[", 10000), nil, http.StatusBadRequest, codeInvalidInput, ""},
		{"strange field name", `{"<script>":1}`, nil, http.StatusBadRequest, codeInvalidInput, ""},
	}
	for _, tc := range tests {
		r := h.do(http.MethodPut, path, tc.body, tc.opts...)
		if r.code != tc.status || r.errCode() != tc.code || r.field() != tc.field {
			t.Errorf("%s = %d %s", tc.name, r.code, r.body)
		}
	}
	// JSON with a charset is JSON.
	h.ok(http.MethodPut, path, `{"language":"en"}`, nil, header("Content-Type", "application/json; charset=utf-8"))
	// A body where none belongs.
	if r := h.do(http.MethodPost, "/api/audit/verify", `{}`); r.errCode() != codeInvalidInput {
		t.Errorf("body on a route without one = %d", r.code)
	}
	if r := h.do(http.MethodGet, "/api/session", `{"x":1}`); r.errCode() != codeInvalidInput {
		t.Errorf("body on a GET = %d", r.code)
	}
}

func TestEveryAnswerHasTheSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	for _, r := range []result{h.do(http.MethodGet, "/api/session", nil), h.do(http.MethodGet, "/api/nope", nil),
		h.do(http.MethodDelete, "/api/templates/none", nil), h.do(http.MethodGet, "/api/session", nil, as(guestID))} {
		hdr := r.header
		if hdr.Get("Cache-Control") != "no-store" || hdr.Get("X-Content-Type-Options") != "nosniff" ||
			hdr.Get("Content-Type") != "application/json" || !strings.Contains(hdr.Get("Content-Security-Policy"), "default-src 'none'") ||
			hdr.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%d: headers %v", r.code, hdr)
		}
	}
	// 204 too.
	h.ok(http.MethodPut, "/api/templates/spare", map[string]any{"draft": draft(t)}, nil)
	r := h.do(http.MethodDelete, "/api/templates/spare", nil)
	if r.code != http.StatusNoContent || r.header.Get("Cache-Control") != "no-store" || len(r.body) != 0 {
		t.Errorf("204 = %d %v", r.code, r.header)
	}
}

func TestUnknownRoutesAndMethods(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api"}, {http.MethodGet, "/api/"}, {http.MethodGet, "/api/admin"},
		{http.MethodDelete, "/api/agents"}, {http.MethodPost, "/api/session"}, {http.MethodGet, "/api/agents/revoke"},
		{"PATCH", "/api/settings"}, {http.MethodGet, "/api/mcp"},
	} {
		if r := h.do(tc.method, tc.path, nil); r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d", tc.method, tc.path, r.code)
		}
	}
}

func TestRequestLimitPerUser(t *testing.T) {
	h := newHarness(t)
	for range requestLimit {
		h.ok(http.MethodGet, "/api/settings", nil, nil)
	}
	r := h.do(http.MethodGet, "/api/settings", nil)
	if r.code != http.StatusTooManyRequests || r.errCode() != codeRateLimited || r.header.Get("Retry-After") != "60" {
		t.Errorf("over the limit = %d %v %s", r.code, r.header, r.body)
	}
	// Another administrator is not limited by it.
	h.ok(http.MethodGet, "/api/settings", nil, nil, as(annaID))
	h.now.Add(requestPeriod)
	h.ok(http.MethodGet, "/api/settings", nil, nil)
}

func TestLimitsForgetOldWindows(t *testing.T) {
	c := &clock{t: testStart}
	l := newLimits(c.Now)
	for i := range 10001 {
		l.allow(strings.Repeat("k", i%50)+string(rune('a'+i%26))+time.Duration(i).String(), 1, time.Minute)
	}
	c.Add(2 * time.Hour)
	l.allow("new", 1, time.Minute)
	if n := len(l.windows); n > 2 {
		t.Errorf("%d windows kept", n)
	}
	// A clock jump backwards starts a new window.
	if ok, _ := l.allow("new", 1, time.Minute); ok {
		t.Error("second request in the window allowed")
	}
	c.Add(-time.Hour)
	if ok, _ := l.allow("new", 1, time.Minute); !ok {
		t.Error("window not reset after the clock went back")
	}
}

func TestInternalErrorsShowNoDetails(t *testing.T) {
	h := newHarness(t)
	_ = h.st.Close()
	r := h.do(http.MethodGet, "/api/agents", nil)
	if r.code != http.StatusInternalServerError || r.errCode() != codeInternal || strings.Contains(string(r.body), "sql") ||
		strings.Contains(string(r.body), "closed") {
		t.Errorf("database closed = %d %s", r.code, r.body)
	}
}

// Decision U2: the trusted address is configured (container mode: the own proxy). Exactly
// that one is served; without one, nobody.
func TestConfiguredProxyAddress(t *testing.T) {
	h := newHarness(t)
	h.srv.cfg.Proxy = netip.MustParseAddr("10.20.30.40")
	if r := h.do(http.MethodGet, "/api/session", nil, from("10.20.30.40:5000")); r.code != http.StatusOK {
		t.Errorf("configured proxy = %d", r.code)
	}
	if r := h.do(http.MethodGet, "/api/session", nil); r.code != http.StatusForbidden {
		t.Errorf("Supervisor address while another proxy is configured = %d", r.code)
	}
	h.srv.cfg.Proxy = netip.MustParseAddr("fd00::2")
	if r := h.do(http.MethodGet, "/api/session", nil, from("[fd00::2]:5000")); r.code != http.StatusOK {
		t.Errorf("IPv6 proxy = %d", r.code)
	}
	h.srv.cfg.Proxy = netip.Addr{}
	for _, addr := range []string{remote, "10.20.30.40:5000", "[::]:1"} {
		if r := h.do(http.MethodGet, "/api/session", nil, from(addr)); r.code != http.StatusForbidden {
			t.Errorf("no proxy configured, %s = %d", addr, r.code)
		}
	}
}
