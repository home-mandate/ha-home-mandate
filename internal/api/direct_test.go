// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/home-mandate/home-mandate/internal/ha"
)

const publicURL = "https://hm.example.org:8765"

// fakeSignIn plays Home Assistant's sign-in: a code names the user it signs in.
type fakeSignIn struct {
	mu    sync.Mutex
	codes map[string]string
	used  []string
}

func (f *fakeSignIn) AuthorizeURL(state string) string {
	return "https://ha.example.org/auth/authorize?state=" + url.QueryEscape(state)
}

func (f *fakeSignIn) SignIn(_ context.Context, code string) (ha.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.used = append(f.used, code)
	user, ok := f.codes[code]
	if !ok {
		return ha.User{}, errors.New("code refused")
	}
	return ha.User{ID: user}, nil
}

type direct struct {
	*harness
	signIn *fakeSignIn
	d      http.Handler
}

func newDirect(t *testing.T) *direct {
	t.Helper()
	h := newHarness(t)
	f := &fakeSignIn{codes: map[string]string{"code-admin": adminID, "code-anna": annaID, "code-guest": guestID, "code-bad": "../x"}}
	h.direct, h.publicURL = f, publicURL
	h.build()
	return &direct{harness: h, signIn: f, d: h.srv.DirectHandler()}
}

type browser struct {
	addr    string
	cookies map[string]*http.Cookie
}

func newBrowser() *browser {
	return &browser{addr: "192.0.2.10:50000", cookies: map[string]*http.Cookie{}}
}

func (d *direct) send(b *browser, method, target string, edit func(*http.Request)) *httptest.ResponseRecorder {
	d.t.Helper()
	req := httptest.NewRequest(method, publicURL+target, nil)
	req.RemoteAddr = b.addr
	for _, c := range b.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	if edit != nil {
		edit(req)
	}
	rec := httptest.NewRecorder()
	d.d.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = c
	}
	return rec
}

// signIn runs the sign-in through Home Assistant with code and returns where it ended.
func (d *direct) signInWith(b *browser, code string) string {
	d.t.Helper()
	start := d.send(b, http.MethodGet, "/ui/signin", nil)
	if start.Code != http.StatusSeeOther {
		d.t.Fatalf("start = %d", start.Code)
	}
	to, _ := url.Parse(start.Header().Get("Location"))
	state := to.Query().Get("state")
	end := d.send(b, http.MethodGet, "/ui/signin/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	return end.Header().Get("Location")
}

func (d *direct) api(b *browser, method, path string, edit func(*http.Request)) *httptest.ResponseRecorder {
	return d.send(b, method, "/ui/api"+path, edit)
}

func TestDirectModeIsOffWithoutSignIn(t *testing.T) {
	h := newHarness(t)
	if h.srv.DirectHandler() != nil {
		t.Error("direct mode without a sign-in")
	}
}

func TestDirectSignInOfAnAdministrator(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	start := d.send(b, http.MethodGet, "/ui/signin", nil)
	if start.Code != http.StatusSeeOther || !strings.HasPrefix(start.Header().Get("Location"), "https://ha.example.org/auth/authorize?state=") {
		t.Fatalf("start = %d %s", start.Code, start.Header().Get("Location"))
	}
	c := b.cookies[signInCookie]
	if c == nil || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 600 {
		t.Errorf("sign-in cookie %+v", c)
	}
	to, _ := url.Parse(start.Header().Get("Location"))
	if to.Query().Get("state") != c.Value {
		t.Error("the state is not the cookie's")
	}

	end := d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&state="+url.QueryEscape(c.Value), nil)
	if end.Code != http.StatusSeeOther || end.Header().Get("Location") != "/ui/" {
		t.Fatalf("callback = %d %s", end.Code, end.Header().Get("Location"))
	}
	if b.cookies[signInCookie] != nil {
		t.Error("the sign-in cookie was not cleared")
	}
	s := b.cookies[uiCookie]
	if s == nil || !s.Secure || !s.HttpOnly || s.SameSite != http.SameSiteStrictMode || s.Path != "/" || s.Domain != "" ||
		!strings.HasPrefix(s.Name, "__Host-") {
		t.Fatalf("session cookie %+v", s)
	}

	r := d.api(b, http.MethodGet, "/session", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("session = %d %s", r.Code, r.Body)
	}
	var sess wireSession
	result{code: r.Code, body: r.Body.Bytes()}.json(t, &sess)
	if sess.User.ID != adminID || !sess.SignOut || sess.CSRFToken == "" {
		t.Errorf("session %+v", sess)
	}
}

func TestDirectSignInRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(d *direct, b *browser) string
		want string
	}{
		{"no administrator", func(d *direct, b *browser) string { return d.signInWith(b, "code-guest") }, signInDenied},
		{"code refused by Home Assistant", func(d *direct, b *browser) string { return d.signInWith(b, "code-unknown") }, signInFailed},
		{"malformed user ID", func(d *direct, b *browser) string { return d.signInWith(b, "code-bad") }, signInFailed},
		{"without the sign-in cookie", func(d *direct, b *browser) string {
			start := d.send(b, http.MethodGet, "/ui/signin", nil)
			to, _ := url.Parse(start.Header().Get("Location"))
			delete(b.cookies, signInCookie)
			return d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&state="+url.QueryEscape(to.Query().Get("state")), nil).Header().Get("Location")
		}, signInFailed},
		{"state of another sign-in", func(d *direct, b *browser) string {
			d.send(b, http.MethodGet, "/ui/signin", nil)
			other := newBrowser()
			start := d.send(other, http.MethodGet, "/ui/signin", nil)
			to, _ := url.Parse(start.Header().Get("Location"))
			return d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&state="+url.QueryEscape(to.Query().Get("state")), nil).Header().Get("Location")
		}, signInFailed},
		{"state used twice", func(d *direct, b *browser) string {
			start := d.send(b, http.MethodGet, "/ui/signin", nil)
			to, _ := url.Parse(start.Header().Get("Location"))
			state := to.Query().Get("state")
			cookie := b.cookies[signInCookie]
			d.send(b, http.MethodGet, "/ui/signin/callback?code=code-anna&state="+url.QueryEscape(state), nil)
			delete(b.cookies, uiCookie)
			b.cookies[signInCookie] = cookie
			return d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&state="+url.QueryEscape(state), nil).Header().Get("Location")
		}, signInFailed},
		{"expired sign-in", func(d *direct, b *browser) string {
			start := d.send(b, http.MethodGet, "/ui/signin", nil)
			to, _ := url.Parse(start.Header().Get("Location"))
			d.now.Add(signInTTL + time.Second)
			return d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&state="+url.QueryEscape(to.Query().Get("state")), nil).Header().Get("Location")
		}, signInFailed},
		{"two codes", func(d *direct, b *browser) string {
			start := d.send(b, http.MethodGet, "/ui/signin", nil)
			to, _ := url.Parse(start.Header().Get("Location"))
			return d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&code=code-anna&state="+url.QueryEscape(to.Query().Get("state")), nil).Header().Get("Location")
		}, signInFailed},
		{"Home Assistant cannot say who is an administrator", func(d *direct, b *browser) string {
			d.ha.set(func(f *fakeHA) { f.usersErr = errors.New("down") })
			return d.signInWith(b, "code-admin")
		}, signInFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDirect(t)
			b := newBrowser()
			if got := tc.run(d, b); got != tc.want {
				t.Errorf("ended at %q, want %q", got, tc.want)
			}
			if b.cookies[uiCookie] != nil {
				t.Error("a session cookie was set")
			}
			if r := d.api(b, http.MethodGet, "/session", nil); r.Code != http.StatusUnauthorized {
				t.Errorf("session = %d, want 401", r.Code)
			}
		})
	}
}

func TestDirectModeNeverTrustsProxyHeaders(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	b.addr = supervisorAddr + ":40404" // even from the Supervisor's address
	r := d.api(b, http.MethodGet, "/session", func(r *http.Request) { r.Header.Set("X-Remote-User-Id", adminID) })
	if r.Code != http.StatusUnauthorized {
		t.Errorf("X-Remote-User-Id without a session = %d, want 401", r.Code)
	}
	// And Ingress stays as it was: the session cookie is no user there.
	d.signInWith(b, "code-admin")
	res := d.do(http.MethodGet, "/api/session", nil, header("X-Remote-User-Id", ""),
		func(r *http.Request) { r.AddCookie(&http.Cookie{Name: uiCookie, Value: b.cookies[uiCookie].Value}) })
	if res.code != http.StatusUnauthorized {
		t.Errorf("session cookie behind Ingress = %d, want 401", res.code)
	}
}

func TestDirectSessionFixation(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	b.cookies[uiCookie] = &http.Cookie{Name: uiCookie, Value: "chosen-by-the-attacker"}
	d.signInWith(b, "code-admin")
	if b.cookies[uiCookie].Value == "chosen-by-the-attacker" {
		t.Error("the sign-in kept a session ID it did not issue")
	}
	first := b.cookies[uiCookie].Value
	d.signInWith(b, "code-admin")
	if b.cookies[uiCookie].Value == first {
		t.Error("a second sign-in kept the session ID")
	}
}

func TestDirectSessionEnds(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		d := newDirect(t)
		b := newBrowser()
		d.signInWith(b, "code-admin")
		d.now.Add(uiIdle + time.Second)
		if r := d.api(b, http.MethodGet, "/session", nil); r.Code != http.StatusUnauthorized {
			t.Errorf("after the idle time = %d", r.Code)
		}
	})
	t.Run("administrator rights withdrawn", func(t *testing.T) {
		d := newDirect(t)
		b := newBrowser()
		d.signInWith(b, "code-anna")
		d.ha.set(func(f *fakeHA) { f.users[1].GroupIDs = []string{"system-users"} })
		d.now.Add(usersTTL + time.Second)
		if r := d.api(b, http.MethodGet, "/session", nil); r.Code != http.StatusForbidden {
			t.Errorf("after the rights were withdrawn = %d, want 403", r.Code)
		}
	})
	t.Run("restart", func(t *testing.T) {
		d := newDirect(t)
		b := newBrowser()
		d.signInWith(b, "code-admin")
		d.build()
		d.d = d.srv.DirectHandler()
		if r := d.api(b, http.MethodGet, "/session", nil); r.Code != http.StatusUnauthorized {
			t.Errorf("after a restart = %d", r.Code)
		}
	})
}

func TestDirectSignOut(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	d.signInWith(b, "code-admin")
	token := b.cookies[uiCookie].Value
	csrf := d.srv.csrfToken(adminID, d.now.Now())
	write := func(site, csrf string) func(*http.Request) {
		return func(r *http.Request) {
			if site != "" {
				r.Header.Set("Sec-Fetch-Site", site)
			}
			if csrf != "" {
				r.Header.Set("X-HM-CSRF", csrf)
			}
		}
	}
	for name, edit := range map[string]func(*http.Request){
		"without CSRF token":        write("same-origin", ""),
		"with another user's token": write("same-origin", d.srv.csrfToken(annaID, d.now.Now())),
		"from another site":         write("cross-site", csrf),
		"without Sec-Fetch-Site":    write("", csrf),
	} {
		if r := d.send(b, http.MethodPost, "/ui/signout", edit); r.Code != http.StatusForbidden {
			t.Errorf("sign-out %s = %d, want 403", name, r.Code)
		}
	}
	if r := d.api(b, http.MethodGet, "/session", nil); r.Code != http.StatusOK {
		t.Fatalf("a refused sign-out ended the session: %d", r.Code)
	}
	if r := d.send(b, http.MethodPost, "/ui/signout", write("same-origin", csrf)); r.Code != http.StatusNoContent {
		t.Fatalf("sign-out = %d %s", r.Code, r.Body)
	}
	if b.cookies[uiCookie] != nil {
		t.Error("the session cookie was not cleared")
	}
	b.cookies[uiCookie] = &http.Cookie{Name: uiCookie, Value: token}
	if r := d.api(b, http.MethodGet, "/session", nil); r.Code != http.StatusUnauthorized {
		t.Errorf("the old cookie after the sign-out = %d", r.Code)
	}
	if r := d.send(b, http.MethodPost, "/ui/signout", write("same-origin", csrf)); r.Code != http.StatusUnauthorized {
		t.Errorf("sign-out without a session = %d", r.Code)
	}
}

func TestDirectSignInLimitPerAddress(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	for range signInStarts {
		if r := d.send(b, http.MethodGet, "/ui/signin", nil); !strings.HasPrefix(r.Header().Get("Location"), "https://ha.example.org/") {
			t.Fatalf("start refused early: %s", r.Header().Get("Location"))
		}
	}
	if r := d.send(b, http.MethodGet, "/ui/signin", nil); r.Header().Get("Location") != signInBusy {
		t.Errorf("start beyond the limit sent to %s", r.Header().Get("Location"))
	}
	other := newBrowser()
	other.addr = "192.0.2.11:1"
	if r := d.send(other, http.MethodGet, "/ui/signin", nil); !strings.HasPrefix(r.Header().Get("Location"), "https://ha.example.org/") {
		t.Errorf("another address is refused: %s", r.Header().Get("Location"))
	}
}

func TestDirectServesTheUIAndNothingElse(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	if r := d.send(b, http.MethodGet, "/ui/", nil); r.Code != http.StatusOK || r.Body.String() != "direct ui" {
		t.Errorf("/ui/ = %d %q", r.Code, r.Body)
	}
	if r := d.send(b, http.MethodGet, "/ui", nil); r.Code != http.StatusMovedPermanently || r.Header().Get("Location") != "/ui/" {
		t.Errorf("/ui = %d %s", r.Code, r.Header().Get("Location"))
	}
	for _, p := range []string{"/", "/api/session", "/uix/", "/ui/api/unknown"} {
		r := d.send(b, http.MethodGet, p, nil)
		if r.Code == http.StatusOK {
			t.Errorf("%s = 200", p)
		}
	}
	if r := d.send(b, http.MethodPost, "/ui/signin", nil); r.Code == http.StatusSeeOther {
		t.Error("a sign-in started with POST")
	}
}

func TestDirectEventStream(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	d.signInWith(b, "code-admin")
	oldCheck := adminRecheck
	adminRecheck = 20 * time.Millisecond
	defer func() { adminRecheck = oldCheck }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = b.addr
		d.d.ServeHTTP(w, r)
	}))
	defer srv.Close()
	dial := func(origin string, extra http.Header) (*websocket.Conn, *http.Response, error) {
		h := http.Header{"Cookie": {uiCookie + "=" + b.cookies[uiCookie].Value}}
		if origin != "" {
			h.Set("Origin", origin)
		}
		for k, v := range extra {
			h[k] = v
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ui/api/events", &websocket.DialOptions{HTTPHeader: h})
	}
	for name, tc := range map[string]struct {
		origin string
		extra  http.Header
	}{
		"no origin":                     {"", nil},
		"another origin":                {"https://evil.example.org", nil},
		"forwarded host of an attacker": {"https://evil.example.org", http.Header{"X-Forwarded-Host": {"evil.example.org"}}},
		"public URL but another site":   {publicURL, http.Header{"Sec-Fetch-Site": {"cross-site"}}},
	} {
		if conn, resp, err := dial(tc.origin, tc.extra); err == nil {
			conn.CloseNow()
			t.Errorf("%s: stream opened", name)
		} else if resp != nil && resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
	}

	conn, _, err := dial(publicURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"csrf":"`+d.srv.csrfToken(adminID, d.now.Now())+`"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("first event: %v", err)
	}
	// Signing out closes the stream with 4401.
	csrf := d.srv.csrfToken(adminID, d.now.Now())
	if r := d.send(b, http.MethodPost, "/ui/signout", func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("X-HM-CSRF", csrf)
	}); r.Code != http.StatusNoContent {
		t.Fatalf("sign-out = %d", r.Code)
	}
	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != closeSignedOut {
			t.Errorf("closed with %v, want %d", err, closeSignedOut)
		}
		break
	}
}

func TestDirectEventStreamEndsWithTheSession(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	d.signInWith(b, "code-admin")
	oldCheck := adminRecheck
	adminRecheck = 20 * time.Millisecond
	defer func() { adminRecheck = oldCheck }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = b.addr
		d.d.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ui/api/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {uiCookie + "=" + b.cookies[uiCookie].Value}, "Origin": {publicURL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"csrf":"`+d.srv.csrfToken(adminID, d.now.Now())+`"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("first event: %v", err)
	}
	// An open stream does not count as use: when the session is idle, the stream ends.
	d.now.Add(uiIdle + time.Second)
	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != closeSignedOut {
			t.Errorf("closed with %v, want %d", err, closeSignedOut)
		}
		break
	}
}

// The MCP listener has read and write timeouts; the event stream outlives them (net/http
// clears the deadlines when the connection is hijacked).
func TestDirectEventStreamOutlivesServerTimeouts(t *testing.T) {
	d := newDirect(t)
	b := newBrowser()
	d.signInWith(b, "code-admin")
	oldBeat := heartbeat
	heartbeat = 50 * time.Millisecond
	defer func() { heartbeat = oldBeat }()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = b.addr
		d.d.ServeHTTP(w, r)
	}))
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 100*time.Millisecond, 100*time.Millisecond
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ui/api/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {uiCookie + "=" + b.cookies[uiCookie].Value}, "Origin": {publicURL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"csrf":"`+d.srv.csrfToken(adminID, d.now.Now())+`"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatalf("stream ended after the server's timeouts: %v", err)
		}
	}
}

func TestDirectSignInWhenFull(t *testing.T) {
	d := newDirect(t)
	for i := range signInTotal {
		if _, err := d.srv.ui.startSignIn("198.51.100." + string(rune('0'+i%10)) + string(rune('0'+i/10))); err != nil {
			t.Fatal(err)
		}
	}
	if r := d.send(newBrowser(), http.MethodGet, "/ui/signin", nil); r.Header().Get("Location") != signInBusy {
		t.Errorf("start with all sign-ins in progress sent to %s", r.Header().Get("Location"))
	}

	d = newDirect(t)
	b := newBrowser()
	start := d.send(b, http.MethodGet, "/ui/signin", nil)
	to, _ := url.Parse(start.Header().Get("Location"))
	for i := range uiTotal {
		if _, err := d.srv.ui.create("user" + strings.Repeat("x", i)); err != nil {
			t.Fatal(err)
		}
	}
	end := d.send(b, http.MethodGet, "/ui/signin/callback?code=code-admin&state="+url.QueryEscape(to.Query().Get("state")), nil)
	if end.Header().Get("Location") != signInBusy || b.cookies[uiCookie] != nil {
		t.Errorf("callback with all sessions taken = %s", end.Header().Get("Location"))
	}
}
