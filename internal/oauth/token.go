// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
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
	// Home-Mandate admits public clients only. Client authentication is refused rather
	// than ignored, so that no client takes it for checked; before the grant is looked
	// at, so that a code survives the refusal.
	if r.Header.Get("Authorization") != "" {
		// RFC 6749 section 5.2: authentication in the header is answered with 401.
		w.Header().Set("WWW-Authenticate", `Basic realm="Home-Mandate"`)
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if form["client_assertion"] != "" || form["client_assertion_type"] != "" || form["client_secret"] != "" {
		oauthError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	grant := form["grant_type"]
	if grant != "authorization_code" && grant != "refresh_token" && grant != deviceGrantID {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	h, ok := s.holder(w, r)
	if !ok {
		return
	}
	switch grant {
	case "authorization_code":
		s.exchangeCode(w, r, form, h)
	case "refresh_token":
		s.refresh(w, r, form, h)
	default:
		s.pollDevice(w, r, form, h)
	}
}

// exchangeCode redeems an authorization code once, for the client and redirect URI it
// was issued to and with the PKCE verifier of its challenge; then the agent is admitted.
func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, form map[string]string, h agent.Holder) {
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
	// RFC 9449 section 10: a code bound by dpop_jkt is exchanged only with a proof of that
	// key. Refused before the admission, so that nobody is admitted without the tokens.
	h = requireFor(h, c.authz.client)
	if c.authz.dpopJKT != "" {
		h.Bind = true // the code was bound while DPoP was offered; switching off since changes nothing
	}
	if c.authz.dpopJKT != "" && h.JKT != c.authz.dpopJKT || h.Require && h.JKT == "" {
		s.warn("code exchange refused: DPoP proof missing or of another key", "oauth_client", c.authz.client.ID)
		oauthError(w, http.StatusBadRequest, errInvalidDPoPProof)
		return
	}
	s.admit(w, r, c.authz.client, c.authz.resource, c.decision, h)
}

func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return equalSecret(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

// admit carries out the human's decision and returns the agent's first tokens. It
// reports whether the failure was on the server side, so that a pairing can be retried;
// the admission is not cancelled when the agent disconnects.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, client Client, resource string, d decision, h agent.Holder) (serverError bool) {
	_, tokens, err := s.carryOut(r.Context(), client, resource, d)
	switch {
	case err == nil:
		s.handOut(w, r, tokens, h, client)
	case refused(err):
		s.warn("admission refused", "oauth_client", client.ID, "error", err)
		oauthError(w, http.StatusBadRequest, "invalid_grant")
	default:
		s.cfg.Logger.Error("admission failed", "oauth_client", client.ID, "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error")
		return true
	}
	return false
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request, form map[string]string, h agent.Holder) {
	if form["refresh_token"] == "" || form["client_id"] == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if res := form["resource"]; res != "" && res != s.cfg.Resource {
		oauthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	tokens, err := s.cfg.Tokens.Refresh(r.Context(), form["refresh_token"], form["resource"], form["client_id"], h)
	switch {
	case err == nil:
		if tokens.Repeated {
			s.warn("refresh token presented again within the grace period", "oauth_client", form["client_id"])
		}
		writeTokens(w, tokens)
	case errors.Is(err, agent.ErrDPoP):
		s.warn("refresh refused: DPoP proof missing or of another key", "oauth_client", form["client_id"])
		oauthError(w, http.StatusBadRequest, errInvalidDPoPProof)
	case errors.Is(err, agent.ErrInvalidGrant):
		switch {
		case errors.Is(err, agent.ErrRefreshReused):
			s.warn("refresh token reused, token family revoked", "oauth_client", form["client_id"])
		case errors.Is(err, agent.ErrRefreshWrongClient):
			s.warn("refresh token of another client refused", "oauth_client", form["client_id"])
		}
		oauthError(w, http.StatusBadRequest, "invalid_grant")
	default:
		s.cfg.Logger.Error("refreshing tokens failed", "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error")
	}
}

// writeTokens writes a pair; bound tokens have token_type DPoP (RFC 9449 section 5).
func writeTokens(w http.ResponseWriter, p agent.TokenPair) {
	tokenType := "Bearer"
	if p.JKT != "" {
		tokenType = "DPoP"
	}
	writeJSON(w, http.StatusOK, tokenResponse{AccessToken: p.AccessToken, TokenType: tokenType,
		ExpiresIn: int(agent.AccessTokenTTL.Seconds()), RefreshToken: p.RefreshToken})
}
