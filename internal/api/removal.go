// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/oauth"
	"github.com/home-mandate/ha-home-mandate/internal/removal"
)

// Removing revoked agents and mandates (issue #21) and reconnecting an agent in the
// pairing step (issue #22).

// wireRemoved counts what "Remove all revoked" removed.
type wireRemoved struct {
	Agents   int `json:"agents"`
	Mandates int `json:"mandates"`
}

func (s *Server) removal() *removal.Service {
	return removal.New(s.cfg.Store.DB(), s.cfg.Agents, s.cfg.Mandates)
}

// removalError maps refusals: only revoked ones are removed.
func removalError(err error) error {
	switch {
	case errors.Is(err, agent.ErrNotFound), errors.Is(err, mandate.ErrNotFound):
		return fail(codeNotFound)
	case errors.Is(err, agent.ErrNotRevoked), errors.Is(err, mandate.ErrNotRevoked):
		return fail(codeConflict)
	}
	return err
}

// removeAgent removes a revoked agent (POST api/agents/remove). With revoke, an active
// agent is revoked first in the same transaction (leftovers after an emergency stop);
// with mandates, its mandates are removed too, each recorded on its own.
func (s *Server) removeAgent(r *request) (any, error) {
	var in struct {
		ClientID string `json:"client_id"`
		Revoke   bool   `json:"revoke"`
		Mandates bool   `json:"mandates"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.ClientID == "" || len(in.ClientID) > 256 {
		return nil, failField(codeInvalidInput, "/client_id")
	}
	ctx := r.Context()
	res, err := s.removal().RemoveAgent(ctx, in.ClientID, removal.AgentOptions{Revoke: in.Revoke, Mandates: in.Mandates}, s.actor(r))
	if err != nil {
		return nil, removalError(err)
	}
	if res.Revoked {
		if n := s.cfg.Approvals.CancelAgent(in.ClientID); n > 0 {
			s.cfg.Logger.Info("open approval requests of a revoked agent ended", "client_id", in.ClientID, "requests", n)
		}
	}
	s.publish(event{Type: "agents.changed"})
	if in.Revoke || len(res.Mandates) > 0 {
		s.publish(event{Type: "mandates.changed"})
	}
	a, err := s.cfg.Agents.Get(ctx, in.ClientID)
	if err != nil {
		return nil, err
	}
	return s.presentAgent(ctx, a), nil
}

// removeMandate removes a revoked mandate (POST api/mandates/{id}/remove).
func (s *Server) removeMandate(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	if err := s.removal().RemoveMandate(r.Context(), id, s.actor(r)); err != nil {
		return nil, removalError(err)
	}
	s.publish(event{Type: "mandates.changed", ID: id})
	s.publish(event{Type: "agents.changed"})
	info, doc, err := s.cfg.Mandates.Current(r.Context(), id)
	if err != nil {
		return nil, err
	}
	uses, err := s.cfg.Mandates.TemplateUses(r.Context())
	if err != nil {
		return nil, err
	}
	return s.summary(info, doc, uses)
}

// removeRevoked removes every revoked agent with its mandates and every other revoked
// mandate (POST api/revoked/remove).
func (s *Server) removeRevoked(r *request) (any, error) {
	res, err := s.removal().RemoveRevoked(r.Context(), s.actor(r))
	if len(res.Agents)+len(res.Mandates) > 0 {
		s.publish(event{Type: "agents.changed"})
		s.publish(event{Type: "mandates.changed"})
	}
	if err != nil {
		return nil, err
	}
	return wireRemoved{Agents: len(res.Agents), Mandates: len(res.Mandates)}, nil
}

// pairingReconnect gives an existing agent that the check offered new tokens instead of
// admitting the agent behind the code anew (POST api/pairing/reconnect).
func (s *Server) pairingReconnect(r *request) (any, error) {
	p, err := s.pairing()
	if err != nil {
		return nil, err
	}
	var in struct {
		pairingDecision
		ClientID string `json:"client_id"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if err := in.check(); err != nil {
		return nil, err
	}
	if in.ClientID == "" || len(in.ClientID) > 256 {
		return nil, failField(codeInvalidInput, "/client_id")
	}
	a, err := p.Reconnect(r.Context(), r.user, oauth.PairingReconnect{Code: in.Code, PairingID: in.PairingID, ClientID: in.ClientID, By: r.user})
	if err != nil {
		if errors.Is(err, oauth.ErrPairingAdmission) && (errors.Is(err, agent.ErrNotReconnectable) ||
			errors.Is(err, agent.ErrRevoked) || errors.Is(err, agent.ErrNotFound)) {
			return nil, fail(codeConflict)
		}
		return nil, pairingError(err)
	}
	s.publish(event{Type: "agents.changed"})
	return s.presentAgent(r.Context(), a), nil
}
