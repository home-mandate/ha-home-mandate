// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	controlPath = "/test/state"
	// maxBodyBytes bounds a request: a state carries several mandates of up to 256 KiB.
	maxBodyBytes = 8 << 20
)

// binding is the HTTP binding (SPEC-v0 section 10.3): the AuthZEN endpoint of
// internal/pdp on a state that PUT /test/state replaces as a whole.
type binding struct {
	token string

	mu    sync.Mutex
	state state
	at    time.Time
}

func (b *binding) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !b.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	switch {
	case r.Method == http.MethodPut && r.URL.Path == controlPath:
		b.setState(w, body)
	case r.Method == http.MethodPost && r.URL.Path == "/access/v1/evaluation":
		b.evaluate(w, r, body)
	default:
		http.NotFound(w, r)
	}
}

func (b *binding) authorized(r *http.Request) bool {
	want := "Bearer " + b.token
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) == 1
}

func (b *binding) setState(w http.ResponseWriter, body []byte) {
	var s state
	if err := json.Unmarshal(body, &s); err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	at, ok := pointInTime(s.Time)
	if !ok {
		http.Error(w, "invalid time", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	b.state, b.at = s, at
	b.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// evaluate serves the request with the PDP of the current state. The household the PDP
// serves is the principal the request names: the tool asks about one household per case,
// and mandates of other principals in the state must not be selected.
func (b *binding) evaluate(w http.ResponseWriter, r *http.Request, body []byte) {
	var peek struct {
		Subject struct {
			Properties struct {
				Principal string `json:"principal"`
			} `json:"properties"`
		} `json:"subject"`
	}
	_ = json.Unmarshal(body, &peek) // the PDP rejects a malformed request itself
	b.mu.Lock()
	s, at := b.state, b.at
	b.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(body))
	newPDP(s, peek.Subject.Properties.Principal, at).Handler().ServeHTTP(w, r)
}

// serveHTTP serves the binding on a loopback address until ctx ends; listening is told
// the address it listens on.
func serveHTTP(ctx context.Context, addr, token string, listening func(string)) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s is not a loopback address", host)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: &binding{token: token}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	listening(ln.Addr().String())
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return ctx.Err()
}
