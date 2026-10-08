// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// wireApproverReach is one person who may approve requests of a mandate made from a
// template: the name from Home Assistant (null if unknown) and how ordinary and critical
// requests reach them now (push, ui, none or unknown).
type wireApproverReach struct {
	UserID   string  `json:"user_id"`
	Name     *string `json:"name"`
	Normal   string  `json:"normal"`
	Critical string  `json:"critical"`
	Self     bool    `json:"self"`    // the human who admits
	Service  bool    `json:"service"` // Home-Mandate's own user: never asked
}

// wireTemplateApprovers says who may approve after an admission with a template, and
// whether ordinary and critical requests reach anyone (not_needed, reachable, nobody or
// unknown).
type wireTemplateApprovers struct {
	People   []wireApproverReach `json:"people"`
	Normal   string              `json:"normal"`
	Critical string              `json:"critical"`
}

// ApproversPreview tells who may approve the requests of a mandate made from template
// if by admits an agent now (issue: show who may approve at admission), with names from
// Home Assistant. The consent page asks it too. If Home Assistant cannot be asked, the
// people set up are unknown, never reachable.
func (s *Server) ApproversPreview(ctx context.Context, template string, by audit.Actor) (approval.ReachPreview, error) {
	expected, err := s.cfg.Admission.ApproversFor(ctx, template, by)
	if err != nil {
		return approval.ReachPreview{}, err
	}
	preview, err := s.cfg.Approvers.ReachOf(ctx, approval.Expected{People: expected.People, Normal: expected.Normal,
		Critical: expected.Critical}, s.cfg.Status().ServiceUser, s.users.IsAdmin)
	if err != nil {
		return approval.ReachPreview{}, err
	}
	people := make([]approval.PersonReach, len(preview.People))
	for i, p := range preview.People {
		if name := s.users.name(ctx, p.UserID); name != nil {
			p.Name = *name
		}
		people[i] = p
	}
	preview.People = people
	return preview, nil
}

func (s *Server) getTemplateApprovers(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	preview, err := s.ApproversPreview(r.Context(), name, s.actor(r))
	if errors.Is(err, admission.ErrTemplateNotFound) {
		return nil, fail(codeNotFound)
	}
	if err != nil {
		return nil, err
	}
	out := wireTemplateApprovers{People: make([]wireApproverReach, 0, len(preview.People)), Normal: preview.Normal,
		Critical: preview.Critical}
	for _, p := range preview.People {
		out.People = append(out.People, wireApproverReach{UserID: p.UserID, Name: optional(p.Name), Normal: p.Normal,
			Critical: p.Critical, Self: p.UserID == r.user, Service: p.Service})
	}
	return out, nil
}
