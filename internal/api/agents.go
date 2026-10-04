// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

type wireAgentMandate struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Status            string `json:"status"`
	MaxActionsPerHour *int   `json:"max_actions_per_hour"`
	Digest            string `json:"digest"`
}

type wireAgent struct {
	ClientID        string            `json:"client_id"`
	DisplayName     string            `json:"display_name"`
	Status          string            `json:"status"`
	CreatedAt       string            `json:"created_at"`
	CreatedBy       string            `json:"created_by"`
	CreatedByName   *string           `json:"created_by_name"`
	LastActiveAt    *string           `json:"last_active_at"`
	OAuthClient     string            `json:"oauth_client"`
	ClientVerified  bool              `json:"client_verified"`
	RedirectURIs    []string          `json:"redirect_uris"`
	RevokedAt       *string           `json:"revoked_at"`
	RevokedByName   *string           `json:"revoked_by_name"`
	RequestsToday   int               `json:"requests_today"`
	ActionsLastHour int               `json:"actions_last_hour"`
	Mandate         *wireAgentMandate `json:"mandate"`
}

func (s *Server) getAgents(r *request) (any, error) {
	list, err := s.cfg.Agents.List(r.Context())
	if err != nil {
		return nil, err
	}
	return s.presentAgents(r.Context(), list)
}

// householdLocation is the household's time zone, UTC until Home Assistant told it.
func (s *Server) householdLocation() *time.Location {
	if tz := s.cfg.Status().TimeZone; tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.UTC
}

// presentAgents adds what the UI shows to agents: who admitted and revoked them, their
// activity from the audit log (the household's day, the last hour) and their mandate.
func (s *Server) presentAgents(ctx context.Context, list []agent.Agent) ([]wireAgent, error) {
	now := s.now().In(s.householdLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()) // DST-safe: midnight in the zone
	activity, err := s.cfg.Log.Activities(ctx, dayStart, now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	mandates, err := s.cfg.Mandates.List(ctx)
	if err != nil {
		return nil, err
	}
	// The current mandate per agent: the active one, else the newest (List is in creation order).
	current := map[string]mandate.Info{}
	for _, m := range mandates {
		if prev, ok := current[m.ClientID]; !ok || prev.Status != mandate.StatusActive {
			current[m.ClientID] = m
		}
	}
	out := make([]wireAgent, 0, len(list))
	for _, a := range list {
		w := wireAgent{ClientID: a.ClientID, DisplayName: a.DisplayName, Status: a.Status, CreatedAt: *formatTime(a.CreatedAt),
			CreatedBy: a.CreatedBy, CreatedByName: s.users.name(ctx, a.CreatedBy), OAuthClient: a.OAuthClient,
			ClientVerified: a.ClientVerified, RedirectURIs: a.RedirectURIs, RevokedAt: formatTime(a.RevokedAt)}
		if w.RedirectURIs == nil {
			w.RedirectURIs = []string{}
		}
		if a.Status == agent.StatusRevoked {
			w.RevokedByName = s.users.name(ctx, a.RevokedBy)
		}
		if act, ok := activity[a.ClientID]; ok {
			w.LastActiveAt, w.RequestsToday, w.ActionsLastHour = formatTime(act.LastAt), act.Since, act.Counted
		}
		if m, ok := current[a.ClientID]; ok {
			limit := m.MaxActionsPerHour
			w.Mandate = &wireAgentMandate{ID: m.ID, Name: nameOf(m), Status: m.Status, MaxActionsPerHour: &limit, Digest: m.Digest}
		}
		out = append(out, w)
	}
	return out, nil
}

// nameOf is a mandate's display name; the ID for mandates without one (imports).
func nameOf(m mandate.Info) string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

func (s *Server) actor(r *request) audit.Actor {
	return audit.Actor{Kind: audit.ActorUser, ID: r.user}
}

// revokeAgent revokes the agent, its tokens and its mandate in one transaction, then
// ends its open approval requests (decision F1). A repeated revoke answers like the first.
func (s *Server) revokeAgent(r *request) (any, error) {
	var in struct {
		ClientID string `json:"client_id"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.ClientID == "" || len(in.ClientID) > 256 {
		return nil, failField(codeInvalidInput, "/client_id")
	}
	ctx := r.Context()
	tx, err := s.cfg.Store.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("revoke: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.cfg.Agents.RevokeTx(ctx, tx, in.ClientID, s.actor(r)); err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			return nil, fail(codeNotFound)
		}
		return nil, err
	}
	if err := s.cfg.Mandates.RevokeAgentTx(ctx, tx, in.ClientID, s.actor(r)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("revoke: commit: %w", err)
	}
	if n := s.cfg.Approvals.CancelAgent(in.ClientID); n > 0 {
		s.cfg.Logger.Info("open approval requests of a revoked agent ended", "client_id", in.ClientID, "requests", n)
	}
	s.publish(event{Type: "agents.changed"})
	a, err := s.cfg.Agents.Get(ctx, in.ClientID)
	if err != nil {
		return nil, err
	}
	return s.presentAgent(ctx, a), nil
}

// presentAgent is presentAgents for one agent after a change that is done: if its
// activity or mandate cannot be read now, the agent is answered without them rather
// than with an error that would say the change failed.
func (s *Server) presentAgent(ctx context.Context, a agent.Agent) wireAgent {
	list, err := s.presentAgents(ctx, []agent.Agent{a})
	if err == nil {
		return list[0]
	}
	s.cfg.Logger.Warn("agent answered without activity and mandate", "error", err)
	uris := a.RedirectURIs
	if uris == nil {
		uris = []string{}
	}
	return wireAgent{ClientID: a.ClientID, DisplayName: a.DisplayName, Status: a.Status, CreatedAt: *formatTime(a.CreatedAt),
		CreatedBy: a.CreatedBy, OAuthClient: a.OAuthClient, ClientVerified: a.ClientVerified, RedirectURIs: uris,
		RevokedAt: formatTime(a.RevokedAt)}
}
