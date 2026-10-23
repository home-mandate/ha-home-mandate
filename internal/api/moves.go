// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// wireAreaMove is a device that left or entered an area that rules of active mandates
// name: those rules stopped or started applying to it (SPEC-v0 section 3.4).
type wireAreaMove struct {
	EntityID string   `json:"entity_id"`
	Name     string   `json:"name"`
	Previous []string `json:"previous"`
	Current  []string `json:"current"`
	MovedAt  string   `json:"moved_at"`
	// Mandates are the active mandates with a rule on an area the device left or entered.
	Mandates []wireMovedMandate `json:"mandates"`
}

type wireMovedMandate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Rules are the IDs of the rules on such an area.
	Rules []string `json:"rules"`
}

// areaRule is a rule of an active mandate that names an area.
type areaRule struct {
	mandateID, mandateName, ruleID, area string
}

// areaRules returns the rules of the active mandates that name an area, in the order of
// the mandates and their rules.
func (s *Server) areaRules(ctx context.Context) ([]areaRule, error) {
	list, err := s.cfg.Mandates.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("area moves: %w", err)
	}
	var out []areaRule
	for _, m := range list {
		if m.Status != mandate.StatusActive {
			continue
		}
		info, doc, err := s.cfg.Mandates.Current(ctx, m.ID)
		if err != nil {
			return nil, fmt.Errorf("area moves: mandate %s: %w", m.ID, err)
		}
		var d ruleResources
		if err := json.Unmarshal(doc, &d); err != nil {
			return nil, fmt.Errorf("area moves: mandate %s: %w", m.ID, err)
		}
		for _, r := range d.Rules {
			if r.Resource.Area != "" {
				out = append(out, areaRule{mandateID: info.ID, mandateName: nameOf(info), ruleID: r.ID, area: r.Resource.Area})
			}
		}
	}
	return out, nil
}

// affectedBy groups the rules on one of areas by mandate.
func affectedBy(rules []areaRule, areas []string) []wireMovedMandate {
	out := []wireMovedMandate{}
	for _, r := range rules {
		if !slices.Contains(areas, r.area) {
			continue
		}
		if n := len(out); n > 0 && out[n-1].ID == r.mandateID {
			out[n-1].Rules = append(out[n-1].Rules, r.ruleID)
			continue
		}
		out = append(out, wireMovedMandate{ID: r.mandateID, Name: r.mandateName, Rules: []string{r.ruleID}})
	}
	return out
}

// getAreaMoves lists the moves that active mandates are affected by. Others are not
// shown: no rule applies differently because of them.
func (s *Server) getAreaMoves(r *request) (any, error) {
	out := []wireAreaMove{}
	if s.cfg.AreaMoves == nil {
		return out, nil
	}
	moves := s.cfg.AreaMoves.Open()
	if len(moves) == 0 {
		return out, nil
	}
	rules, err := s.areaRules(r.Context())
	if err != nil {
		return nil, err
	}
	for _, mv := range moves {
		mandates := affectedBy(rules, mv.Changed())
		if len(mandates) == 0 {
			continue
		}
		name := mv.EntityID
		if d, ok := s.cfg.Catalog.Lookup(mv.EntityID); ok && d.Name() != "" {
			name = d.Name()
		}
		out = append(out, wireAreaMove{EntityID: mv.EntityID, Name: name, Previous: mv.Previous, Current: mv.Current,
			MovedAt: mv.MovedAt.UTC().Format(timeFormat), Mandates: mandates})
	}
	return out, nil
}

// dismissAreaMove ends a move after a human saw it: entity_id and its current areas as
// shown, so that a later move is not dismissed unseen.
func (s *Server) dismissAreaMove(r *request) (any, error) {
	var in struct {
		EntityID *string  `json:"entity_id"`
		Current  []string `json:"current"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.EntityID == nil {
		return nil, failField(codeInvalidInput, "/entity_id")
	}
	if in.Current == nil {
		return nil, failField(codeInvalidInput, "/current")
	}
	if s.cfg.AreaMoves == nil {
		return nil, fail(codeNotFound)
	}
	err := s.cfg.AreaMoves.Dismiss(r.Context(), *in.EntityID, in.Current)
	if errors.Is(err, catalog.ErrMoveChanged) {
		return nil, fail(codeConflict)
	}
	if err != nil {
		s.cfg.Logger.Error("area move not dismissed", "entity_id", *in.EntityID, "by", r.user, "error", err)
		return nil, err
	}
	s.cfg.Logger.Info("area move dismissed", "entity_id", *in.EntityID, "current", in.Current, "by", r.user)
	s.publish(event{Type: "devices.changed"})
	return nil, nil
}
