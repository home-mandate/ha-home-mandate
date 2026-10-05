// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Error codes of the contract (ApiErrorCode).
const (
	codeUnauthenticated = "unauthenticated"
	codeForbidden       = "forbidden"
	codeCSRF            = "csrf_invalid"
	codeNotFound        = "not_found"
	codeConflict        = "conflict"
	codeInvalidInput    = "invalid_input"
	codeInvalidMandate  = "invalid_mandate"
	codeCriticalConfirm = "critical_confirmation_required"
	codeBuiltinTemplate = "builtin_template" // a base template cannot be changed or removed
	codeNoApprovers     = "no_approvers"     // a template's placeholder has nobody to stand for
	codePairingInvalid  = "pairing_code_invalid"
	codePairingExpired  = "pairing_code_expired"
	codePairingLocked   = "pairing_locked"
	codeTooLarge        = "too_large"
	codeRateLimited     = "rate_limited"
	codeUnavailable     = "unavailable"
	codeInternal        = "internal"
)

// statusOf is the HTTP status of every code (types.ts, header comment).
var statusOf = map[string]int{
	codeUnauthenticated: http.StatusUnauthorized,
	codeForbidden:       http.StatusForbidden,
	codeCSRF:            http.StatusForbidden,
	codeNotFound:        http.StatusNotFound,
	codeConflict:        http.StatusConflict,
	codeInvalidInput:    http.StatusBadRequest,
	codeInvalidMandate:  http.StatusUnprocessableEntity,
	codeCriticalConfirm: http.StatusUnprocessableEntity,
	codeBuiltinTemplate: http.StatusConflict,
	codeNoApprovers:     http.StatusUnprocessableEntity,
	codePairingInvalid:  http.StatusBadRequest,
	codePairingExpired:  http.StatusGone,
	codePairingLocked:   http.StatusTooManyRequests,
	codeTooLarge:        http.StatusRequestEntityTooLarge,
	codeRateLimited:     http.StatusTooManyRequests,
	codeUnavailable:     http.StatusServiceUnavailable,
	codeInternal:        http.StatusInternalServerError,
}

// apiError is an answer with an error code; field is a JSON pointer into the request
// body, retryAfter seconds until a lock or limit ends.
type apiError struct {
	code       string
	field      string
	retryAfter int
}

func (e *apiError) Error() string { return "api: " + e.code + " " + e.field }

func fail(code string) error { return &apiError{code: code} }

func failField(code, field string) error { return &apiError{code: code, field: field} }

func failRetry(code string, seconds int) error {
	return &apiError{code: code, retryAfter: max(seconds, 1)}
}

type errorBody struct {
	Code       string `json:"code"`
	Field      string `json:"field,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

// writeError answers err: an apiError as it is, anything else as internal without
// details (the cause is logged by the caller).
func writeError(w http.ResponseWriter, err error) {
	var e *apiError
	if !errors.As(err, &e) {
		e = &apiError{code: codeInternal}
	}
	if e.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.retryAfter))
	}
	writeJSON(w, statusOf[e.code], errorBody{Code: e.code, Field: e.field, RetryAfter: e.retryAfter})
}

// writeJSON writes v with the headers of every API answer.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		status, data = http.StatusInternalServerError, []byte(`{"code":"internal"}`)
	}
	apiHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func apiHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	h.Set("Referrer-Policy", "no-referrer")
}
