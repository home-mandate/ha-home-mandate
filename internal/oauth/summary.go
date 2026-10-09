// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"strings"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// consentTemplate is a template as the consent page shows it: what the human chooses
// from, in plain words (ARCHITECTURE section 6).
type consentTemplate struct {
	Name, Title, Description string
	Digest                   string // bound to the choice: the human approves what they saw
	Base                     bool
	Allow, Ask, Deny         []string
	Approvers                *consentApprovers // who may approve; nil when not known here
}

// consentTemplates lists the templates a human may choose at admission: hidden base
// templates are left out.
func (s *Server) consentTemplates(ctx context.Context, lang i18n.Lang) ([]consentTemplate, error) {
	list, err := s.cfg.Admission.Templates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]consentTemplate, 0, len(list))
	for _, t := range list {
		if t.Hidden {
			continue
		}
		doc, full, err := s.cfg.Admission.TemplateDocument(ctx, t.Name)
		if err != nil {
			return nil, err
		}
		// A template that grants critical actions without approval needs a separate
		// confirmation (decision U9), which this page does not ask for: it is admitted
		// through the UI, which does.
		if critical, err := mandate.NewCriticalGrant(nil, doc); err != nil || critical {
			continue
		}
		sum, err := admission.Summarize(doc)
		if err != nil {
			return nil, err
		}
		c := consentTemplate{Name: t.Name, Title: t.Name, Base: t.Builtin, Digest: full.Digest,
			Allow: summaryLines(lang, sum.Allow), Ask: summaryLines(lang, sum.Ask), Deny: summaryLines(lang, sum.Deny)}
		if t.Builtin {
			c.Title, c.Description = t.Title[string(lang)], t.Description[string(lang)]
		}
		out = append(out, c)
	}
	return out, nil
}

func summaryLines(lang i18n.Lang, lines []admission.SummaryLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = summaryText(lang, l)
	}
	return out
}

// summaryText puts one rule in words, e.g. "Lights in area kitchen: switch on, switch off
// (only under conditions)".
func summaryText(lang i18n.Lang, l admission.SummaryLine) string {
	var subject string
	switch {
	case l.Any:
		subject = i18n.T(lang, i18n.SummaryAnyDevice, nil)
	case l.Entity != "":
		subject = i18n.T(lang, i18n.SummaryDevice, i18n.Args{"entity": l.Entity})
	case l.Category != "" && l.Area != "":
		subject = i18n.T(lang, i18n.SummaryInArea, i18n.Args{"subject": i18n.T(lang, i18n.Key("category_"+l.Category), nil), "area": l.Area})
	case l.Category != "":
		subject = i18n.T(lang, i18n.Key("category_"+l.Category), nil)
	default:
		subject = i18n.T(lang, i18n.SummaryArea, i18n.Args{"area": l.Area})
	}
	actions := i18n.T(lang, i18n.SummaryAllActions, nil)
	if !l.All {
		names := make([]string, len(l.Actions))
		for i, a := range l.Actions {
			names[i] = i18n.T(lang, i18n.Key("action_"+a), nil)
		}
		actions = strings.Join(names, ", ")
	}
	text := i18n.T(lang, i18n.SummaryLine, i18n.Args{"subject": subject, "actions": actions})
	var notes []string
	if l.Conditions {
		notes = append(notes, i18n.T(lang, i18n.SummaryConditions, nil))
	}
	if l.Critical {
		notes = append(notes, i18n.T(lang, i18n.SummaryCritical, nil))
	}
	if len(notes) > 0 {
		text += " (" + strings.Join(notes, "; ") + ")"
	}
	return text
}
