// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mandatespec "github.com/mandate-spec/mandate-spec"
)

// uiClient is a person signed in through the Ingress stand-in (Home Assistant's real
// login flow), using the UI's API as the browser does.
type uiClient struct {
	t    *testing.T
	c    *http.Client
	user string
	csrf string
}

func uiLogin(t *testing.T, user string) *uiClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	u := env.users[user]
	resp, err := c.PostForm(env.uiURL+"/login", url.Values{"username": {u.name}, "password": {u.password}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in of %s through Ingress = %d", user, resp.StatusCode)
	}
	ui := &uiClient{t: t, c: c, user: user}
	if status, body := ui.do(http.MethodGet, "api/session", nil); status == http.StatusOK {
		var s struct {
			CSRF string `json:"csrf_token"`
		}
		_ = json.Unmarshal(body, &s)
		ui.csrf = s.CSRF
		env.secrets = append(env.secrets, s.CSRF)
	}
	return ui
}

// do sends a request of the page: same origin, with the CSRF token on writes.
func (u *uiClient) do(method, path string, body any) (int, []byte) {
	u.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, env.uiURL+env.uiPath+"/"+path, rd)
	req.Header.Set("Accept", "application/json")
	if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("X-HM-CSRF", u.csrf)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func (u *uiClient) ok(method, path string, body, out any) {
	u.t.Helper()
	status, data := u.do(method, path, body)
	if status != http.StatusOK && status != http.StatusNoContent {
		u.t.Fatalf("%s %s = %d %s", method, path, status, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			u.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

// Negative catalog, UI: the gateway's UI listener answers nobody but the Supervisor, even
// with forged Ingress headers; a request through Ingress with forged headers is the
// signed-in person's own.
func TestUIOnlyThroughIngress(t *testing.T) {
	for _, path := range []string{"/api/session", "/", "/api/events"} {
		req, _ := http.NewRequest(http.MethodGet, env.uiDirect+path, nil)
		req.Header.Set("X-Remote-User-Id", env.users[adminApprover].id)
		req.Header.Set("X-Forwarded-For", "172.30.32.2")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("direct %s = %d", path, resp.StatusCode)
		}
	}
	plain := uiLogin(t, userPlain)
	if status, body := plain.do(http.MethodGet, "api/session", nil); status != http.StatusForbidden || !strings.Contains(string(body), "forbidden") {
		t.Errorf("user-plain = %d %s", status, body)
	}
	// user-plain claims to be the administrator: the stand-in, like the Supervisor, replaces it.
	req, _ := http.NewRequest(http.MethodGet, env.uiURL+env.uiPath+"/api/session", nil)
	req.Header.Set("X-Remote-User-Id", env.users[adminApprover].id)
	resp, err := plain.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("forged user header = %d", resp.StatusCode)
	}
	admin := uiLogin(t, adminApprover)
	var s struct {
		User struct{ ID, Name string } `json:"user"`
	}
	admin.ok(http.MethodGet, "api/session", nil, &s)
	if s.User.ID != env.users[adminApprover].id {
		t.Errorf("session = %+v", s)
	}
	// A write without the CSRF token.
	admin.csrf = ""
	if status, _ := admin.do(http.MethodPut, "api/session/language", map[string]any{"language": "de"}); status != http.StatusForbidden {
		t.Errorf("write without CSRF = %d", status)
	}
	// The UI itself, with its CSP, under the Ingress path.
	page, err := admin.c.Get(env.uiURL + env.uiPath + "/")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(page.Header.Get("Content-Security-Policy"), "default-src 'none'") ||
		!strings.HasPrefix(page.Header.Get("Content-Type"), "text/html") {
		t.Errorf("UI page = %d %v", page.StatusCode, page.Header)
	}
}

// Scenario 6 through the UI: revoking an agent locks its token out at once.
func TestUIScenario06Revoke(t *testing.T) {
	token := newAgent(t, "UI revoke", nil)
	ui := uiLogin(t, adminApprover)
	var agents []struct {
		ClientID    string `json:"client_id"`
		DisplayName string `json:"display_name"`
		Status      string `json:"status"`
	}
	ui.ok(http.MethodGet, "api/agents", nil, &agents)
	id := ""
	for _, a := range agents {
		if a.DisplayName == "UI revoke" {
			id = a.ClientID
		}
	}
	if id == "" || statusWith(t, token) != http.StatusOK {
		t.Fatalf("agent %q before revocation", id)
	}
	var revoked struct {
		Status  string `json:"status"`
		Mandate struct {
			Status string `json:"status"`
		} `json:"mandate"`
	}
	ui.ok(http.MethodPost, "api/agents/revoke", map[string]any{"client_id": id}, &revoked)
	if revoked.Status != "revoked" || revoked.Mandate.Status != "revoked" {
		t.Errorf("revoked = %+v", revoked)
	}
	if status := statusWith(t, token); status != http.StatusUnauthorized {
		t.Errorf("token after revocation in the UI: %d", status)
	}
}

// Scenario 9: a request outside the mandate's time window is denied. No test clock: the
// window is set from the household's real time (decision U5).
func TestUIScenario09TimeWindow(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(berlin)
	window := now.Add(2*time.Hour).Format("15:04") + "-" + now.Add(3*time.Hour).Format("15:04")
	s := session(t, newAgent(t, "Night only", func(d map[string]any) {
		for _, r := range d["rules"].([]any) {
			rule := r.(map[string]any)
			if res, _ := rule["resource"].(map[string]any); res["category"] == "light" {
				rule["conditions"] = map[string]any{"time_window": window}
			}
		}
	}))
	ready(t, s)
	before := haState(t, "light.bed_light")
	action := "turn_on"
	if before == "on" {
		action = "turn_off"
	}
	if _, errText := call(t, s, "perform_action", map[string]any{"entity_id": "light.bed_light", "action": action}); !strings.HasPrefix(errText, "denied") {
		t.Errorf("outside the window = %q", errText)
	}
	time.Sleep(time.Second)
	if after := haState(t, "light.bed_light"); after != before {
		t.Errorf("light changed from %s to %s", before, after)
	}
	ui := uiLogin(t, adminApprover)
	var page struct {
		Entries []map[string]any `json:"entries"`
	}
	ui.ok(http.MethodGet, "api/audit?group=decision&device=light.bed_light&limit=1", nil, &page)
	if len(page.Entries) != 1 || page.Entries[0]["evaluation"].(map[string]any)["reason"] != "no_match" {
		t.Errorf("audit entry = %v", page.Entries)
	}
}

// An approval answered in the UI (decision F2): admin-other has only the UI channel; the
// door moves only after the answer.
func TestUIApprovalAnswer(t *testing.T) {
	other := env.users[adminOther]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := dialHA(ctx, env.haToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.command(ctx, map[string]any{"type": "person/create", "name": "Other Admin", "user_id": other.id}); err != nil {
		t.Fatalf("person: %v", err)
	}
	ui := uiLogin(t, adminOther)
	ui.ok(http.MethodPut, "api/approvers/"+other.id, map[string]any{"devices": []any{}, "ui": true, "ui_critical": true, "language": nil}, nil)
	token := agentWithApprover(t, "UI approval", other.id)
	s := session(t, token)
	ready(t, s)
	// The mandate asks for unlock (r-locks): start from a locked door.
	entity := "lock.kitchen_door"
	haService(t, "lock", "lock", entity)
	eventually(t, "door locked", 15*time.Second, func() bool { return haState(t, entity) == "locked" })
	before := haState(t, entity)
	result := callAsync(t, s, "perform_action", map[string]any{"entity_id": entity, "action": "unlock", "reason": "UI test"})
	var open struct {
		Open []struct {
			ID        string `json:"id"`
			EntityID  string `json:"entity_id"`
			CanAnswer bool   `json:"can_answer"`
		} `json:"open"`
	}
	id := ""
	eventually(t, "request in the UI", 20*time.Second, func() bool {
		ui.ok(http.MethodGet, "api/approvals", nil, &open)
		for _, r := range open.Open {
			if r.EntityID == entity && r.CanAnswer {
				id = r.ID
			}
		}
		return id != ""
	})
	if haState(t, entity) != before {
		t.Fatal("the door moved before the answer")
	}
	var entry struct {
		Outcome string `json:"outcome"`
		Via     string `json:"via"`
	}
	ui.ok(http.MethodPost, "api/approvals/"+id+"/answer", map[string]any{"approve": true}, &entry)
	if entry.Outcome != "approved" || entry.Via != "ui" {
		t.Errorf("answer = %+v", entry)
	}
	if r := awaitResult(t, result); r.errText != "" {
		t.Errorf("perform_action = %q", r.errText)
	}
	eventually(t, "door moved", 15*time.Second, func() bool { return haState(t, entity) != before })
	if !hasLine(auditLog(t), `"entity_id":"`+entity+`"`, `"via":"ui"`, `"by":"`+other.id+`"`, `"status":"executed"`) {
		t.Error("no approved entry via the UI in the audit log")
	}
}

// haService calls a Home Assistant service directly (test set-up, not through Home-Mandate).
func haService(t *testing.T, domain, service, entityID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"entity_id": entityID})
	req, _ := http.NewRequest(http.MethodPost, env.haURL+"/api/services/"+domain+"/"+service, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.haToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s.%s = %d", domain, service, resp.StatusCode)
	}
}

// agentWithApprover admits an agent with the voice assistant mandate whose approvals go to
// approver.
func agentWithApprover(t *testing.T, name, approver string) string {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	settings := map[string]any{"timeout": "PT20S", "approvers": []any{approver}}
	doc["approval"] = settings
	for _, r := range doc["rules"].([]any) {
		if rule := r.(map[string]any); rule["approval"] != nil {
			rule["approval"] = settings
		}
	}
	template := "t-" + randomHex(4)
	mandate, _ := json.Marshal(doc)
	cli(t, string(mandate), "mandate", "template", "import", template, "-")
	return admit(t, name, template)
}

// Pairing in the UI (decision D5): code check, approval, the agent's poll gets its tokens.
func TestUIPairing(t *testing.T) {
	ui := uiLogin(t, adminApprover)
	p := requestPairing(t)
	var c struct {
		PairingID string `json:"pairing_id"`
		Client    string `json:"client"`
	}
	ui.ok(http.MethodPost, "api/pairing/check", map[string]any{"code": strings.ToLower(p.UserCode)}, &c)
	if c.Client != e2eClient || c.PairingID == "" {
		t.Fatalf("candidate = %+v", c)
	}
	var a struct {
		Status  string `json:"status"`
		Mandate struct {
			Name string `json:"name"`
		} `json:"mandate"`
	}
	ui.ok(http.MethodPost, "api/pairing/approve", map[string]any{"code": p.UserCode, "pairing_id": c.PairingID,
		"display_name": "Paired in the UI", "template": lastTemplate(t), "mandate_name": "UI-Mandat"}, &a)
	if a.Status != "active" || a.Mandate.Name != "UI-Mandat" {
		t.Errorf("admitted = %+v", a)
	}
	status, tokens := pollTokens(t, p)
	if status != http.StatusOK {
		t.Fatalf("poll = %d %v", status, tokens)
	}
	access, _ := tokens["access_token"].(string)
	refresh, _ := tokens["refresh_token"].(string)
	env.secrets = append(env.secrets, access, refresh, p.UserCode)
	if statusWith(t, access) != http.StatusOK {
		t.Error("token of the agent paired in the UI does not work")
	}
	// A wrong code: refused alike, counted.
	if status, body := ui.do(http.MethodPost, "api/pairing/check", map[string]any{"code": "BBBB-BBBB"}); status != http.StatusBadRequest ||
		!strings.Contains(string(body), "pairing_code_invalid") {
		t.Errorf("wrong code = %d %s", status, body)
	}
}

// The event stream through Ingress: the CSRF token as first message, then a system event.
func TestUIEventStream(t *testing.T) {
	ui := uiLogin(t, adminApprover)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(env.uiURL, "http") + env.uiPath + "/api/events"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: ui.c,
		HTTPHeader: http.Header{"Origin": {env.uiURL}, "Sec-Fetch-Site": {"same-origin"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	msg, _ := json.Marshal(map[string]string{"csrf": ui.csrf})
	if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || !strings.Contains(string(data), `"type":"system"`) || !strings.Contains(string(data), `"connected":true`) {
		t.Fatalf("first event = %s, %v", data, err)
	}
	// A search of the audit log announces nothing and its text never reaches the logs.
	ui.ok(http.MethodGet, "api/audit?q="+url.QueryEscape("Haus-Tür-Suche"), nil, nil)
	// A change made on the command line reaches the open UI (the tail reads the log).
	cli(t, "", "emergency-stop", "status")
	newAgent(t, "Seen live", nil)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("no agents.changed: %v", err)
		}
		if strings.Contains(string(data), `"type":"agents.changed"`) {
			break
		}
	}
}

// Scenario 11: after a restart, mandates, agents and the audit log are unchanged and the
// chain is valid.
func TestUIScenario11Restart(t *testing.T) {
	ui := uiLogin(t, adminApprover)
	snapshot := func() (string, string, int) {
		_, agents := ui.do(http.MethodGet, "api/agents", nil)
		_, mandates := ui.do(http.MethodGet, "api/mandates", nil)
		var page struct {
			Total int `json:"total"`
		}
		ui.ok(http.MethodGet, "api/audit?limit=1", nil, &page)
		return stripVolatile(string(agents)), string(mandates), page.Total
	}
	agents, mandates, total := snapshot()
	if _, err := run("restart", env.hm); err != nil {
		t.Fatal(err)
	}
	if err := waitHTTP(env.mcpURL, time.Minute); err != nil {
		t.Fatal(err)
	}
	ui = uiLogin(t, adminApprover) // a new process: a new CSRF key
	eventually(t, "UI back", time.Minute, func() bool { status, _ := ui.do(http.MethodGet, "api/system", nil); return status == http.StatusOK })
	ui = uiLogin(t, adminApprover)
	a2, m2, t2 := snapshot()
	if a2 != agents || m2 != mandates || t2 != total {
		t.Errorf("after restart: agents equal %v, mandates equal %v, entries %d → %d", a2 == agents, m2 == mandates, total, t2)
	}
	var v struct {
		Valid   bool `json:"valid"`
		Checked int  `json:"checked"`
	}
	ui.ok(http.MethodPost, "api/audit/verify", nil, &v)
	if !v.Valid || v.Checked != total {
		t.Errorf("verification = %+v (entries %d)", v, total)
	}
}

// stripVolatile removes the activity counters, which depend on the hour.
func stripVolatile(agents string) string {
	var list []map[string]any
	if json.Unmarshal([]byte(agents), &list) != nil {
		return agents
	}
	for _, a := range list {
		delete(a, "requests_today")
		delete(a, "actions_last_hour")
	}
	out, _ := json.Marshal(list)
	return string(out)
}

// The UI in a real browser against the gateway (Playwright, de and en), when asked for:
// make e2e-ui.
func TestUIInTheBrowser(t *testing.T) {
	if os.Getenv("E2E_PLAYWRIGHT") == "" {
		t.Skip("set E2E_PLAYWRIGHT=1 (make e2e-ui)")
	}
	u := env.users[adminApprover]
	cmd := exec.Command("pnpm", "exec", "playwright", "test", "-c", "playwright.live.config.ts")
	cmd.Dir = "../web"
	cmd.Env = append(os.Environ(), "HM_LIVE_URL="+env.uiURL, "HM_LIVE_PATH="+env.uiPath+"/", "HM_LIVE_USER="+u.name,
		"HM_LIVE_PASSWORD="+u.password, "HM_LIVE_PLAIN_USER="+env.users[userPlain].name,
		"HM_LIVE_PLAIN_PASSWORD="+env.users[userPlain].password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("playwright: %v\n%s", err, out)
	}
	fmt.Fprintf(os.Stderr, "%s", out)
}
