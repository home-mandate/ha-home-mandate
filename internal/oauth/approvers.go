// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"

	reach "github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// ApproverPreview tells who may approve the requests of a mandate made from a template
// if a human admits an agent now, and whether anyone can be reached (internal/api).
type ApproverPreview interface {
	ApproversPreview(ctx context.Context, template string, by audit.Actor) (reach.ReachPreview, error)
}

// SetApprovers names where the consent page learns who may approve. Without it, the page
// does not show approvers.
func (s *Server) SetApprovers(p ApproverPreview) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvers = p
}

func (s *Server) approverPreview() ApproverPreview {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvers
}

// consentPerson is one person who may approve, as the consent page shows them. Name
// comes from Home Assistant and is escaped like every value; empty means unknown.
type consentPerson struct {
	Name  string
	Self  bool
	Notes []i18n.Key
}

// consentApprovers is who may approve after an admission with a template, and the
// warnings: nobody reachable for ordinary or for critical requests, or unknown because
// Home Assistant could not be asked (never shown as reachable). Nothing blocks the
// admission; the human decides.
type consentApprovers struct {
	People                          []consentPerson
	Nobody, NobodyCritical, Unknown bool
}

// withApprovers adds to each template who may approve when user admits with it.
func (s *Server) withApprovers(ctx context.Context, templates []consentTemplate, user string) []consentTemplate {
	preview := s.approverPreview()
	if preview == nil {
		return templates
	}
	out := make([]consentTemplate, len(templates))
	for i, t := range templates {
		p, err := preview.ApproversPreview(ctx, t.Name, audit.Actor{Kind: audit.ActorUser, ID: user})
		if err != nil {
			s.cfg.Logger.Warn("approvers of a template unknown", "template", t.Name, "error", err)
			t.Approvers = &consentApprovers{Unknown: true}
			out[i] = t
			continue
		}
		t.Approvers = approversShown(p, user)
		out[i] = t
	}
	return out
}

func approversShown(p reach.ReachPreview, user string) *consentApprovers {
	out := &consentApprovers{People: make([]consentPerson, 0, len(p.People)),
		Nobody:         p.Normal == reach.CoverageNobody,
		NobodyCritical: p.Critical == reach.CoverageNobody,
		Unknown:        p.Normal == reach.CoverageUnknown || p.Critical == reach.CoverageUnknown}
	for _, person := range p.People {
		out.People = append(out.People, consentPerson{Name: person.Name, Self: person.UserID == user, Notes: notesOf(person)})
	}
	return out
}

// notesOf marks how a person is (not) reached.
func notesOf(p reach.PersonReach) []i18n.Key {
	switch {
	case p.Service:
		return []i18n.Key{i18n.PageConsentApproverService}
	case p.Normal == reach.ReachUnknown || p.Critical == reach.ReachUnknown:
		return []i18n.Key{i18n.PageConsentApproverUnknown}
	case p.Normal == reach.ReachNone && p.Critical == reach.ReachNone:
		return []i18n.Key{i18n.PageConsentApproverNone}
	}
	var notes []i18n.Key
	if p.Normal == reach.ReachUI {
		notes = append(notes, i18n.PageConsentApproverUI)
	}
	if p.Critical == reach.ReachNone {
		notes = append(notes, i18n.PageConsentApproverNoCritical)
	}
	return notes
}
