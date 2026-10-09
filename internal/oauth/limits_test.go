// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
)

// raise lifts the limits for tests that sign in many times from one sender on purpose.
func (l *rateLimit) raise(perSender, global int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.perSender, l.global = perSender, global
}

// One IPv6 network (/64) is one sender, so that an attacker cannot rotate addresses
// within it; an IPv4 address is a sender of its own.
func TestSenderKey(t *testing.T) {
	for remote, want := range map[string]string{
		"192.0.2.1:1234":                 "192.0.2.1",
		"198.51.100.7:0":                 "198.51.100.7",
		"[::ffff:192.0.2.1]:443":         "192.0.2.1",
		"[2001:db8:1:2:3:4:5:6]:443":     "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff::1]:1":       "2001:db8:1:2::/64",
		"[2001:db8:1:3::1]:1":            "2001:db8:1:3::/64",
		"[fe80::1%eth0]:443":             "fe80::/64",
		"not-an-address":                 "not-an-address",
		"[2001:db8::1]":                  "[2001:db8::1]",
		"[2001:db8:0:0:1::1]:0":          "2001:db8::/64",
		"192.0.2.1":                      "192.0.2.1",
		"2001:db8:5:6::1":                "2001:db8:5:6::/64",
		"[::1]:443":                      "::/64",
		"[2001:db8:ab:cd:1:2:3:4%x]:443": "2001:db8:ab:cd::/64",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if got := senderKey(r); got != want {
			t.Errorf("senderKey(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	l := newRateLimit(3, 5)
	for i := range 3 {
		if ok, _ := l.allow("a", now); !ok {
			t.Fatalf("request %d of a refused", i+1)
		}
	}
	if ok, retry := l.allow("a", now.Add(10*time.Second)); ok || retry != 50*time.Second {
		t.Errorf("a over its limit: %v, retry after %v", ok, retry)
	}
	for i := range 2 {
		if ok, _ := l.allow(fmt.Sprint("b", i), now); !ok {
			t.Errorf("other sender %d refused", i)
		}
	}
	if ok, _ := l.allow("c", now); ok {
		t.Error("request over the global limit allowed")
	}
	if len(l.bySender) > 5 {
		t.Errorf("%d senders remembered", len(l.bySender))
	}
	if ok, _ := l.allow("a", now.Add(rateWindow)); !ok {
		t.Error("a refused in the next window")
	}
}

// Every unauthenticated endpoint is limited per sender: the request over the limit gets
// 429 with Retry-After, a page for the browser and an OAuth error for the agent.
func TestUnauthenticatedEndpointsAreRateLimited(t *testing.T) {
	_, challenge := pkce()
	form := url.Values{"client_id": {"n8n-kitchen"}}
	for name, tc := range map[string]struct {
		limit int
		send  func(b *browser) response
		json  bool
	}{
		"authorize":     {onboardingPerSender, func(b *browser) response { return b.get(authorizeQuery(challenge, nil)) }, false},
		"callback":      {onboardingPerSender, func(b *browser) response { return b.get(CallbackPath + "?code=x&state=y") }, false},
		"consent page":  {onboardingPerSender, func(b *browser) response { return b.get(ConsentPath) }, false},
		"consent":       {onboardingPerSender, func(b *browser) response { return b.post(ConsentPath, url.Values{"csrf": {"x"}}) }, false},
		"pair page":     {onboardingPerSender, func(b *browser) response { return b.get(PairPath) }, false},
		"pair":          {onboardingPerSender, func(b *browser) response { return b.post(PairPath, url.Values{"code": {"x"}}) }, false},
		"device":        {onboardingPerSender, func(b *browser) response { return b.post(DevicePath, form) }, true},
		"token":         {tokenPerSender, func(b *browser) response { return b.post(TokenPath, url.Values{"grant_type": {"x"}}) }, true},
		"token refresh": {tokenPerSender, func(b *browser) response { return b.post(TokenPath, url.Values{"grant_type": {"refresh_token"}}) }, true},
	} {
		h := newHarness(t)
		b := h.browser()
		for i := range tc.limit {
			if res := tc.send(b); res.status == http.StatusTooManyRequests {
				t.Fatalf("%s: request %d refused", name, i+1)
			}
		}
		res := tc.send(b)
		if res.status != http.StatusTooManyRequests || res.header.Get("Retry-After") != "60" {
			t.Errorf("%s: %d, Retry-After %q", name, res.status, res.header.Get("Retry-After"))
		}
		var out map[string]string
		if isJSON := json.Unmarshal([]byte(res.body), &out) == nil; isJSON != tc.json ||
			tc.json && out["error"] != "temporarily_unavailable" || !tc.json && !strings.Contains(res.body, "<html") {
			t.Errorf("%s: body %q", name, res.body)
		}
		// Static resources and metadata are not limited.
		if res := b.get(stylePath); res.status != http.StatusOK {
			t.Errorf("%s: style sheet %d", name, res.status)
		}
		h.clock.Add(rateWindow)
		if res := tc.send(b); res.status == http.StatusTooManyRequests {
			t.Errorf("%s: refused in the next window", name)
		}
	}
}

// Many senders together are limited as well, and the browser endpoints and the token
// endpoint count separately: a flood of sign-ins does not stop agents from refreshing.
func TestRateLimitAcrossSenders(t *testing.T) {
	h := newHarness(t)
	senders := 0
	h.server.clientAddr = func(*http.Request) string { senders++; return fmt.Sprint("sender-", senders) }
	for range onboardingGlobal {
		h.browser().get(PairPath)
	}
	if res := h.browser().get(PairPath); res.status != http.StatusTooManyRequests {
		t.Errorf("over the global limit: %d", res.status)
	}
	if status, out, _ := h.tokenRequest(url.Values{"grant_type": {"x"}}); status != http.StatusBadRequest {
		t.Errorf("token endpoint during a flood of sign-ins: %d %v", status, out)
	}
}

// A bare GET /pair keeps no state: the sign-in is bound to the browser by its cookie and
// a signed state, and a session exists only after Home Assistant signed in an
// administrator.
func TestPairPageKeepsNoStateBeforeTheSignIn(t *testing.T) {
	h := newHarness(t)
	senders := 0
	h.server.clientAddr = func(*http.Request) string { senders++; return fmt.Sprint("sender-", senders) }
	for range maxSessions + 1 {
		if res := h.browser().get(PairPath); res.status != http.StatusFound {
			t.Fatalf("pair page = %d", res.status)
		}
	}
	if n := len(h.server.sessions.byID); n != 0 {
		t.Errorf("%d sessions after bare GET /pair", n)
	}
	// The state is bound to the browser's cookie and used up by a failed attempt.
	b := h.browser()
	toHA := b.get(PairPath)
	other := h.browser()
	if res := other.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("state of another browser: %d", res.status)
	}
	if res := b.get(CallbackPath + "?code=admin-code&state=wrong"); res.status != http.StatusBadRequest {
		t.Errorf("wrong state: %d", res.status)
	}
	if res := b.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("right state after a wrong one: %d", res.status)
	}
	// It expires with the session time.
	b = h.browser()
	toHA = b.get(PairPath)
	h.clock.Add(sessionTTL)
	if res := b.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("expired state: %d", res.status)
	}
	// A state of another server (restart) is worthless.
	b = h.browser()
	toHA = b.get(PairPath)
	h.server.pairKey = newPairKey()
	if res := b.signIn(toHA, "admin-code"); res.status != http.StatusBadRequest {
		t.Errorf("state of another key: %d", res.status)
	}
	if n := len(h.server.sessions.byID); n != 0 {
		t.Errorf("%d sessions after failed sign-ins", n)
	}
}

// rejectingTokens answers every refresh as a reused token.
type rejectingTokens struct{}

func (rejectingTokens) Refresh(context.Context, string, string, string) (agent.TokenPair, error) {
	return agent.TokenPair{}, agent.ErrRefreshReused
}

// syncBuffer is a log destination safe for concurrent handlers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Warnings that anyone can cause are logged at most once a minute per kind.
func TestRefusalWarningsAreThrottled(t *testing.T) {
	h := newHarness(t)
	var log syncBuffer
	h.server.cfg.Logger = slog.New(slog.NewTextHandler(&log, nil))
	_, challenge := pkce()
	bad := authorizeQuery(challenge, func(q url.Values) { q.Set("client_id", "https://evil.example.org/c.json") })
	b := h.browser()
	for range 3 {
		b.get(bad)
		b.signIn(b.get(PairPath), "bad-code")
		h.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}, "client_id": {testClient}})
	}
	h.server.cfg.Tokens = rejectingTokens{}
	for range 3 {
		h.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}, "client_id": {testClient}})
	}
	for msg, want := range map[string]int{"authorization request refused": 1, "sign-in through Home Assistant failed": 1,
		"refresh token reused": 1} {
		if n := strings.Count(log.String(), `msg="`+msg); n != want {
			t.Errorf("%q logged %d times, want %d\n%s", msg, n, want, log.String())
		}
	}
	h.clock.Add(time.Minute)
	b.get(bad)
	if n := strings.Count(log.String(), "authorization request refused"); n != 2 {
		t.Errorf("after a minute: logged %d times", n)
	}
}
