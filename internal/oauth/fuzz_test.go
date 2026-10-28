// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fuzzEndpoints are the endpoints of the authorization server that read input.
var fuzzEndpoints = []string{
	"GET " + AuthorizePath,
	"GET " + CallbackPath,
	"GET " + ConsentPath,
	"POST " + ConsentPath,
	"POST " + TokenPath,
	"POST " + DevicePath,
	"GET " + PairPath,
	"POST " + PairPath,
}

// FuzzEndpoints: any query and form an agent or a browser sends to the authorization
// server is answered without a panic and without a 5xx; the token and device endpoints
// answer an error as OAuth JSON; a redirect goes only to Home Assistant's sign-in, a
// registered redirect URI of the client or a page of the server itself; and without a
// completed flow no agent is admitted.
func FuzzEndpoints(f *testing.F) {
	h := newHarness(f)
	_, challenge := pkce()
	for _, seed := range []struct {
		endpoint     uint8
		query, form  string
		formEncoding bool
	}{
		{0, strings.TrimPrefix(authorizeQuery(challenge, nil), AuthorizePath+"?"), "", false},
		{0, "response_type=code&client_id=n8n-kitchen&redirect_uri=https://evil.example/cb&state=x", "", false},
		{0, "response_type=code&client_id=https://claude.example.org/client.json&redirect_uri=http://127.0.0.1:1/callback&code_challenge=x&code_challenge_method=plain", "", false},
		{1, "code=admin-code&state=x", "", false},
		{3, "", "csrf=x&action=approve&name=Claude&template=voice-assistant", true},
		{4, "", "grant_type=authorization_code&code=x&client_id=" + url.QueryEscape(testClient) + "&redirect_uri=" + url.QueryEscape(testRedirect) + "&code_verifier=x", true},
		{4, "", "grant_type=refresh_token&refresh_token=hm_rt_x&client_id=n8n-kitchen", true},
		{4, "", "grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=x&client_id=n8n-kitchen", true},
		{4, "", "grant_type=authorization_code&grant_type=refresh_token", true},
		{4, "", `{"grant_type":"refresh_token"}`, false},
		{5, "", "client_id=n8n-kitchen&resource=" + url.QueryEscape(testResource), true},
		{6, "code=ABCD-EFGH", "", false},
		{7, "", "csrf=x&code=ABCD-EFGH", true},
		{7, "", "%zz=1", true},
	} {
		f.Add(seed.endpoint, seed.query, []byte(seed.form), seed.formEncoding)
	}
	agentsBefore := countAgents(f, h)
	handler := h.server.Handler()
	f.Fuzz(func(t *testing.T, which uint8, query string, form []byte, formEncoding bool) {
		h.clock.Add(time.Minute) // past the rate limits
		method, path, _ := strings.Cut(fuzzEndpoints[int(which)%len(fuzzEndpoints)], " ")
		var body io.Reader
		if method == http.MethodPost {
			body = bytes.NewReader(form)
		}
		req, err := http.NewRequest(method, testPublicURL+path+"?"+query, body)
		if err != nil {
			return
		}
		if formEncoding {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("Origin", testPublicURL)
		req.RemoteAddr = "192.0.2.1:40000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code >= http.StatusInternalServerError && rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s?%s: %d %s", method, path, query, rec.Code, rec.Body)
		}
		if (path == TokenPath || path == DevicePath) && rec.Code >= http.StatusBadRequest {
			var e struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
				t.Fatalf("%s: %d is no OAuth error: %s", path, rec.Code, rec.Body)
			}
		}
		if location := rec.Header().Get("Location"); location != "" && !permittedRedirect(location) {
			t.Fatalf("%s %s?%s redirects to %q", method, path, query, location)
		}
		if n := countAgents(t, h); n != agentsBefore {
			t.Fatalf("%d agents after %s %s?%s, %d before", n, method, path, query, agentsBefore)
		}
	})
}

// permittedRedirect: Home Assistant's sign-in, a registered redirect URI of a client of
// the harness (a loopback one on any port), or a path of the server itself.
func permittedRedirect(location string) bool {
	u, err := url.Parse(location)
	if err != nil {
		return false
	}
	switch {
	case u.Scheme == "" && u.Host == "" && strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") && !strings.HasPrefix(location, "/\\"):
		return true
	case u.User != nil || u.Fragment != "":
		return false
	case u.Scheme == "https" && u.Host == "ha.test" && u.Path == "/auth/authorize":
		return true
	case u.Scheme == "https" && u.Host == "app.example.org" && u.EscapedPath() == "/cb":
		return true
	}
	return u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.EscapedPath() == "/callback"
}

// FuzzClientMetadata: a Client ID Metadata Document comes from anywhere on the internet.
// Whatever it holds, Resolve returns ErrInvalidClient or a client whose ID is the one
// asked for, whose redirect URIs are all https or loopback http without user info or
// fragment, and whose name can be shown; a redirect URI is accepted for it only if it
// is as safe as a registered one.
func FuzzClientMetadata(f *testing.F) {
	const clientID = "https://client.example/oauth/client.json"
	for _, seed := range []string{
		goodDoc,
		`{"client_id":"$ID","redirect_uris":["https://a.example/cb"],"token_endpoint_auth_methods_supported":["private_key_jwt","none"],"token_endpoint_auth_method":"private_key_jwt"}`,
		`{"client_id":"$ID","client_name":"Evil\u202eedoC","redirect_uris":["http://[::1]:9/cb?x=1"]}`,
		`{"client_id":"$ID","client_id":"$ID","redirect_uris":[]}`,
		`{"client_id":"$ID","redirect_uris":["javascript:alert(1)"]}`,
		`{"client_id":"$ID","redirect_uris":["https://user@a.example/cb#f"]}`,
		`{"client_id":"$ID","redirect_uris":"https://a.example/cb"}`,
		`{"client_id":"https://other.example/c.json"}`,
		`[]`, `{`, ``,
	} {
		f.Add([]byte(strings.ReplaceAll(seed, "$ID", clientID)), "application/json", "http://127.0.0.1:1/callback")
	}
	f.Fuzz(func(t *testing.T, doc []byte, contentType, requested string) {
		r := NewCIMDResolver(nil)
		r.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
				Body: io.NopCloser(bytes.NewReader(doc))}, nil
		})}
		c, err := r.Resolve(context.Background(), clientID)
		if err != nil {
			if !errors.Is(err, ErrInvalidClient) {
				t.Fatalf("undefined error %v", err)
			}
			return
		}
		if c.ID != clientID || !c.Verified || !displayable(c.Name) || len(c.RedirectURIs) > maxRedirectURIs {
			t.Fatalf("client %+v", c)
		}
		for _, uri := range c.RedirectURIs {
			u, err := url.Parse(uri)
			if err != nil || u.User != nil || u.Fragment != "" || !(u.Scheme == "https" || u.Scheme == "http" && loopbackHost(u.Hostname())) {
				t.Fatalf("redirect URI %q accepted", uri)
			}
		}
		if redirectAllowed(c.RedirectURIs, requested) {
			u, err := url.Parse(requested)
			if err != nil || u.User != nil || u.Fragment != "" || !(u.Scheme == "https" || u.Scheme == "http" && loopbackHost(u.Hostname())) {
				t.Fatalf("redirect to %q allowed for %q", requested, c.RedirectURIs)
			}
		}
	})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
