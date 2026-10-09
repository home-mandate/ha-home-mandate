// SPDX-License-Identifier: AGPL-3.0-or-later

// Package oauth is Home-Mandate's authorization server for agents (ARCHITECTURE
// section 6): metadata (RFC 8414, RFC 9728), Authorization Code with PKCE, client
// identification via Client ID Metadata Documents, Resource Indicators (RFC 8707) and the
// Device Authorization Grant (RFC 8628) as pairing code. Humans sign in with their Home
// Assistant account; only administrators may admit agents. Tokens themselves are managed
// by internal/agent.
package oauth

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// CallbackPath is where Home Assistant sends the human back after signing in.
const CallbackPath = "/oauth/ha/callback"

const (
	maxHACodeLen     = 512
	maxHAAnswerBytes = 64 << 10
	haRevokeTimeout  = 5 * time.Second
)

// ErrSignInFailed means the sign-in through Home Assistant did not yield a user. The
// cause is wrapped for the log; it never contains a token or code.
var ErrSignInFailed = errors.New("oauth: sign-in through Home Assistant failed")

// HomeAssistant signs a human in through Home Assistant's OAuth for external
// applications (IndieAuth): the browser is sent to AuthorizeURL, Home Assistant sends it
// back to CallbackPath with a code, and SignIn turns the code into the user.
type HomeAssistant interface {
	AuthorizeURL(state string) string
	SignIn(ctx context.Context, code string) (ha.User, error)
}

// HASignInConfig configures HASignIn.
type HASignInConfig struct {
	// PublicURL is Home-Mandate's origin; it is the client ID at Home Assistant and the
	// origin of the callback, so Home Assistant needs no access to Home-Mandate.
	PublicURL string
	// BrowserURL is Home Assistant as the human's browser reaches it.
	BrowserURL string
	// HTTPURL and WebSocketURL are Home Assistant as Home-Mandate reaches it.
	HTTPURL      string
	WebSocketURL string
	Roots        *x509.CertPool
	// Plaintext are the hosts reached without TLS, as for Home-Mandate's own connection.
	Plaintext ha.Plaintext
	// Callback is the path Home Assistant sends the browser back to; CallbackPath when
	// empty. The UI's sign-in in direct mode uses its own.
	Callback string
}

// HASignIn implements HomeAssistant. It keeps no Home Assistant token of the human: the
// tokens from the exchange are used once for auth/current_user and revoked at once.
type HASignIn struct {
	clientID, redirectURI, browserURL, httpURL, wsURL string
	roots                                             *x509.CertPool
	client                                            *http.Client
	currentUser                                       func(context.Context, string, *x509.CertPool, ha.Secret) (ha.User, error)
}

// NewHASignIn validates cfg.
func NewHASignIn(cfg HASignInConfig) (*HASignIn, error) {
	if cfg.PublicURL == "" || cfg.BrowserURL == "" || cfg.WebSocketURL == "" {
		return nil, errors.New("oauth: sign-in needs the public URL and the Home Assistant URLs")
	}
	client, err := ha.HTTPClient(cfg.HTTPURL, cfg.Roots, cfg.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("oauth: Home Assistant HTTP URL: %w", err)
	}
	callback := cfg.Callback
	if callback == "" {
		callback = CallbackPath
	}
	if !strings.HasPrefix(callback, "/") || strings.ContainsAny(callback, "?#") {
		return nil, errors.New("oauth: callback must be a path")
	}
	return &HASignIn{clientID: cfg.PublicURL + "/", redirectURI: cfg.PublicURL + callback,
		browserURL: cfg.BrowserURL, httpURL: cfg.HTTPURL, wsURL: cfg.WebSocketURL, roots: cfg.Roots,
		client: client, currentUser: func(ctx context.Context, wsURL string, roots *x509.CertPool, token ha.Secret) (ha.User, error) {
			return ha.CurrentUser(ctx, wsURL, roots, cfg.Plaintext, token)
		}}, nil
}

// AuthorizeURL is Home Assistant's sign-in page for this sign-in.
func (s *HASignIn) AuthorizeURL(state string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {s.clientID}, "redirect_uri": {s.redirectURI}, "state": {state}}
	return s.browserURL + "/auth/authorize?" + q.Encode()
}

// haTokens is the answer of /auth/token.
type haTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// SignIn exchanges code, looks up the user and revokes the Home Assistant tokens.
func (s *HASignIn) SignIn(ctx context.Context, code string) (ha.User, error) {
	if code == "" || len(code) > maxHACodeLen || strings.ContainsFunc(code, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return ha.User{}, fmt.Errorf("%w: malformed code", ErrSignInFailed)
	}
	tokens, err := s.exchange(ctx, code)
	if tokens.RefreshToken != "" {
		defer s.revoke(context.WithoutCancel(ctx), tokens.RefreshToken)
	}
	if err != nil {
		return ha.User{}, fmt.Errorf("%w: %w", ErrSignInFailed, err)
	}
	u, err := s.currentUser(ctx, s.wsURL, s.roots, ha.Secret(tokens.AccessToken))
	if err != nil {
		return ha.User{}, fmt.Errorf("%w: %w", ErrSignInFailed, err)
	}
	return u, nil
}

func (s *HASignIn) exchange(ctx context.Context, code string) (haTokens, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {s.clientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.httpURL+"/auth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return haTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return haTokens{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHAAnswerBytes+1))
	if err != nil {
		return haTokens{}, fmt.Errorf("token answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return haTokens{}, fmt.Errorf("token request: status %d", resp.StatusCode)
	}
	if len(body) > maxHAAnswerBytes {
		return haTokens{}, errors.New("token answer too large")
	}
	var t haTokens
	if err := json.Unmarshal(body, &t); err != nil {
		return haTokens{}, errors.New("token answer is not JSON")
	}
	if t.AccessToken == "" {
		return t, errors.New("token answer without access token")
	}
	return t, nil
}

// revoke ends the Home Assistant session of the sign-in on a best-effort basis.
func (s *HASignIn) revoke(ctx context.Context, refreshToken string) {
	ctx, cancel := context.WithTimeout(ctx, haRevokeTimeout)
	defer cancel()
	form := url.Values{"token": {refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.httpURL+"/auth/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := s.client.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxHAAnswerBytes))
		resp.Body.Close()
	}
}
