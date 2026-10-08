// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/spec"
)

// e2eClient is the client ID of the test agent: a free identifier, so the pairing code
// works without a client metadata document on the internet.
const e2eClient = "e2e-agent"

// hmBrowser is a human's browser on the Home-Mandate pages: it keeps cookies and does
// not follow redirects, so every step can be checked.
type hmBrowser struct {
	t *testing.T
	c *http.Client
}

type page struct {
	status   int
	body     string
	location string
}

func newBrowser(t *testing.T) *hmBrowser {
	jar, _ := cookiejar.New(nil)
	return &hmBrowser{t: t, c: &http.Client{Jar: jar, Timeout: 30 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: env.roots, MinVersion: tls.VersionTLS13}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *hmBrowser) do(req *http.Request) page {
	b.t.Helper()
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return page{status: resp.StatusCode, body: string(body), location: resp.Header.Get("Location")}
}

func (b *hmBrowser) get(path string) page {
	b.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.public+path, nil)
	return b.do(req)
}

func (b *hmBrowser) post(path string, form url.Values) page {
	b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, env.public+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", env.public)
	return b.do(req)
}

var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (b *hmBrowser) csrf(p page) string {
	b.t.Helper()
	m := csrfPattern.FindStringSubmatch(p.body)
	if m == nil {
		b.t.Fatalf("no CSRF token in page %d:\n%s", p.status, p.body)
	}
	return m[1]
}

// signIn follows the redirect to Home Assistant's sign-in as user, with the real login
// flow of Home Assistant, and returns Home-Mandate's answer to the callback.
func (b *hmBrowser) signIn(toHA page, user string) page {
	b.t.Helper()
	u, err := url.Parse(toHA.location)
	if toHA.status != http.StatusFound || err != nil || u.Path != "/auth/authorize" {
		b.t.Fatalf("expected the redirect to Home Assistant, got %d %q", toHA.status, toHA.location)
	}
	q := u.Query()
	code, err := haLogin(q.Get("client_id"), q.Get("redirect_uri"), env.users[user])
	if err != nil {
		b.t.Fatal(err)
	}
	return b.get("/oauth/ha/callback?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode())
}

type pairing struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	Interval   int    `json:"interval"`
}

func requestPairing(t *testing.T) pairing {
	t.Helper()
	resp, err := httpClient().PostForm(env.public+"/oauth/device_authorization", url.Values{"client_id": {e2eClient}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var p pairing
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p.DeviceCode == "" {
		t.Fatalf("device authorization: status %d, %v", resp.StatusCode, err)
	}
	env.secrets = append(env.secrets, p.DeviceCode)
	return p
}

// pollTokens asks the token endpoint once for the pairing.
func pollTokens(t *testing.T, p pairing) (int, map[string]any) {
	t.Helper()
	resp, err := httpClient().PostForm(env.public+"/oauth/token", url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {p.DeviceCode}, "client_id": {e2eClient}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// pairAs admits the pairing p as user with the template and returns the page after
// the decision.
func pairAs(t *testing.T, p pairing, user, name, template string) page {
	t.Helper()
	b := newBrowser(t)
	back := b.signIn(b.get("/pair"), user)
	if back.status != http.StatusSeeOther || back.location != "/pair" {
		return back
	}
	entry := b.get("/pair")
	res := b.post("/pair", url.Values{"csrf": {b.csrf(entry)}, "code": {p.UserCode}})
	if res.status != http.StatusSeeOther {
		t.Fatalf("code entry = %d\n%s", res.status, res.body)
	}
	consent := b.get("/oauth/consent")
	return b.post("/oauth/consent", url.Values{"csrf": {b.csrf(consent)}, "action": {"approve"}, "name": {name},
		"template": {choiceOf(consent, template)}})
}

// choiceOf is the value the consent page gives a template: its name and the digest of
// what it showed, as a browser sends it. A template the page does not offer keeps its
// bare name, which the gateway refuses.
func choiceOf(consent page, template string) string {
	m := regexp.MustCompile(`value="(` + regexp.QuoteMeta(template) + `@sha256:[0-9a-f]{64})"`).FindStringSubmatch(consent.body)
	if m == nil {
		return template
	}
	return m[1]
}

// admit pairs a new agent through the pairing code, admitted by admin-approver with
// template, and returns its access token.
func admit(t *testing.T, name, template string) string {
	t.Helper()
	p := requestPairing(t)
	if res := pairAs(t, p, adminApprover, name, template); res.status != http.StatusOK {
		t.Fatalf("admission = %d %q\n%s", res.status, res.location, res.body)
	}
	status, tokens := pollTokens(t, p)
	if status != http.StatusOK {
		t.Fatalf("token request = %d %v", status, tokens)
	}
	access, _ := tokens["access_token"].(string)
	refresh, _ := tokens["refresh_token"].(string)
	env.secrets = append(env.secrets, access, refresh, p.UserCode)
	return access
}

var approverOnce sync.Once

// newAgent admits an agent with the voice assistant mandate of the specification (edited by
// edit): admin-approver approves, within 10 seconds.
func newAgent(t *testing.T, name string, edit func(map[string]any)) string {
	t.Helper()
	approver := env.users[adminApprover].id
	approverOnce.Do(func() { cli(t, "", "approver", "add", approver, "persistent_notification") })
	data, err := fs.ReadFile(spec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	settings := map[string]any{"timeout": "PT10S", "approvers": []any{approver}}
	doc["approval"] = settings
	for _, r := range doc["rules"].([]any) {
		if rule := r.(map[string]any); rule["approval"] != nil {
			rule["approval"] = settings
		}
	}
	if edit != nil {
		edit(doc)
	}
	template := "t-" + randomHex(4)
	mandate, _ := json.Marshal(doc)
	cli(t, string(mandate), "mandate", "template", "import", template, "-")
	return admit(t, name, template)
}

// clientIDOf returns the client ID of the agent with display name from agent list.
func clientIDOf(t *testing.T, name string) string {
	t.Helper()
	for _, line := range strings.Split(cli(t, "", "agent", "list"), "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 && f[2] == name {
			return f[0]
		}
	}
	t.Fatalf("no agent %q", name)
	return ""
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
