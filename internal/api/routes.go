// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// maxBody bounds every request body (decision U4).
	maxBody = 64 << 10
	// requestLimit bounds the requests of one user: far above what the UI sends, far
	// below what would load the gateway.
	requestLimit  = 600
	requestPeriod = time.Minute
	// handlerTimeout bounds the work of one request.
	handlerTimeout = 20 * time.Second
)

// request is one authenticated API request.
type request struct {
	*http.Request
	user string
	w    http.ResponseWriter
	body []byte // the JSON body of writes, nil for GET
}

// handler answers a request with a value to encode, nil for 204, or an error.
type handler func(r *request) (any, error)

// bodyKind says what a route expects in the body.
type bodyKind int

const (
	noBody bodyKind = iota
	jsonBody
)

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	route := func(pattern string, body bodyKind, h handler) {
		mux.Handle(pattern, s.wrap(body, h))
	}
	route("GET /api/session", noBody, s.getSession)
	route("PUT /api/session/language", jsonBody, s.putLanguage)
	route("GET /api/system", noBody, s.getSystem)

	route("GET /api/agents", noBody, s.getAgents)
	route("POST /api/agents/revoke", jsonBody, s.revokeAgent)
	route("POST /api/agents/remove", jsonBody, s.removeAgent)
	route("POST /api/revoked/remove", noBody, s.removeRevoked)
	route("POST /api/pairing/check", jsonBody, s.pairingCheck)
	route("POST /api/pairing/approve", jsonBody, s.pairingApprove)
	route("POST /api/pairing/deny", jsonBody, s.pairingDeny)
	route("POST /api/pairing/reconnect", jsonBody, s.pairingReconnect)

	route("GET /api/devices", noBody, s.getDevices)
	route("PUT /api/devices/critical", jsonBody, s.putDeviceCritical)
	route("GET /api/renames", noBody, s.getRenames)
	route("POST /api/renames/apply", jsonBody, s.applyRename)
	route("POST /api/renames/dismiss", jsonBody, s.dismissRename)

	route("GET /api/mandates", noBody, s.getMandates)
	route("POST /api/mandates", jsonBody, s.createMandate)
	route("GET /api/mandates/{id}", noBody, s.getMandate)
	route("PUT /api/mandates/{id}", jsonBody, s.putMandate)
	route("GET /api/mandates/{id}/versions/{number}", noBody, s.getMandateVersion)
	route("POST /api/mandates/{id}/apply-template", jsonBody, s.applyTemplate)
	route("POST /api/mandates/{id}/revoke", noBody, s.revokeMandate)
	route("POST /api/mandates/{id}/remove", noBody, s.removeMandate)

	route("GET /api/templates", noBody, s.getTemplates)
	route("GET /api/templates/{name}", noBody, s.getTemplate)
	route("PUT /api/templates/{name}", jsonBody, s.putTemplate)
	route("DELETE /api/templates/{name}", noBody, s.deleteTemplate)
	route("PUT /api/templates/{name}/hidden", jsonBody, s.putTemplateHidden)
	route("GET /api/templates/{name}/approvers", noBody, s.getTemplateApprovers)
	route("GET /api/templates/{name}/usage", noBody, s.getTemplateUsage)
	route("POST /api/templates/{name}/apply", jsonBody, s.applyTemplateToMandates)

	route("GET /api/settings", noBody, s.getSettings)
	route("PUT /api/settings", jsonBody, s.putSettings)

	route("GET /api/approvals", noBody, s.getApprovals)
	route("POST /api/approvals/{id}/answer", jsonBody, s.answerApproval)

	route("GET /api/audit", noBody, s.getAudit)
	route("POST /api/audit/verify", noBody, s.verifyAudit)

	route("GET /api/approvers", noBody, s.getApprovers)
	route("PUT /api/approvers/{id}", jsonBody, s.putApprover)
	route("DELETE /api/approvers/{id}", noBody, s.deleteApprover)
	route("POST /api/approvers/{id}/test", noBody, s.testApprover)

	route("PUT /api/emergency-stop", jsonBody, s.putEmergencyStop)

	mux.Handle("GET /api/events", s.authenticated(http.HandlerFunc(s.events)))
	// Everything else under /api: the same answer for unknown paths and methods.
	mux.Handle("/api/", s.authenticated(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, fail(codeNotFound))
	})))
	mux.Handle("/api", s.authenticated(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, fail(codeNotFound))
	})))
	return mux
}

type userKey struct{}

// authenticated lets through only an administrator, signed in through Ingress or, in
// direct mode, through Home-Mandate's own sign-in, within the request limit. The user is
// in the context afterwards.
func (s *Server) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := identify(r)
		if !ok {
			writeError(w, fail(codeUnauthenticated))
			return
		}
		// Also before the administrator check: whoever opens the panel cannot make the
		// gateway ask Home Assistant without bound.
		if ok, wait := s.limits.allow("pre:"+user, 2*requestLimit, requestPeriod); !ok {
			writeError(w, failRetry(codeRateLimited, wait))
			return
		}
		admin, err := s.users.IsAdmin(r.Context(), user)
		switch {
		case err != nil:
			s.cfg.Logger.Warn("administrator check failed, request refused", "error", err)
			writeError(w, fail(codeUnavailable))
			return
		case !admin:
			s.cfg.Logger.Warn("request of a user who is no administrator refused", "user_id", user)
			writeError(w, fail(codeForbidden))
			return
		}
		if ok, wait := s.limits.allow("req:"+user, requestLimit, requestPeriod); !ok {
			writeError(w, failRetry(codeRateLimited, wait))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	})
}

// wrap runs h after authentication, the checks of writes and decoding the body.
func (s *Server) wrap(kind bodyKind, h handler) http.Handler {
	return s.authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(userKey{}).(string)
		req := &request{Request: r, user: user, w: w}
		if r.Method != http.MethodGet {
			if err := s.checkWrite(r, user); err != nil {
				writeError(w, err)
				return
			}
		}
		body, err := readBody(w, r, kind)
		if err != nil {
			writeError(w, err)
			return
		}
		req.body = body
		timeout := handlerTimeout
		if r.Pattern == "POST /api/audit/verify" {
			timeout = verifyTimeout + 5*time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		req.Request = r.WithContext(ctx)
		out, err := h(req)
		if errors.Is(err, context.DeadlineExceeded) {
			// Too slow (a large log, a slow Home Assistant): no failure of the server.
			s.cfg.Logger.Warn("API request took too long", "route", r.Pattern)
			err = fail(codeUnavailable)
		}
		if err != nil {
			var e *apiError
			if !errors.As(err, &e) {
				s.cfg.Logger.Error("API request failed", "method", r.Method, "route", r.Pattern, "error", err)
			}
			writeError(w, err)
			return
		}
		if out == nil {
			apiHeaders(w.Header())
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}))
}

// checkWrite applies what every write needs (decision U4): a request of the page
// itself (Sec-Fetch-Site), and the session's CSRF token.
func (s *Server) checkWrite(r *http.Request, user string) error {
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return fail(codeForbidden)
	}
	tokens := r.Header.Values("X-HM-CSRF")
	if len(tokens) != 1 || !s.validCSRF(user, tokens[0]) {
		return fail(codeCSRF)
	}
	return nil
}

// readBody reads at most maxBody bytes of JSON for routes that take a body, and refuses
// a body where none is expected.
func readBody(w http.ResponseWriter, r *http.Request, kind bodyKind) ([]byte, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, fail(codeTooLarge)
	case err != nil:
		return nil, fail(codeInvalidInput)
	}
	if kind == noBody {
		if len(data) > 0 {
			return nil, fail(codeInvalidInput)
		}
		return nil, nil
	}
	ct := r.Header.Get("Content-Type")
	if ct != "application/json" && !strings.HasPrefix(ct, "application/json;") {
		return nil, fail(codeInvalidInput)
	}
	return data, nil
}

// decode reads the body into v: exactly one JSON object, no unknown fields.
func (r *request) decode(v any) error {
	dec := json.NewDecoder(bytes.NewReader(r.body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return failField(codeInvalidInput, fieldOf(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail(codeInvalidInput)
	}
	return nil
}

// fieldOf names the field a decoding error is about, as a JSON pointer, if it can.
func fieldOf(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return "/" + strings.ReplaceAll(typeErr.Field, ".", "/")
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		name = strings.Trim(name, `"`)
		if userIDPattern.MatchString(name) {
			return "/" + name
		}
	}
	return ""
}
