// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// haUser is a test user of Home Assistant (TESTING.md §3).
type haUser struct {
	name, password string
	admin          bool
	id             string
	token          string // access token, to fire answers as this user
}

// The test users of TESTING.md §3.
const (
	adminApprover = "admin-approver"
	adminOther    = "admin-other"
	userPlain     = "user-plain"
)

// haConn is a WebSocket connection to Home Assistant for test set-up and observation.
type haConn struct {
	conn *websocket.Conn
	id   int
}

func dialHA(ctx context.Context, token string) (*haConn, error) {
	wsURL := "wss" + strings.TrimPrefix(env.haURL, "https") + "/api/websocket"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: httpClient()})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(16 << 20)
	c := &haConn{conn: conn}
	if _, err := c.read(ctx); err != nil { // auth_required
		conn.CloseNow()
		return nil, err
	}
	if err := c.write(ctx, map[string]any{"type": "auth", "access_token": token}); err != nil {
		conn.CloseNow()
		return nil, err
	}
	if m, err := c.read(ctx); err != nil || m["type"] != "auth_ok" {
		conn.CloseNow()
		return nil, fmt.Errorf("auth: %v %v", m["type"], err)
	}
	return c, nil
}

func (c *haConn) read(ctx context.Context) (map[string]any, error) {
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(data, &m)
}

func (c *haConn) write(ctx context.Context, v any) error {
	data, _ := json.Marshal(v)
	return c.conn.Write(ctx, websocket.MessageText, data)
}

// command sends one command and returns its result.
func (c *haConn) command(ctx context.Context, cmd map[string]any) (any, error) {
	c.id++
	cmd["id"] = c.id
	if err := c.write(ctx, cmd); err != nil {
		return nil, err
	}
	for {
		m, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if m["type"] != "result" || int(m["id"].(float64)) != c.id {
			continue
		}
		if m["success"] != true {
			return nil, fmt.Errorf("%s: %v", cmd["type"], m["error"])
		}
		return m["result"], nil
	}
}

// createUsers adds admin-approver, admin-other and user-plain with access tokens.
func createUsers() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := dialHA(ctx, env.haToken)
	if err != nil {
		return err
	}
	defer c.conn.CloseNow()
	env.users = map[string]*haUser{}
	for _, u := range []*haUser{{name: adminApprover, admin: true}, {name: adminOther, admin: true}, {name: userPlain}} {
		group := "system-users"
		if u.admin {
			group = "system-admin"
		}
		res, err := c.command(ctx, map[string]any{"type": "config/auth/create", "name": u.name, "group_ids": []string{group}})
		if err != nil {
			return err
		}
		u.id = res.(map[string]any)["user"].(map[string]any)["id"].(string)
		u.password = "e2e-" + env.id + "-" + u.name
		if _, err := c.command(ctx, map[string]any{"type": "config/auth_provider/homeassistant/create",
			"user_id": u.id, "username": u.name, "password": u.password}); err != nil {
			return err
		}
		code, err := haLogin(env.haURL+"/", env.haURL+"/?auth_callback=1", u)
		if err != nil {
			return err
		}
		if u.token, err = exchangeHACode(code, env.haURL+"/"); err != nil {
			return err
		}
		env.secrets = append(env.secrets, u.password, u.token)
		env.users[u.name] = u
	}
	return nil
}

// haLogin signs u in through Home Assistant's login flow, as its sign-in page does, and
// returns the authorization code for clientID.
func haLogin(clientID, redirectURI string, u *haUser) (string, error) {
	post := func(path string, body map[string]any) (map[string]any, error) {
		data, _ := json.Marshal(body)
		resp, err := httpClient().Post(env.haURL+path, "application/json", bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("%s: status %d: %w", path, resp.StatusCode, err)
		}
		return out, nil
	}
	flow, err := post("/auth/login_flow", map[string]any{"client_id": clientID, "handler": []any{"homeassistant", nil}, "redirect_uri": redirectURI})
	if err != nil {
		return "", err
	}
	flowID, _ := flow["flow_id"].(string)
	if flowID == "" {
		return "", fmt.Errorf("login flow: %v", flow)
	}
	res, err := post("/auth/login_flow/"+flowID, map[string]any{"username": u.name, "password": u.password, "client_id": clientID})
	if err != nil {
		return "", err
	}
	code, _ := res["result"].(string)
	if res["type"] != "create_entry" || code == "" {
		return "", fmt.Errorf("login flow: %v", res)
	}
	return code, nil
}

func exchangeHACode(code, clientID string) (string, error) {
	resp, err := httpClient().PostForm(env.haURL+"/auth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil || tokens.AccessToken == "" {
		return "", fmt.Errorf("token: status %d, %v", resp.StatusCode, err)
	}
	return tokens.AccessToken, nil
}

// notification is a notify service call seen in Home Assistant.
type notification struct {
	Service string
	Title   string
	Message string
	Actions []string
}

// watchNotifications reports every notify service call from now on.
func watchNotifications(t *testing.T) <-chan notification {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := dialHA(ctx, env.haToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		c.conn.CloseNow()
	})
	if _, err := c.command(ctx, map[string]any{"type": "subscribe_events", "event_type": "call_service"}); err != nil {
		t.Fatal(err)
	}
	ch := make(chan notification, 16)
	go func() {
		for {
			m, err := c.read(ctx)
			if err != nil {
				return
			}
			var ev struct {
				Event struct {
					Data struct {
						Domain      string `json:"domain"`
						Service     string `json:"service"`
						ServiceData struct {
							Title   string `json:"title"`
							Message string `json:"message"`
							Data    struct {
								Actions []struct {
									Action string `json:"action"`
								} `json:"actions"`
							} `json:"data"`
						} `json:"service_data"`
					} `json:"data"`
				} `json:"event"`
			}
			raw, _ := json.Marshal(m)
			if json.Unmarshal(raw, &ev) != nil || ev.Event.Data.Domain != "notify" {
				continue
			}
			n := notification{Service: ev.Event.Data.Service, Title: ev.Event.Data.ServiceData.Title, Message: ev.Event.Data.ServiceData.Message}
			for _, a := range ev.Event.Data.ServiceData.Data.Actions {
				n.Actions = append(n.Actions, a.Action)
			}
			ch <- n
		}
	}()
	return ch
}

func nextNotification(t *testing.T, ch <-chan notification) notification {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(30 * time.Second):
		t.Fatal("no notification")
		return notification{}
	}
}

// answer fires mobile_app_notification_action as user, the way the companion app does:
// Home Assistant sets context.user_id to the user of the token.
func answer(t *testing.T, user, action string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"action": action})
	req, _ := http.NewRequest(http.MethodPost, env.haURL+"/api/events/mobile_app_notification_action", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.users[user].token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("firing the answer as %s: status %d", user, resp.StatusCode)
	}
}
