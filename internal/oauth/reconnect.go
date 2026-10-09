// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// Reconnecting after an emergency stop (issue #22, SPEC-v0 section 11.3). An agent that
// signs in again brings nothing but its OAuth client ID, which several agents can share.
// So the consent page and the pairing step only offer the agents of that client that
// have no valid token; the default stays admitting a new agent, and only an
// administrator's explicit choice reconnects one.

// ReconnectCandidate is an existing agent of the signing-in client without a valid
// token, offered for reconnecting.
type ReconnectCandidate struct {
	ClientID    string
	DisplayName string
	MandateID   string // empty without an active mandate
	MandateName string
	AdmittedAt  time.Time
}

// PairingReconnect is an administrator's decision to reconnect an existing agent to the
// agent behind a code.
type PairingReconnect struct {
	Code, PairingID string
	ClientID        string // the existing agent
	By              string // Home Assistant user ID
}

// reconnectCandidates lists the agents a human may reconnect for client. A failure is
// logged and offers none: admitting a new agent stays possible.
func (s *Server) reconnectCandidates(ctx context.Context, client Client) []ReconnectCandidate {
	list, err := s.cfg.Admission.Reconnectable(ctx, agent.Client{ID: client.ID, Verified: client.Verified})
	if err != nil {
		s.cfg.Logger.Error("listing agents to reconnect failed", "oauth_client", client.ID, "error", err)
		return nil
	}
	out := make([]ReconnectCandidate, 0, len(list))
	for _, r := range list {
		out = append(out, ReconnectCandidate{ClientID: r.Agent.ClientID, DisplayName: r.Agent.DisplayName, MandateID: r.MandateID,
			MandateName: r.MandateName, AdmittedAt: r.Agent.CreatedAt})
	}
	return out
}

// offered reports whether clientID is among the agents offered for client now.
func (s *Server) offered(ctx context.Context, client Client, clientID string) bool {
	return clientID != "" && slices.ContainsFunc(s.reconnectCandidates(ctx, client), func(c ReconnectCandidate) bool {
		return c.ClientID == clientID
	})
}

// carryOut carries out a human's decision for client: it admits a new agent or, if the
// human chose one, reconnects an existing agent. The decision is not cancelled when the
// agent disconnects.
func (s *Server) carryOut(ctx context.Context, client Client, resource string, d decision) (agent.Agent, agent.TokenPair, error) {
	ctx = context.WithoutCancel(ctx)
	by := audit.Actor{Kind: audit.ActorUser, ID: d.by}
	if d.reconnect != "" {
		a, tokens, err := s.cfg.Admission.Reconnect(ctx, admission.ReconnectRequest{ClientID: d.reconnect, OAuthClient: client.ID,
			ClientVerified: client.Verified, Resource: resource, By: by})
		if err == nil {
			s.cfg.Logger.Info("agent reconnected", "client_id", a.ClientID, "oauth_client", client.ID, "by", d.by)
		}
		return a, tokens, err
	}
	a, tokens, err := s.cfg.Admission.Admit(ctx, admission.Request{DisplayName: d.name, Template: d.template, TemplateDigest: d.templateDigest,
		OAuthClient: client.ID, ClientVerified: client.Verified, RedirectURIs: client.RedirectURIs, Resource: resource,
		MandateName: d.mandateName, ConfirmCritical: d.confirmCritical, By: by})
	if err == nil {
		s.cfg.Logger.Info("agent admitted", "client_id", a.ClientID, "oauth_client", client.ID, "by", d.by)
	}
	return a, tokens, err
}

// Reconnect gives an existing agent of the client behind a code new tokens at once; they
// wait for the agent's next poll. The agent must be one Check offered.
func (s *Server) Reconnect(ctx context.Context, session string, r PairingReconnect) (agent.Agent, error) {
	key, g, err := s.grantForCode(ctx, session, r.Code)
	if err != nil {
		return agent.Agent{}, err
	}
	if !equalSecret(g.id, r.PairingID) {
		return agent.Agent{}, ErrPairingConflict
	}
	if !s.offered(ctx, g.client, r.ClientID) {
		return agent.Agent{}, fmt.Errorf("%w: %w", ErrPairingAdmission, agent.ErrNotReconnectable)
	}
	return s.admitGrant(ctx, key, g.id, decision{reconnect: r.ClientID, by: r.By})
}

// refusedReconnect tells errors of a reconnection that are the state, not a failure.
func refusedReconnect(err error) bool {
	return errors.Is(err, agent.ErrNotReconnectable) || errors.Is(err, agent.ErrRevoked) || errors.Is(err, agent.ErrNotFound)
}
