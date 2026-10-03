// SPDX-License-Identifier: AGPL-3.0-or-later

// Command ingressproxy plays Home Assistant's Ingress for the end-to-end tests (it is no
// part of Home-Mandate): run with the address 172.30.32.2 in the test network, it signs
// people in through Home Assistant's real login flow and forwards their requests to the
// gateway's UI listener under /api/hassio_ingress/<token>/, the way Core and the
// Supervisor do: the prefix is removed, client copies of the X-Remote-User-* headers,
// X-Ingress-Path and X-Hass-Source are dropped and set from the session, WebSockets are
// passed through.
//
//	ingressproxy -listen :8080 -target http://gateway:8099 -ha https://homeassistant:8123 \
//	  -ca /certs/ca.pem -token <random>
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// strippedHeaders are set only by the proxy, never passed on from the client.
var strippedHeaders = []string{"X-Remote-User-Id", "X-Remote-User-Name", "X-Remote-User-Display-Name", "X-Ingress-Path", "X-Hass-Source"}

const cookieName = "ingress_session"

type user struct {
	ID, Name string
}

// loginFunc signs someone in to Home Assistant and returns who it is.
type loginFunc func(ctx context.Context, username, password string) (user, error)

type proxy struct {
	prefix string // /api/hassio_ingress/<token>
	login  loginFunc
	rp     *httputil.ReverseProxy

	mu       sync.Mutex
	sessions map[string]user
}

func newProxy(target *url.URL, token string, login loginFunc) *proxy {
	p := &proxy{prefix: "/api/hassio_ingress/" + token, login: login, sessions: map[string]user{}}
	p.rp = &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(pr.In.URL.Path, p.prefix), "/")
		pr.Out.URL.RawPath = ""
		for _, h := range strippedHeaders {
			pr.Out.Header.Del(h)
		}
		u, _ := pr.In.Context().Value(userKey{}).(user)
		pr.Out.Header.Set("X-Remote-User-Id", u.ID)
		pr.Out.Header.Set("X-Remote-User-Name", u.Name)
		pr.Out.Header.Set("X-Remote-User-Display-Name", u.Name)
		pr.Out.Header.Set("X-Ingress-Path", p.prefix)
		pr.SetXForwarded()
	}}
	return p
}

type userKey struct{}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/login" && r.Method == http.MethodPost:
		p.signIn(w, r)
	case r.URL.Path == p.prefix || strings.HasPrefix(r.URL.Path, p.prefix+"/"):
		c, err := r.Cookie(cookieName)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		p.mu.Lock()
		u, ok := p.sessions[c.Value]
		p.mu.Unlock()
		if !ok {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	default:
		http.NotFound(w, r)
	}
}

func (p *proxy) signIn(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u, err := p.login(r.Context(), r.PostForm.Get("username"), r.PostForm.Get("password"))
	if err != nil {
		log.Printf("sign-in failed: %v", err)
		http.Error(w, "sign-in failed", http.StatusForbidden)
		return
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	p.mu.Lock()
	p.sessions[id] = u
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"ingress_path": p.prefix + "/", "user_id": u.ID})
}

// haLogin signs in through Home Assistant's login flow, exchanges the code for an
// access token and asks auth/current_user who that is.
func haLogin(client *http.Client, haURL string) loginFunc {
	return func(ctx context.Context, username, password string) (user, error) {
		clientID := haURL + "/"
		post := func(path string, body map[string]any) (map[string]any, error) {
			data, _ := json.Marshal(body)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, haURL+path, bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
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
		flow, err := post("/auth/login_flow", map[string]any{"client_id": clientID, "handler": []any{"homeassistant", nil},
			"redirect_uri": haURL + "/?auth_callback=1"})
		if err != nil {
			return user{}, err
		}
		flowID, _ := flow["flow_id"].(string)
		res, err := post("/auth/login_flow/"+url.PathEscape(flowID), map[string]any{"username": username, "password": password, "client_id": clientID})
		if err != nil {
			return user{}, err
		}
		code, _ := res["result"].(string)
		if res["type"] != "create_entry" || code == "" {
			return user{}, errors.New("wrong user or password")
		}
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, haURL+"/auth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return user{}, err
		}
		var tokens struct {
			AccessToken string `json:"access_token"`
		}
		err = json.NewDecoder(resp.Body).Decode(&tokens)
		resp.Body.Close()
		if err != nil || tokens.AccessToken == "" {
			return user{}, fmt.Errorf("token: status %d: %v", resp.StatusCode, err)
		}
		return currentUser(ctx, client, haURL, tokens.AccessToken)
	}
}

func currentUser(ctx context.Context, client *http.Client, haURL, token string) (user, error) {
	wsURL := "ws" + strings.TrimPrefix(haURL, "http") + "/api/websocket"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return user{}, err
	}
	defer conn.CloseNow()
	read := func() (map[string]any, error) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(data, &m)
	}
	write := func(v any) error {
		data, _ := json.Marshal(v)
		return conn.Write(ctx, websocket.MessageText, data)
	}
	if _, err := read(); err != nil {
		return user{}, err
	}
	if err := write(map[string]any{"type": "auth", "access_token": token}); err != nil {
		return user{}, err
	}
	if m, err := read(); err != nil || m["type"] != "auth_ok" {
		return user{}, fmt.Errorf("auth: %v %v", m["type"], err)
	}
	if err := write(map[string]any{"id": 1, "type": "auth/current_user"}); err != nil {
		return user{}, err
	}
	m, err := read()
	if err != nil {
		return user{}, err
	}
	result, _ := m["result"].(map[string]any)
	id, _ := result["id"].(string)
	name, _ := result["name"].(string)
	if id == "" {
		return user{}, errors.New("current user without id")
	}
	return user{ID: id, Name: name}, nil
}

func main() {
	listen := flag.String("listen", ":8080", "listen address")
	target := flag.String("target", "http://gateway:8099", "the gateway's UI listener")
	haURL := flag.String("ha", "https://homeassistant:8123", "Home Assistant")
	caFile := flag.String("ca", "", "PEM file with the CA of Home Assistant's certificate")
	token := flag.String("token", "", "Ingress token in the path")
	flag.Parse()
	if err := run(*listen, *target, *haURL, *caFile, *token); err != nil {
		fmt.Fprintln(os.Stderr, "ingressproxy:", err)
		os.Exit(1)
	}
}

func run(listen, target, haURL, caFile, token string) error {
	t, err := url.Parse(target)
	if err != nil || t.Host == "" || token == "" {
		return errors.New("need -target and -token")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return err
		}
		roots.AppendCertsFromPEM(pem)
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	srv := &http.Server{Addr: listen, Handler: newProxy(t, token, haLogin(client, haURL)), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}
