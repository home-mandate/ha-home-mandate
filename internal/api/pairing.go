// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/oauth"
	"github.com/home-mandate/home-mandate/internal/untrusted"
)

// maxCode bounds the typed code; the server ignores case, spaces and the dash.
const maxCode = 32

type wirePairingCandidate struct {
	PairingID      string `json:"pairing_id"`
	ClaimedName    string `json:"claimed_name"`
	Client         string `json:"client"`
	ClientVerified bool   `json:"client_verified"`
	RequestedAt    string `json:"requested_at"`
	ExpiresAt      string `json:"expires_at"`
	RequestedFrom  string `json:"requested_from"`
}

type pairingDecision struct {
	Code      string `json:"code"`
	PairingID string `json:"pairing_id"`
}

func (d pairingDecision) check() error {
	if d.Code == "" || len(d.Code) > maxCode {
		return failField(codeInvalidInput, "/code")
	}
	if d.PairingID == "" || len(d.PairingID) > 64 {
		return failField(codeInvalidInput, "/pairing_id")
	}
	return nil
}

// pairing returns the pairing service; without OAuth no agent can be admitted.
func (s *Server) pairing() (Pairing, error) {
	if s.cfg.Pairing == nil {
		return nil, fail(codeUnavailable)
	}
	return s.cfg.Pairing, nil
}

// pairingError maps the errors of internal/oauth. A wrong code and an unknown request
// look alike.
func pairingError(err error) error {
	var locked *oauth.PairingLockedError
	switch {
	case errors.As(err, &locked):
		return failRetry(codePairingLocked, int(math.Ceil(locked.RetryAfter.Seconds())))
	case errors.Is(err, oauth.ErrPairingInvalid):
		return fail(codePairingInvalid)
	case errors.Is(err, oauth.ErrPairingExpired):
		return fail(codePairingExpired)
	case errors.Is(err, oauth.ErrPairingConflict):
		return fail(codeConflict)
	case errors.Is(err, mandate.ErrCriticalConfirmation):
		return fail(codeCriticalConfirm)
	case errors.Is(err, mandate.ErrNoApprovers):
		return fail(codeNoApprovers)
	case errors.Is(err, admission.ErrTemplateNotFound):
		return failField(codeInvalidInput, "/template")
	case errors.Is(err, agent.ErrInvalidName):
		return failField(codeInvalidInput, "/display_name")
	case errors.Is(err, agent.ErrEmergencyStop), errors.Is(err, mandate.ErrConflict):
		return fail(codeConflict)
	case errors.Is(err, mandate.ErrInvalid):
		return failField(codeInvalidMandate, "/template")
	case errors.Is(err, oauth.ErrPairingUnavailable):
		return fail(codeUnavailable)
	case errors.Is(err, oauth.ErrPairingAdmission):
		return fail(codeInvalidInput)
	}
	return err
}

func (s *Server) pairingCheck(r *request) (any, error) {
	p, err := s.pairing()
	if err != nil {
		return nil, err
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.Code == "" || len(in.Code) > maxCode {
		return nil, failField(codeInvalidInput, "/code")
	}
	c, err := p.Check(r.Context(), r.user, in.Code)
	if err != nil {
		return nil, pairingError(err)
	}
	// Claimed by the agent: cleaned here as well as in the UI (defence in depth).
	return wirePairingCandidate{PairingID: c.PairingID, ClaimedName: untrusted.Clean(c.ClaimedName, untrusted.Max),
		Client: untrusted.Clean(c.Client, untrusted.Max), ClientVerified: c.ClientVerified,
		RequestedAt: *formatTime(c.RequestedAt), ExpiresAt: *formatTime(c.ExpiresAt), RequestedFrom: c.RequestedFrom}, nil
}

// validDisplayName applies the rules of agent names (and mandate names): trimmed, 1 to
// 80 characters, no control, format or separator characters, a letter or digit.
func validDisplayName(name string) (string, bool) {
	trimmed, ok := agent.ValidName(name)
	return trimmed, ok && utf8.RuneCountInString(trimmed) <= 80
}

func (s *Server) pairingApprove(r *request) (any, error) {
	p, err := s.pairing()
	if err != nil {
		return nil, err
	}
	var in struct {
		pairingDecision
		DisplayName     string  `json:"display_name"`
		Template        string  `json:"template"`
		MandateName     *string `json:"mandate_name"`
		ConfirmCritical bool    `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if err := in.check(); err != nil {
		return nil, err
	}
	name, ok := validDisplayName(in.DisplayName)
	if !ok {
		return nil, failField(codeInvalidInput, "/display_name")
	}
	if in.Template == "" || len(in.Template) > 64 {
		return nil, failField(codeInvalidInput, "/template")
	}
	mandateName := ""
	if in.MandateName != nil && strings.TrimSpace(*in.MandateName) != "" {
		if mandateName, ok = validDisplayName(*in.MandateName); !ok {
			return nil, failField(codeInvalidInput, "/mandate_name")
		}
	}
	a, err := p.Approve(r.Context(), r.user, oauth.PairingApproval{Code: in.Code, PairingID: in.PairingID, DisplayName: name,
		Template: in.Template, MandateName: mandateName, ConfirmCritical: in.ConfirmCritical, By: r.user})
	if err != nil {
		return nil, pairingError(err)
	}
	s.publish(event{Type: "agents.changed"})
	return s.presentAgent(r.Context(), a), nil
}

func (s *Server) pairingDeny(r *request) (any, error) {
	p, err := s.pairing()
	if err != nil {
		return nil, err
	}
	var in pairingDecision
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if err := in.check(); err != nil {
		return nil, err
	}
	if err := p.Deny(r.Context(), r.user, in.Code, in.PairingID); err != nil {
		return nil, pairingError(err)
	}
	return nil, nil
}
