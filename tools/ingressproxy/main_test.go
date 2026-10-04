// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeHA answers the login flow, the token endpoint and auth/current_user for one user.
func fakeHA(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login_flow", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"flow_id": "f1"})
	})
	mux.HandleFunc("POST /auth/login_flow/f1", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["username"] == "anna" && in["password"] == "secret" {
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "create_entry", "result": "code-1"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "form", "errors": map[string]string{"base": "invalid_auth"}})
	})
	mux.HandleFunc("POST /auth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") != "code-1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1"})
	})
	mux.HandleFunc("/api/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth_required"}`))
		_, _, _ = conn.Read(ctx)
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth_ok"}`))
		_, _, _ = conn.Read(ctx)
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"type":"result","success":true,"result":{"id":"u-anna","name":"Anna"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// gateway echoes what reached it, and echoes WebSocket messages.
func gateway(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			typ, data, err := conn.Read(r.Context())
			if err == nil {
				_ = conn.Write(r.Context(), typ, append([]byte(r.Header.Get("X-Remote-User-Id")+":"), data...))
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path, "query": r.URL.RawQuery, "headers": r.Header})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setup(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	ha, gw := fakeHA(t), gateway(t)
	target, _ := url.Parse(gw.URL)
	p := newProxy(target, "tok", haLogin(http.DefaultClient, ha.URL))
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return srv, &http.Client{Jar: jar}
}

func TestSignInAndForward(t *testing.T) {
	srv, client := setup(t)
	if resp, _ := client.Get(srv.URL + "/api/hassio_ingress/tok/api/session"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("before sign-in = %d", resp.StatusCode)
	}
	if resp, _ := client.PostForm(srv.URL+"/login", url.Values{"username": {"anna"}, "password": {"wrong"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong password = %d", resp.StatusCode)
	}
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"username": {"anna"}, "password": {"secret"}})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in = %v %v", resp, err)
	}
	var who map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&who)
	if who["user_id"] != "u-anna" || who["ingress_path"] != "/api/hassio_ingress/tok/" {
		t.Errorf("sign-in answer = %v", who)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/hassio_ingress/tok/api/session?x=1", nil)
	req.Header.Set("X-Remote-User-Id", "u-admin") // forged by the client
	req.Header.Set("X-Ingress-Path", "/evil")
	req.Header.Set("X-Hass-Source", "core.ingress")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path, Query string
		Headers     http.Header
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Path != "/api/session" || got.Query != "x=1" || got.Headers.Get("X-Remote-User-Id") != "u-anna" ||
		len(got.Headers.Values("X-Remote-User-Id")) != 1 || got.Headers.Get("X-Remote-User-Name") != "Anna" ||
		got.Headers.Get("X-Ingress-Path") != "/api/hassio_ingress/tok" || got.Headers.Get("X-Hass-Source") != "" {
		t.Errorf("forwarded = %+v", got)
	}
	for _, path := range []string{"/api/hassio_ingress/tok", "/api/hassio_ingress/tok/"} {
		resp, _ := client.Get(srv.URL + path)
		var root struct{ Path string }
		_ = json.NewDecoder(resp.Body).Decode(&root)
		if root.Path != "/" {
			t.Errorf("%s forwarded as %q", path, root.Path)
		}
	}
	for _, path := range []string{"/api/hassio_ingress/other/", "/", "/api/hassio_ingress/tokX/"} {
		if resp, _ := client.Get(srv.URL + path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d", path, resp.StatusCode)
		}
	}
	// A forged session cookie.
	other := &http.Client{}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/hassio_ingress/tok/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "guess"})
	if resp, _ := other.Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged cookie = %d", resp.StatusCode)
	}
	// WebSockets go through.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/hassio_ingress/tok/api/events", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_ = conn.Write(ctx, websocket.MessageText, []byte("hi"))
	if _, data, err := conn.Read(ctx); err != nil || string(data) != "u-anna:hi" {
		t.Errorf("websocket = %q, %v", data, err)
	}
	if resp, _ := client.Post(srv.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader("%zz")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad form = %d", resp.StatusCode)
	}
}

// closedURL is an address where nothing listens (refused at once, unlike a filtered port).
func closedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

func TestLoginFailures(t *testing.T) {
	ctx := context.Background()
	if _, err := haLogin(http.DefaultClient, closedURL(t))(ctx, "a", "b"); err == nil {
		t.Error("unreachable Home Assistant")
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "not json") }))
	defer broken.Close()
	if _, err := haLogin(http.DefaultClient, broken.URL)(ctx, "a", "b"); err == nil {
		t.Error("broken answers")
	}
	// The token endpoint refuses the code; the WebSocket gives no user.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login_flow", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"flow_id":"f"}`) })
	mux.HandleFunc("POST /auth/login_flow/f", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"type":"create_entry","result":"c"}`)
	})
	mux.HandleFunc("POST /auth/token", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"error":"x"}`) })
	refuse := httptest.NewServer(mux)
	defer refuse.Close()
	if _, err := haLogin(http.DefaultClient, refuse.URL)(ctx, "a", "b"); err == nil {
		t.Error("refused code")
	}
	for name, script := range map[string][]string{
		"auth refused":  {`{"type":"auth_required"}`, `{"type":"auth_invalid"}`},
		"no user":       {`{"type":"auth_required"}`, `{"type":"auth_ok"}`, `{"id":1,"type":"result","success":true,"result":{}}`},
		"closed early":  {},
		"closed at end": {`{"type":"auth_required"}`, `{"type":"auth_ok"}`},
	} {
		ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for i, msg := range script {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
				if i < len(script)-1 || len(script) == 3 {
					_, _, _ = conn.Read(r.Context())
				}
			}
			if len(script) == 2 && script[1] == `{"type":"auth_ok"}` {
				_, _, _ = conn.Read(r.Context())
			}
		}))
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if _, err := currentUser(cctx, http.DefaultClient, ws.URL, "t"); err == nil {
			t.Errorf("%s: no error", name)
		}
		cancel()
		ws.Close()
	}
	if _, err := currentUser(ctx, http.DefaultClient, closedURL(t), "t"); err == nil {
		t.Error("unreachable WebSocket")
	}
}

func TestRunChecksItsArguments(t *testing.T) {
	if err := run(":0", "", "http://ha", "", "tok"); err == nil {
		t.Error("no target")
	}
	if err := run(":0", "http://gw:8099", "http://ha", "", ""); err == nil {
		t.Error("no token")
	}
	if err := run(":0", "http://gw:8099", "http://ha", filepath.Join(t.TempDir(), "none.pem"), "tok"); err == nil {
		t.Error("missing CA file")
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	_ = os.WriteFile(ca, []byte("not pem"), 0o600)
	err := run("256.0.0.1:0", "http://gw:8099", "http://ha", ca, "tok")
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("bad listen address = %v", err)
	}
}
