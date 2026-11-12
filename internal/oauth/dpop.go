// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/dpop"
)

// OAuth errors of DPoP at the token endpoint (RFC 9449 sections 5 and 8).
const (
	errInvalidDPoPProof = "invalid_dpop_proof"
	errUseDPoPNonce     = "use_dpop_nonce"
)

// jktPattern is the value of dpop_jkt: an RFC 7638 thumbprint, base64url of a SHA-256.
var jktPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// dpopMode is the configured mode; it is read under the lock so that tests may switch it
// while the server runs.
func (s *Server) dpopMode() dpop.Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.DPoPMode
}

func (s *Server) setDPoPMode(m dpop.Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.DPoPMode = m
}

// holder checks the DPoP proof of a token request (RFC 9449 section 5): the request
// method and the token endpoint's public URL, and a server nonce. It is checked in every
// mode, because an agent bound once stays bound when DPoP is switched off. Without a
// proof, the holder proves no key. On an invalid proof or a missing nonce it answers the
// request itself and returns false; the grant is not looked at, so a code survives.
func (s *Server) holder(w http.ResponseWriter, r *http.Request) (agent.Holder, bool) {
	mode := s.dpopMode()
	h := agent.Holder{Bind: mode != dpop.ModeOff, Require: mode == dpop.ModeRequired}
	p, err := s.dpop.Verify(r.Header, dpop.Expect{Method: http.MethodPost, URL: s.cfg.PublicURL + TokenPath, Nonce: true})
	switch {
	case errors.Is(err, dpop.ErrNoProof):
		return h, true
	case errors.Is(err, dpop.ErrUseNonce):
		w.Header().Set(dpop.NonceHeader, s.dpop.Nonce())
		oauthError(w, http.StatusBadRequest, errUseDPoPNonce)
		return h, false
	case err != nil:
		s.warn("DPoP proof refused at the token endpoint", "error", err)
		oauthError(w, http.StatusBadRequest, errInvalidDPoPProof)
		return h, false
	}
	h.JKT = p.JKT
	return h, true
}

// requireFor adds what client asks for: metadata with dpop_bound_access_tokens.
func requireFor(h agent.Holder, client Client) agent.Holder {
	h.Require = h.Require || client.DPoPBound
	return h
}

// handOut binds a pair issued by an admission or a reconnect to the holder's key and
// writes it; a pair that may not be handed out unbound is refused as invalid_dpop_proof
// and is revoked by the store.
func (s *Server) handOut(w http.ResponseWriter, r *http.Request, p agent.TokenPair, h agent.Holder, client Client) {
	bound, err := s.cfg.Tokens.Bind(r.Context(), p, h)
	switch {
	case err == nil:
		writeTokens(w, bound)
	case errors.Is(err, agent.ErrDPoP):
		s.warn("tokens refused without a DPoP proof", "oauth_client", client.ID)
		oauthError(w, http.StatusBadRequest, errInvalidDPoPProof)
	default:
		s.cfg.Logger.Error("binding tokens failed", "oauth_client", client.ID, "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error")
	}
}

// dpopMetadata adds the DPoP members of the metadata documents while DPoP is offered.
func (s *Server) dpopMetadata(md map[string]any, resource bool) map[string]any {
	mode := s.dpopMode()
	if mode == dpop.ModeOff {
		return md
	}
	md["dpop_signing_alg_values_supported"] = dpop.Algorithms
	if resource {
		md["dpop_bound_access_tokens_required"] = mode == dpop.ModeRequired
	}
	return md
}
