// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestCurrentUser(t *testing.T) {
	f := newFakeHA(t)
	f.set(func(f *fakeHA) { f.token = "user-token" })
	f.handle("auth/current_user", func(fakeMsg) (any, *CommandError) {
		return map[string]any{"id": "u-123", "name": "Markus", "is_owner": true, "is_admin": true,
			"credentials": []any{map[string]any{"auth_provider_type": "homeassistant"}}}, nil
	})
	u, err := CurrentUser(context.Background(), f.url(), nil, Plaintext{}, "", "user-token")
	if err != nil {
		t.Fatal(err)
	}
	if u != (User{ID: "u-123", Name: "Markus", IsOwner: true, IsAdmin: true}) {
		t.Errorf("user = %+v", u)
	}
	// Exactly one command, from the allowlist.
	if got := f.receivedTypes(); !slices.Equal(got, []string{"auth/current_user"}) {
		t.Errorf("commands = %v", got)
	}
}

func TestCurrentUserRejects(t *testing.T) {
	tests := map[string]struct {
		handler func(fakeMsg) (any, *CommandError)
		token   Secret
		want    error
	}{
		"token rejected": {nil, "wrong", ErrAuthInvalid},
		"command fails": {func(fakeMsg) (any, *CommandError) {
			return nil, &CommandError{Code: "unauthorized", Message: "x"}
		}, "user-token", nil},
		"no id":     {func(fakeMsg) (any, *CommandError) { return map[string]any{"name": "x"}, nil }, "user-token", ErrProtocol},
		"malformed": {func(fakeMsg) (any, *CommandError) { return []any{1}, nil }, "user-token", ErrProtocol},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFakeHA(t)
			f.set(func(f *fakeHA) { f.token = "user-token" })
			if tc.handler != nil {
				f.handle("auth/current_user", tc.handler)
			}
			_, err := CurrentUser(context.Background(), f.url(), nil, Plaintext{}, "", tc.token)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("CurrentUser = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCurrentUserRefusesInsecureURLs(t *testing.T) {
	for _, u := range []string{"ws://ha.example.org/api/websocket", "http://localhost/api/websocket", "wss://user:pw@ha/api/websocket"} {
		if _, err := CurrentUser(context.Background(), u, nil, Plaintext{}, "", "t"); !errors.Is(err, ErrInsecureURL) && !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%s: %v", u, err)
		}
	}
	if _, err := CurrentUser(context.Background(), "ws://localhost:1/api/websocket", nil, appPlaintext, "", "t"); !errors.Is(err, ErrInsecureURL) {
		t.Errorf("localhost in app mode: %v", err)
	}
	if _, err := CurrentUser(context.Background(), "ws://localhost:1/api/websocket", nil, Plaintext{}, "", ""); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("empty token: %v", err)
	}
}

func TestCurrentUserTimesOut(t *testing.T) {
	f := newFakeHA(t)
	f.set(func(f *fakeHA) { f.token = "user-token"; f.authMode = "silent" })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := CurrentUser(ctx, f.url(), nil, Plaintext{}, "", "user-token"); err == nil {
		t.Error("CurrentUser succeeded")
	}
}

func TestClientCurrentUser(t *testing.T) {
	f := newFakeHA(t)
	f.handle("auth/current_user", func(fakeMsg) (any, *CommandError) {
		return map[string]any{"id": "hm-service", "name": "Home-Mandate", "is_admin": true}, nil
	})
	c, _ := startClient(t, testConfig(f.url()))
	waitReady(t, c)
	u, err := c.CurrentUser(context.Background())
	if err != nil || u.ID != "hm-service" {
		t.Errorf("CurrentUser = %+v, %v", u, err)
	}
	f.handle("auth/current_user", func(fakeMsg) (any, *CommandError) { return map[string]any{}, nil })
	if _, err := c.CurrentUser(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Errorf("without id: %v", err)
	}
	f.handle("auth/current_user", func(fakeMsg) (any, *CommandError) { return nil, &CommandError{Code: "x"} })
	if _, err := c.CurrentUser(context.Background()); err == nil {
		t.Error("error result accepted")
	}
}
