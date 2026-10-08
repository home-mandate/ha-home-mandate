// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// verifierPattern is a PKCE code verifier (RFC 7636 section 4.1).
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// token is the token endpoint for public clients: no client authentication, the
// client is identified by client_id and bound by PKCE or by the refresh token.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	form, ok := parseForm(w, r)
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	switch form["grant_type"] {
	case "authorization_code":
		s.exchangeCode(w, r, form)
	case "refresh_token":
		s.refresh(w, r, form)
	case deviceGrantID:
		s.pollDevice(w, r, form)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

// exchangeCode redeems an authorization code once, for the client and redirect URI it
// was issued to and with the PKCE verifier of its challenge; then the agent is admitted.
func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, form map[string]string) {
	verifier := form["code_verifier"]
	if form["code"] == "" || form["client_id"] == "" || form["redirect_uri"] == "" || verifier == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	c, ok := s.takeCode(form["code"])
	if !ok || c.authz.client.ID != form["client_id"] || c.authz.redirectURI != form["redirect_uri"] ||
		!verifierPattern.MatchString(verifier) || !pkceMatches(verifier, c.authz.challenge) ||
		form["resource"] != "" && form["resource"] != c.authz.resource {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	s.admit(w, r, c.authz.client, c.authz.resource, c.decision)
}

func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return equalSecret(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

// admit carries out the human's decision and returns the agent's first tokens. It
// reports whether the failure was on the server side, so that a pairing can be retried;
// the admission is not cancelled when the agent disconnects.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, client Client, resource string, d decision) (serverError bool) {
	a, tokens, err := s.cfg.Admission.Admit(context.WithoutCancel(r.Context()), admission.Request{DisplayName: d.name, Template: d.template, TemplateDigest: d.templateDigest,
		OAuthClient: client.ID, ClientVerified: client.Verified, RedirectURIs: client.RedirectURIs, Resource: resource,
		By: audit.Actor{Kind: audit.ActorUser, ID: d.by}})
	switch {
	case err == nil:
		s.cfg.Logger.Info("agent admitted", "client_id", a.ClientID, "oauth_client", client.ID, "by", d.by)
		writeTokens(w, tokens)
	case refused(err):
		s.cfg.Logger.Warn("admission refused", "oauth_client", client.ID, "error", err)
		oauthError(w, http.StatusBadRequest, "invalid_grant")
	default:
		s.cfg.Logger.Error("admission failed", "oauth_client", client.ID, "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error")
		return true
	}
	return false
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request, form map[string]string) {
	if form["refresh_token"] == "" || form["client_id"] == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if res := form["resource"]; res != "" && res != s.cfg.Resource {
		oauthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	tokens, err := s.cfg.Tokens.Refresh(r.Context(), form["refresh_token"], form["resource"], form["client_id"])
	switch {
	case err == nil:
		writeTokens(w, tokens)
	case errors.Is(err, agent.ErrInvalidGrant):
		if errors.Is(err, agent.ErrRefreshReused) {
			s.cfg.Logger.Warn("refresh token reused, token family revoked", "oauth_client", form["client_id"])
		}
		oauthError(w, http.StatusBadRequest, "invalid_grant")
	default:
		s.cfg.Logger.Error("refreshing tokens failed", "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error")
	}
}

func writeTokens(w http.ResponseWriter, p agent.TokenPair) {
	writeJSON(w, http.StatusOK, tokenResponse{AccessToken: p.AccessToken, TokenType: "Bearer",
		ExpiresIn: int(agent.AccessTokenTTL.Seconds()), RefreshToken: p.RefreshToken})
}
