// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

// Pairing in the Home-Mandate UI (decision D5): the same pending device grants as the
// /pair page, checked, approved or denied by an administrator signed in through Ingress.
// Wrong codes count per UI session (the Home Assistant user) and for everyone; a locked
// session or the global lock refuses even a correct code, so a lock tells nothing about
// codes. The code itself is never logged.
var (
	// ErrPairingInvalid means the code belongs to no pending pairing.
	ErrPairingInvalid = errors.New("oauth: pairing code invalid")
	// ErrPairingExpired means the code belonged to a pairing that expired or was decided.
	ErrPairingExpired = errors.New("oauth: pairing code expired")
	// ErrPairingConflict means the code now belongs to another pending request than the
	// one the person looked at.
	ErrPairingConflict = errors.New("oauth: pairing request changed")
	// ErrPairingAdmission means the admission was refused (template, name, emergency
	// stop, critical confirmation); the cause is wrapped.
	ErrPairingAdmission = errors.New("oauth: admission refused")
	// ErrPairingUnavailable means pairing is not possible right now (the server failed).
	ErrPairingUnavailable = errors.New("oauth: pairing unavailable")
)

// PairingLockedError means wrong codes locked pairing for this session or everyone.
type PairingLockedError struct {
	RetryAfter time.Duration
}

func (e *PairingLockedError) Error() string {
	return fmt.Sprintf("oauth: pairing locked for %s", e.RetryAfter)
}

// PairingCandidate is the agent waiting behind a code.
type PairingCandidate struct {
	PairingID      string
	ClaimedName    string
	Client         string
	ClientVerified bool
	RequestedAt    time.Time
	ExpiresAt      time.Time
	RequestedFrom  string
}

// PairingApproval is an administrator's decision to admit the agent behind a code.
type PairingApproval struct {
	Code, PairingID string
	DisplayName     string
	Template        string
	MandateName     string
	ConfirmCritical bool
	By              string // Home Assistant user ID
}

// uiFailures are the wrong codes of one UI session.
type uiFailures struct {
	at          []time.Time
	lockedUntil time.Time
}

const maxUISessions = 200

// issuedGrace keeps an issued grant at least this long for the agent's poll, so that an
// approval shortly before the code expires does not leave an agent without its tokens.
const issuedGrace = 2 * time.Minute

// lockSession serializes the attempts of one UI session (a Home Assistant user).
func (s *Server) lockSession(session string) func() {
	s.mu.Lock()
	l, ok := s.uiLocks[session]
	if !ok {
		l = &sessionLock{}
		s.uiLocks[session] = l
	}
	l.users++
	s.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		if l.users--; l.users == 0 {
			delete(s.uiLocks, session)
		}
		s.mu.Unlock()
	}
}

type sessionLock struct {
	mu    sync.Mutex
	users int // guarded by Server.mu
}

// lockedFor returns how long session or everyone is locked; 0 if not.
func (s *Server) lockedFor(session string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	until := s.pairing.lockedUntil
	if f, ok := s.uiFailures[session]; ok && f.lockedUntil.After(until) {
		until = f.lockedUntil
	}
	if !now.Before(until) {
		return 0
	}
	return until.Sub(now)
}

// failUI counts a wrong code of a UI session and for everyone, and returns the lock
// that follows from it (nil if none).
func (s *Server) failUI(ctx context.Context, session string) error {
	s.recordPairFailure()
	now := s.cfg.Now()
	s.mu.Lock()
	f, ok := s.uiFailures[session]
	if !ok {
		if len(s.uiFailures) >= maxUISessions {
			for k, v := range s.uiFailures {
				if !now.Before(v.lockedUntil) && (len(v.at) == 0 || now.Sub(v.at[len(v.at)-1]) >= pairWindow) {
					delete(s.uiFailures, k)
				}
			}
		}
		f = &uiFailures{}
		s.uiFailures[session] = f
	}
	recent := f.at[:0]
	for _, t := range f.at {
		if now.Sub(t) < pairWindow {
			recent = append(recent, t)
		}
	}
	f.at = append(recent, now)
	if len(f.at) >= pairSessionMax {
		f.lockedUntil, f.at = now.Add(pairWindow), nil
	}
	s.mu.Unlock()
	s.rejectUser(ctx, session, "pairing_code_invalid")
	if wait := s.lockedFor(session); wait > 0 {
		return &PairingLockedError{RetryAfter: wait}
	}
	return ErrPairingInvalid
}

// grantForCode finds the grant of a code for the UI: pending, or decided/expired. The
// attempts of a session are serialized, so that parallel guesses cannot all pass the
// lock check before the fifth wrong one is counted.
func (s *Server) grantForCode(ctx context.Context, session, code string) (string, deviceGrant, error) {
	unlock := s.lockSession(session)
	defer unlock()
	if wait := s.lockedFor(session); wait > 0 {
		return "", deviceGrant{}, &PairingLockedError{RetryAfter: wait}
	}
	normalized := normalizeUserCode(code)
	if normalized != "" {
		want := hashKey(normalized)
		now := s.cfg.Now()
		s.mu.Lock()
		for key, g := range s.grants {
			if g.userCode != want {
				continue
			}
			copied := *g
			s.mu.Unlock()
			if copied.status != grantPending || !now.Before(copied.expires) {
				return "", deviceGrant{}, ErrPairingExpired
			}
			return key, copied, nil
		}
		s.mu.Unlock()
	}
	return "", deviceGrant{}, s.failUI(ctx, session)
}

// normalizeAddr writes a sender address as the UI shows it: canonical, without zone and
// IPv4-mapped prefix, at most 45 characters; "" if it is no IP address.
func normalizeAddr(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return ""
	}
	out := ip.Unmap().WithZone("").String()
	if len(out) > 45 {
		return ""
	}
	return out
}

// Check returns the agent behind a code.
func (s *Server) Check(ctx context.Context, session, code string) (PairingCandidate, error) {
	_, g, err := s.grantForCode(ctx, session, code)
	if err != nil {
		return PairingCandidate{}, err
	}
	return PairingCandidate{PairingID: g.id, ClaimedName: g.client.Name, Client: g.client.ID, ClientVerified: g.client.Verified,
		RequestedAt: g.created, ExpiresAt: g.expires, RequestedFrom: normalizeAddr(g.sender)}, nil
}

// Approve admits the agent behind a code at once; its tokens wait for its next poll. If
// two approvals race, exactly one admits; the other gets ErrPairingExpired.
func (s *Server) Approve(ctx context.Context, session string, a PairingApproval) (agent.Agent, error) {
	key, g, err := s.grantForCode(ctx, session, a.Code)
	if err != nil {
		return agent.Agent{}, err
	}
	if !equalSecret(g.id, a.PairingID) {
		return agent.Agent{}, ErrPairingConflict
	}
	return s.admitGrant(ctx, key, g.id, decision{name: a.DisplayName, template: a.Template, mandateName: a.MandateName,
		confirmCritical: a.ConfirmCritical, by: a.By})
}

// Deny refuses the agent behind a code; its next poll gets access_denied.
func (s *Server) Deny(ctx context.Context, session, code, pairingID string) error {
	key, g, err := s.grantForCode(ctx, session, code)
	if err != nil {
		return err
	}
	if !equalSecret(g.id, pairingID) {
		return ErrPairingConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.grants[key]
	if !ok || cur.id != g.id || cur.status != grantPending {
		return ErrPairingExpired
	}
	cur.status = grantDenied
	return nil
}

// admitGrant admits the agent of a pending grant with the human's decision and keeps
// its tokens for the poll. The grant is taken while the admission runs, so a second
// decision at the same time fails; if the admission fails, the grant is pending again.
func (s *Server) admitGrant(ctx context.Context, key, id string, d decision) (agent.Agent, error) {
	s.mu.Lock()
	g, ok := s.grants[key]
	if !ok || g.id != id || g.status != grantPending || !s.cfg.Now().Before(g.expires) {
		s.mu.Unlock()
		return agent.Agent{}, ErrPairingExpired
	}
	g.status = grantAdmitting
	client, resource := g.client, g.resource
	s.mu.Unlock()

	a, tokens, err := s.cfg.Admission.Admit(context.WithoutCancel(ctx), admission.Request{DisplayName: d.name, Template: d.template,
		OAuthClient: client.ID, ClientVerified: client.Verified, RedirectURIs: client.RedirectURIs, Resource: resource,
		MandateName: d.mandateName, ConfirmCritical: d.confirmCritical, By: audit.Actor{Kind: audit.ActorUser, ID: d.by}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		g.status = grantPending
		if refused(err) {
			s.cfg.Logger.Warn("admission refused", "oauth_client", client.ID, "error", err)
			return agent.Agent{}, fmt.Errorf("%w: %w", ErrPairingAdmission, err)
		}
		s.cfg.Logger.Error("admission failed", "oauth_client", client.ID, "error", err)
		return agent.Agent{}, fmt.Errorf("%w: %w", ErrPairingUnavailable, err)
	}
	g.status, g.tokens = grantIssued, &tokens
	if min := s.cfg.Now().Add(issuedGrace); g.expires.Before(min) {
		g.expires = min
	}
	s.cfg.Logger.Info("agent admitted", "client_id", a.ClientID, "oauth_client", client.ID, "by", d.by)
	return a, nil
}

// refused tells admission errors that are the human's input or the state, not a failure.
func refused(err error) bool {
	return errors.Is(err, agent.ErrEmergencyStop) || errors.Is(err, admission.ErrTemplateNotFound) ||
		errors.Is(err, agent.ErrInvalidName) || errors.Is(err, mandate.ErrInvalid) ||
		errors.Is(err, mandate.ErrCriticalConfirmation) || errors.Is(err, mandate.ErrConflict)
}
