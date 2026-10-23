// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// The bell points to where the UI really is (issue #48): the sidebar of the app, the
// link of the request's page in direct mode, or the UI without an address.
func TestBellText(t *testing.T) {
	const link = "https://hm.example.org:8765/ui/requests/0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		lang i18n.Lang
		app  bool
		link string
		want string
	}{
		{"app, de", i18n.DE, true, "", "Eine Rückfrage wartet auf Antwort. Öffne Home-Mandate in der Seitenleiste."},
		{"app, en", i18n.EN, true, "", "An approval request is waiting for an answer. Open Home-Mandate in the sidebar."},
		{"app with link, de", i18n.DE, true, "/app/local_home_mandate/requests/0123456789abcdef0123456789abcdef",
			"Eine Rückfrage wartet auf Antwort: [in Home-Mandate öffnen](/app/local_home_mandate/requests/0123456789abcdef0123456789abcdef)."},
		{"app with link, en", i18n.EN, true, "/app/local_home_mandate/requests/0123456789abcdef0123456789abcdef",
			"An approval request is waiting for an answer: [open it in Home-Mandate](/app/local_home_mandate/requests/0123456789abcdef0123456789abcdef)."},
		{"app with a link that is no path", i18n.EN, true, "https://evil.example/x", "An approval request is waiting for an answer. Open Home-Mandate in the sidebar."},
		{"app with another host", i18n.EN, true, "//evil.example/x", "An approval request is waiting for an answer. Open Home-Mandate in the sidebar."},
		{"app with a fragment", i18n.EN, true, "/app/x#y", "An approval request is waiting for an answer. Open Home-Mandate in the sidebar."},
		{"app with a parenthesis", i18n.EN, true, "/app/x)(y", "An approval request is waiting for an answer. Open Home-Mandate in the sidebar."},
		{"container with link, de", i18n.DE, false, link,
			"Eine Rückfrage wartet auf Antwort. Beantworte sie in der Oberfläche von Home-Mandate: [Rückfrage öffnen](" + link + ")"},
		{"container with link, en", i18n.EN, false, link,
			"An approval request is waiting for an answer. Answer it in the Home-Mandate UI: [Open the request](" + link + ")"},
		{"container without link, de", i18n.DE, false, "", "Eine Rückfrage wartet auf Antwort. Öffne die Oberfläche von Home-Mandate."},
		{"container without link, en", i18n.EN, false, "", "An approval request is waiting for an answer. Open the Home-Mandate UI."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bellText(c.lang, c.app, c.link); got != c.want {
				t.Errorf("bellText = %q, want %q", got, c.want)
			}
		})
	}
}

// An address that could break out of the Markdown link is left out, not escaped: the
// hint then names the UI without a link.
func TestBellTextRefusesUnsafeLinks(t *testing.T) {
	plain := bellText(i18n.EN, false, "")
	for _, link := range []string{
		"https://hm.example.org/ui/)[x](https://evil.example",
		"https://hm.example.org/ui/ x",
		"https://hm.example.org/ui/<x>",
		"https://hm.example.org/ui/[x]",
		"https://hm.example.org/ui/\nx",
		"https://hm.example.org/ui/\\",
		"https://hm.example.org/ui/?q=1",
		"https://hm.example.org/ui/#x",
		"https://user@hm.example.org/ui/",
		"javascript:alert(1)",
		"ftp://hm.example.org/ui/",
		"/ui/",
		"https:///ui/",
	} {
		if got := bellText(i18n.EN, false, link); got != plain {
			t.Errorf("link %q: %q", link, got)
		}
	}
	if got := bellText(i18n.EN, false, "http://localhost:8765/ui/"); !strings.Contains(got, "(http://localhost:8765/ui/)") {
		t.Errorf("loopback link: %q", got)
	}
}

// The bell rings with the text for the mode and the link of the request's page, and
// never with anything else about the request: no bell ID, tag or nonce (issue #53).
func TestBellMessageByMode(t *testing.T) {
	const link = "https://hm.example.org/ui/requests/"
	cases := []struct {
		name string
		app  bool
		link string
	}{
		{"app", true, ""},
		{"app, panel", true, "/app/local_home_mandate/requests/"},
		{"container, direct mode", false, link},
		{"container, no direct mode", false, ""},
	}
	for _, c := range cases {
		for _, lang := range i18n.Supported {
			t.Run(c.name+", "+string(lang), func(t *testing.T) {
				e := newChannelEnv(t, time.Minute)
				e.bellOn = true
				e.svc.cfg.Language = func() i18n.Lang { return lang }
				e.svc.cfg.App = c.app
				e.svc.cfg.RequestAddress = func(id string) string {
					if c.link == "" {
						return ""
					}
					return c.link + id
				}
				e.put(t, Approver{UserID: u2, Devices: phones("mobile_app_anna"), UI: true})
				e.admins.put(u2, true)
				ch := e.ask(request())
				id := e.waitOpen(t, 1)[0].ID
				push := e.notifier.next(t)
				nonce := nonceOf(t, push)
				e.svc.CancelAll()
				wait(t, ch)
				rung, _ := e.bell.state()
				if len(rung) != 1 {
					t.Fatalf("rung %+v", rung)
				}
				want := bellText(lang, c.app, "")
				if c.link != "" {
					want = bellText(lang, c.app, c.link+id)
				}
				if rung[0].n.Message != want {
					t.Errorf("message %q, want %q", rung[0].n.Message, want)
				}
				text := rung[0].n.Title + " " + rung[0].n.Message
				if c.link != "" && !strings.Contains(text, c.link+id) {
					t.Errorf("bell text %q does not link the request's page", text)
				}
				for _, secret := range []string{rung[0].id, push.n.Tag, nonce} {
					if secret == "" || strings.Contains(text, secret) {
						t.Errorf("bell text %q contains or misses %q", text, secret)
					}
				}
				if len(rung[0].n.Actions) != 0 {
					t.Errorf("bell has buttons: %+v", rung[0].n.Actions)
				}
			})
		}
	}
}

// Without the hook the bell names the UI without a link.
func TestBellWithoutRequestAddress(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.bellOn = true
	e.put(t, Approver{UserID: u2, Devices: phones("mobile_app_anna"), UI: true})
	e.admins.put(u2, true)
	ch := e.ask(request())
	e.waitOpen(t, 1)
	e.svc.CancelAll()
	wait(t, ch)
	if rung, _ := e.bell.state(); len(rung) != 1 || rung[0].n.Message != bellText(i18n.EN, false, "") {
		t.Fatalf("rung %+v", rung)
	}
}
