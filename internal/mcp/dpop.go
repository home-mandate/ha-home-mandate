// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/home-mandate/ha-home-mandate/internal/dpop"
)

// dpopRequiredProtocols are the MCP protocol versions whose clients must use DPoP: a
// request that names one in MCP-Protocol-Version gets no Bearer access. Empty while MCP
// leaves DPoP optional; a release adds a version once MCP makes DPoP mandatory for it
// (issue #64). There is no setting for this.
var dpopRequiredProtocols = map[string]bool{}

// Error codes of refused requests in the challenge and in auth.rejected.
const (
	errInvalidToken     = "invalid_token"
	errInvalidDPoPProof = "invalid_dpop_proof"
	errDPoPRequired     = "dpop_required"
)

// dpopChallengeAlgs is the algs parameter of the DPoP challenge (RFC 9449 section 7.1).
var dpopChallengeAlgs = `algs="` + strings.Join(dpop.Algorithms, " ") + `"`

// proofKey carries the dpopState of a request from withDPoP to verify; only withDPoP
// sets it.
type proofKey struct{}

// dpopState is a request's checked proof; verify marks it replayed, so that the 401
// names the proof, not the token.
type dpopState struct {
	proof    dpop.Proof
	replayed bool
}

// withDPoP puts the DPoP check in front of the SDK's Bearer middleware, which knows only
// Bearer (RFC 9449 section 7):
//
//   - "DPoP <token>" needs a proof for this request with ath of the token; the token then
//     goes on as Bearer and verify compares the key with the one the token is bound to;
//   - "Bearer <token>" is for unbound tokens only (verify refuses a bound one), and is
//     refused outright in mode required and for the protocol versions that require DPoP;
//   - every 401 carries the DPoP challenge besides the Bearer one, while DPoP is offered or
//     when the request used DPoP.
func (g *Gateway) withDPoP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token := authorization(r.Header.Get("Authorization"))
		usedDPoP := strings.EqualFold(scheme, "DPoP")
		cw := &challengeWriter{ResponseWriter: w, offer: g.cfg.DPoPMode != dpop.ModeOff || usedDPoP}
		if usedDPoP && token != "" {
			cw.dpopError = errInvalidToken
		}
		switch {
		case usedDPoP && token != "":
			p, err := g.cfg.DPoP.Check(r.Header, dpop.Expect{Method: r.Method, URL: g.cfg.Resource, AccessToken: token})
			if err != nil {
				g.recordRejectedCode(r.Context(), errInvalidDPoPProof)
				cw.dpopError = errInvalidDPoPProof
				g.unauthorized(cw)
				return
			}
			cw.state = &dpopState{proof: p}
			r = r.Clone(context.WithValue(r.Context(), proofKey{}, cw.state))
			r.Header.Set("Authorization", "Bearer "+token)
		case strings.EqualFold(scheme, "Bearer") &&
			(g.cfg.DPoPMode == dpop.ModeRequired || dpopRequiredProtocols[r.Header.Get("MCP-Protocol-Version")]):
			g.recordRejectedCode(r.Context(), errDPoPRequired)
			g.unauthorized(cw)
			return
		}
		next.ServeHTTP(cw, r)
	})
}

// authorization splits an Authorization header into scheme and token, as the SDK does:
// exactly two fields, else no token.
func authorization(header string) (scheme, token string) {
	fields := strings.Fields(header)
	switch len(fields) {
	case 0:
		return "", ""
	case 2:
		return fields[0], fields[1]
	}
	return fields[0], ""
}

// unauthorized answers 401 with the Bearer challenge; challengeWriter adds the DPoP one.
func (g *Gateway) unauthorized(w http.ResponseWriter) {
	if g.cfg.ResourceMetadataURL != "" {
		w.Header().Add("WWW-Authenticate", `Bearer resource_metadata="`+g.cfg.ResourceMetadataURL+`"`)
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// proofOf returns the state of the proof withDPoP checked for r; nil without one.
func proofOf(r *http.Request) *dpopState {
	if r == nil {
		return nil
	}
	st, _ := r.Context().Value(proofKey{}).(*dpopState)
	return st
}

// challengeWriter adds the DPoP challenge to a 401 or 403 of the handlers behind it.
type challengeWriter struct {
	http.ResponseWriter
	offer     bool
	dpopError string
	state     *dpopState
}

func (c *challengeWriter) WriteHeader(code int) {
	if c.state != nil && c.state.replayed {
		c.dpopError = errInvalidDPoPProof
	}
	if c.offer && (code == http.StatusUnauthorized || code == http.StatusForbidden) {
		challenge := "DPoP " + dpopChallengeAlgs
		if c.dpopError != "" {
			challenge = `DPoP error="` + c.dpopError + `", ` + dpopChallengeAlgs
		}
		c.Header().Add("WWW-Authenticate", challenge)
	}
	c.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the connection (flushing of streams).
func (c *challengeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// Flush passes a flush on, for handlers that check for http.Flusher directly.
func (c *challengeWriter) Flush() { _ = http.NewResponseController(c.ResponseWriter).Flush() }
