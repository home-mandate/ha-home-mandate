// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"
)

// userReadLimit bounds the messages of a CurrentUser connection; the answer is small.
const userReadLimit = 64 << 10

// User is a Home Assistant user as reported by auth/current_user.
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsOwner bool   `json:"is_owner"`
	IsAdmin bool   `json:"is_admin"`
}

// CurrentUser connects once with the access token of a human who signed in, asks
// auth/current_user and closes the connection. It is how Home-Mandate learns who signed
// in and whether they are an administrator; the same URL and TLS rules as for its own
// connection apply. No other command is sent.
func CurrentUser(ctx context.Context, wsURL string, roots *x509.CertPool, plaintext Plaintext, token Secret) (User, error) {
	c, err := New(Config{URL: wsURL, Token: token, RootCAs: roots, Plaintext: plaintext})
	if err != nil {
		return User{}, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return User{}, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(userReadLimit)
	if err := c.authenticate(ctx, conn); err != nil {
		return User{}, err
	}
	cmd := command{Type: "auth/current_user"}
	if err := checkAllowed(cmd); err != nil {
		return User{}, err
	}
	data, err := encodeCommand(1, cmd)
	if err != nil {
		return User{}, err
	}
	actx, cancel := context.WithTimeout(ctx, c.cfg.AuthTimeout)
	defer cancel()
	if err := conn.Write(actx, websocket.MessageText, data); err != nil {
		return User{}, fmt.Errorf("home assistant: current user: %w", err)
	}
	for {
		m, err := readMessage(actx, conn)
		if err != nil {
			return User{}, fmt.Errorf("home assistant: current user: %w", err)
		}
		if m.Type != typeResult || m.ID != 1 {
			continue // e.g. a pong or an unrelated message; nothing else was asked
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return decodeUser(m)
	}
}

// CurrentUser returns the user of the client's own access token: Home-Mandate's Home
// Assistant user.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	u, err := call[User](ctx, c, "auth/current_user")
	if err != nil {
		return User{}, err
	}
	if u.ID == "" {
		return User{}, fmt.Errorf("%w: current user without id", ErrProtocol)
	}
	return u, nil
}

func decodeUser(m message) (User, error) {
	if !m.Success {
		if m.Error != nil {
			return User{}, m.Error
		}
		return User{}, errors.New("home assistant: current user: failed")
	}
	var u User
	if err := json.Unmarshal(m.Result, &u); err != nil {
		return User{}, fmt.Errorf("%w: current user: %w", ErrProtocol, err)
	}
	if u.ID == "" {
		return User{}, fmt.Errorf("%w: current user without id", ErrProtocol)
	}
	return u, nil
}
