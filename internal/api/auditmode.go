// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/untrusted"
)

// wirePending is a version of a mandate that switches audit mode on and waits for a
// human's confirmation (SPEC-v0 section 11.6 item 4): the confirmation names its digest.
type wirePending struct {
	Digest         string          `json:"digest"`
	Document       json.RawMessage `json:"document"`
	ProposedBy     string          `json:"proposed_by"`
	ProposedByName *string         `json:"proposed_by_name"`
	ProposedAt     string          `json:"proposed_at"`
}

// enforced tells whether a stored document enforces its denials: false only for
// "enforce": false (audit mode, SPEC-v0 section 4.6).
func enforced(doc []byte) bool {
	var d struct {
		Enforce *bool `json:"enforce"`
	}
	return json.Unmarshal(doc, &d) != nil || d.Enforce == nil || *d.Enforce
}

// pendingOf returns the waiting version of mandate id for the UI; nil if none waits.
func (s *Server) pendingOf(ctx context.Context, id string) (*wirePending, error) {
	p, ok, err := s.cfg.Mandates.Pending(ctx, id)
	if err != nil || !ok {
		return nil, err
	}
	return &wirePending{Digest: p.Digest, Document: p.Document, ProposedBy: p.ProposedBy,
		ProposedByName: s.users.name(ctx, p.ProposedBy), ProposedAt: *formatTime(p.ProposedAt)}, nil
}

// confirmAuditMode lets the waiting version take effect after a human confirmed exactly
// it: the digest the UI showed. Every approver of the mandate is told.
func (s *Server) confirmAuditMode(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Digest string `json:"digest"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if !digestPattern.MatchString(in.Digest) {
		return nil, failField(codeInvalidInput, "/digest")
	}
	info, err := s.cfg.Mandates.ConfirmAuditMode(r.Context(), id, in.Digest, s.actor(r))
	if err != nil {
		return nil, mandateError(err, "", nil)
	}
	s.cfg.Logger.Warn("audit mode confirmed", "mandate", id, "digest", in.Digest, "by", r.user)
	if _, doc, err := s.cfg.Mandates.Current(r.Context(), id); err == nil {
		s.tellApprovers(r.Context(), info, doc, i18n.AuditModeActiveTitle, i18n.AuditModeActiveMessage, r.user)
	}
	s.publish(event{Type: "mandates.changed", ID: id})
	return s.detail(r.Context(), id)
}

// discardAuditMode drops the waiting version; the version in force stays.
func (s *Server) discardAuditMode(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Mandates.DiscardAuditMode(r.Context(), id); err != nil {
		return nil, mandateError(err, "", nil)
	}
	s.cfg.Logger.Info("audit mode proposal discarded", "mandate", id, "by", r.user)
	s.publish(event{Type: "mandates.changed", ID: id})
	return s.detail(r.Context(), id)
}

// proposed tells every approver of the mandate that switching audit mode on was
// proposed and waits for a confirmation.
func (s *Server) proposed(ctx context.Context, id, by string) {
	info, err := s.cfg.Mandates.Get(ctx, id)
	if err != nil {
		return
	}
	p, ok, err := s.cfg.Mandates.Pending(ctx, id)
	if err != nil || !ok {
		return
	}
	info.Digest = p.Digest
	s.cfg.Logger.Warn("audit mode proposed", "mandate", id, "digest", p.Digest, "by", by)
	s.tellApprovers(ctx, info, p.Document, i18n.AuditModeProposedTitle, i18n.AuditModeProposedMessage, by)
}

// tellApprovers sends a notification to every device of every approver the document
// names (its approval settings and those of its ask rules), each in its language
// (SPEC-v0 section 11.6 item 4). One that reaches nobody is logged.
func (s *Server) tellApprovers(ctx context.Context, info mandate.Info, doc []byte, title, message i18n.Key, by string) {
	ids := approversOf(doc)
	byName := by
	if n := s.users.name(ctx, by); n != nil {
		byName = *n
	}
	// Names come from humans and Home Assistant: cleaned as everywhere they are shown.
	args := i18n.Args{"mandate": untrusted.Clean(nameOf(info), untrusted.Max), "by": untrusted.Clean(byName, untrusted.Max),
		"digest": shortDigest(info.Digest)}
	reached := 0
	if s.cfg.Approvers != nil && s.cfg.HA != nil {
		approvers, err := s.cfg.Approvers.List(ctx)
		if err != nil {
			s.cfg.Logger.Error("approvers not read for the audit mode notice", "mandate", info.ID, "error", err)
		}
		for _, ap := range approvers {
			if !slices.Contains(ids, ap.UserID) {
				continue
			}
			lang := s.language()
			if l, ok := i18n.Parse(ap.Language); ok {
				lang = l
			}
			n := ha.Notification{Title: i18n.T(lang, title, nil), Message: i18n.T(lang, message, args)}
			for _, d := range ap.Devices {
				if err := s.cfg.HA.Notify(ctx, d.Service, n); err == nil {
					reached++
				}
			}
		}
	}
	if reached == 0 {
		s.cfg.Logger.Warn("no approver device reached for the audit mode notice", "mandate", info.ID, "approvers", len(ids))
	}
}

// language is the household's language for notifications; English if it has none of ours.
func (s *Server) language() i18n.Lang {
	if l, ok := i18n.Parse(s.cfg.Status().Language); ok {
		return l
	}
	return i18n.EN
}

// approversOf returns the user IDs of the approvers a document names.
func approversOf(doc []byte) []string {
	var d struct {
		Approval struct {
			Approvers []string `json:"approvers"`
		} `json:"approval"`
		Rules []struct {
			Approval *struct {
				Approvers []string `json:"approvers"`
			} `json:"approval"`
		} `json:"rules"`
	}
	if json.Unmarshal(doc, &d) != nil {
		return nil
	}
	ids := slices.Clone(d.Approval.Approvers)
	for _, r := range d.Rules {
		if r.Approval != nil {
			ids = append(ids, r.Approval.Approvers...)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// shortDigest is the beginning of a digest, enough for a human to compare it.
func shortDigest(digest string) string {
	const shown = len("sha256:") + 12
	if len(digest) <= shown {
		return digest
	}
	return digest[:shown]
}
