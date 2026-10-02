// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// fakeHAAuth is Home Assistant's /auth/token and /auth/revoke.
type fakeHAAuth struct {
	srv *httptest.Server

	mu       sync.Mutex
	status   int
	body     string
	forms    []url.Values
	revoked  []string
	exchange int
}

func newFakeHAAuth(t *testing.T) *fakeHAAuth {
	t.Helper()
	f := &fakeHAAuth{status: http.StatusOK,
		body: `{"access_token":"ha-access","refresh_token":"ha-refresh","token_type":"Bearer","expires_in":1800}`}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/auth/token":
			f.exchange++
			f.forms = append(f.forms, r.PostForm)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.body))
		case "/auth/revoke":
			f.revoked = append(f.revoked, r.PostForm.Get("token"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHAAuth) roots() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(f.srv.Certificate())
	return p
}

func (f *fakeHAAuth) set(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
}

func (f *fakeHAAuth) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

func (f *fakeHAAuth) exchanges() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exchange
}

type userLookup = func(context.Context, string, *x509.CertPool, ha.Secret) (ha.User, error)

func newSignIn(t *testing.T, f *fakeHAAuth, user userLookup) *HASignIn {
	t.Helper()
	s, err := NewHASignIn(HASignInConfig{
		PublicURL: "https://hm.example.org", BrowserURL: "https://ha.example.org", HTTPURL: f.srv.URL,
		WebSocketURL: "wss://ha.example.org/api/websocket", Roots: f.roots(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.currentUser = user
	return s
}

func TestAuthorizeURL(t *testing.T) {
	f := newFakeHAAuth(t)
	s := newSignIn(t, f, nil)
	u, err := url.Parse(s.AuthorizeURL("st-123"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme+"://"+u.Host+u.Path != "https://ha.example.org/auth/authorize" || q.Get("client_id") != "https://hm.example.org/" ||
		q.Get("redirect_uri") != "https://hm.example.org"+CallbackPath || q.Get("state") != "st-123" || q.Get("response_type") != "code" {
		t.Errorf("authorize URL = %s", u)
	}
}

func TestSignIn(t *testing.T) {
	f := newFakeHAAuth(t)
	var gotToken ha.Secret
	s := newSignIn(t, f, func(_ context.Context, ws string, _ *x509.CertPool, token ha.Secret) (ha.User, error) {
		gotToken = token
		if ws != "wss://ha.example.org/api/websocket" {
			t.Errorf("websocket URL %s", ws)
		}
		return ha.User{ID: "u-1", Name: "Markus", IsAdmin: true}, nil
	})
	u, err := s.SignIn(context.Background(), "ha-code")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "u-1" || !u.IsAdmin || gotToken != "ha-access" {
		t.Errorf("user %+v, token %q", u, string(gotToken))
	}
	form := f.forms[0]
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "ha-code" || form.Get("client_id") != "https://hm.example.org/" {
		t.Errorf("token request = %v", form)
	}
	// Home-Mandate keeps no Home Assistant token of the human.
	if got := f.revokedTokens(); len(got) != 1 || got[0] != "ha-refresh" {
		t.Errorf("revoked = %v", got)
	}
}

func TestSignInRejects(t *testing.T) {
	lookupFails := func(context.Context, string, *x509.CertPool, ha.Secret) (ha.User, error) {
		return ha.User{}, ha.ErrAuthInvalid
	}
	lookupOK := func(context.Context, string, *x509.CertPool, ha.Secret) (ha.User, error) {
		return ha.User{ID: "u-1"}, nil
	}
	tests := map[string]struct {
		status        int
		body, code    string
		lookup        userLookup
		wantRevoke    bool
		wantExchanges int
	}{
		"code rejected":     {http.StatusBadRequest, `{"error":"invalid_request"}`, "c", lookupOK, false, 1},
		"malformed answer":  {http.StatusOK, `{`, "c", lookupOK, false, 1},
		"no access token":   {http.StatusOK, `{"refresh_token":"ha-refresh"}`, "c", lookupOK, true, 1},
		"user lookup fails": {http.StatusOK, "", "c", lookupFails, true, 1},
		"oversized answer":  {http.StatusOK, `{"access_token":"` + strings.Repeat("a", 70<<10) + `"}`, "c", lookupOK, false, 1},
		"empty code":        {http.StatusOK, "", "", lookupOK, false, 0},
		"code with spaces":  {http.StatusOK, "", "a b", lookupOK, false, 0},
		"oversized code":    {http.StatusOK, "", strings.Repeat("a", 600), lookupOK, false, 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFakeHAAuth(t)
			if tc.body != "" {
				f.set(tc.status, tc.body)
			}
			s := newSignIn(t, f, tc.lookup)
			if _, err := s.SignIn(context.Background(), tc.code); !errors.Is(err, ErrSignInFailed) {
				t.Errorf("SignIn = %v, want ErrSignInFailed", err)
			}
			if got := len(f.revokedTokens()) > 0; got != tc.wantRevoke {
				t.Errorf("revoked = %v, want %v", f.revokedTokens(), tc.wantRevoke)
			}
			if n := f.exchanges(); n != tc.wantExchanges {
				t.Errorf("exchanges = %d", n)
			}
		})
	}
}

func TestSignInErrorsDoNotLeakSecrets(t *testing.T) {
	f := newFakeHAAuth(t)
	s := newSignIn(t, f, func(context.Context, string, *x509.CertPool, ha.Secret) (ha.User, error) {
		return ha.User{}, errors.New("boom")
	})
	_, err := s.SignIn(context.Background(), "ha-code")
	if err == nil || strings.Contains(err.Error(), "ha-access") || strings.Contains(err.Error(), "ha-refresh") || strings.Contains(err.Error(), "ha-code") {
		t.Errorf("error = %v", err)
	}
}

func TestNewHASignInValidates(t *testing.T) {
	base := HASignInConfig{PublicURL: "https://hm.example.org", BrowserURL: "https://ha.example.org",
		HTTPURL: "https://ha.example.org", WebSocketURL: "wss://ha.example.org/api/websocket"}
	for name, edit := range map[string]func(*HASignInConfig){
		"no public url":     func(c *HASignInConfig) { c.PublicURL = "" },
		"no browser url":    func(c *HASignInConfig) { c.BrowserURL = "" },
		"plaintext ha http": func(c *HASignInConfig) { c.HTTPURL = "http://ha.example.org" },
		"no websocket":      func(c *HASignInConfig) { c.WebSocketURL = "" },
	} {
		cfg := base
		edit(&cfg)
		if _, err := NewHASignIn(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewHASignIn(base); err != nil {
		t.Errorf("valid config: %v", err)
	}
}
