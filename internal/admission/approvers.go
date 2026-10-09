// SPDX-License-Identifier: AGPL-3.0-or-later

package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/home-mandate/spec/vocabulary"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// Approvers says who may answer the approval requests of a mandate made from a template
// now, and which requests its rules can lead to. It is a display aid for the human who
// admits an agent (the consent page, the pairing in the UI): the mandate keeps the list
// as a snapshot, and what is asked is decided by the evaluation.
type Approvers struct {
	// People are everyone named, in document order, each once: the mandate's approvers,
	// then those of the rules, with the approvers placeholder standing for the admitting
	// human and the approvers set up in Home-Mandate.
	People []string
	// Normal are the approver lists of the rules that can ask for ordinary actions; empty
	// if the template asks for none.
	Normal [][]string
	// Critical are the approver lists that can get requests for critical actions (SPEC-v0
	// section 5): ask rules on critical actions, and the mandate's approvers when an allow
	// rule without allow_critical names one (it becomes ask). Resources the household
	// marks as critical are not known here.
	Critical [][]string
}

// ApproversFor returns who may approve when by admits an agent with the template now. A
// hidden base template or an unknown one is ErrTemplateNotFound, as at admission; a
// placeholder without anyone yields empty lists instead of an error, so that the human
// sees that nobody would be asked.
func (s *Store) ApproversFor(ctx context.Context, template string, by audit.Actor) (Approvers, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Approvers{}, fmt.Errorf("admission: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Only shown, never stored: the separate confirmation is the admission's to ask for.
	document, err := s.templateTx(ctx, tx, template, true)
	if err != nil {
		return Approvers{}, err
	}
	people, err := s.people(ctx, by)
	if err != nil {
		return Approvers{}, err
	}
	return approversOf(document, people)
}

type approvalSettings struct {
	Approvers []string `json:"approvers"`
}

// approversOf reads the approver lists of a template document.
func approversOf(document []byte, people []string) (Approvers, error) {
	var d struct {
		Approval *approvalSettings `json:"approval"`
		Rules    []struct {
			Resource struct {
				Category string `json:"category"`
			} `json:"resource"`
			Actions       []string          `json:"actions"`
			Decision      string            `json:"decision"`
			Approval      *approvalSettings `json:"approval"`
			AllowCritical bool              `json:"allow_critical"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(document, &d); err != nil {
		return Approvers{}, fmt.Errorf("admission: approvers: %w", err)
	}
	var out Approvers
	seen := map[string]bool{}
	expand := func(a *approvalSettings) []string {
		if a == nil {
			return nil
		}
		list := []string{}
		for _, v := range a.Approvers {
			names := []string{v}
			if v == mandate.ApproversPlaceholder {
				names = people
			}
			for _, n := range names {
				if n != "" && !slices.Contains(list, n) {
					list = append(list, n)
				}
				if n != "" && !seen[n] {
					seen[n] = true
					out.People = append(out.People, n)
				}
			}
		}
		return list
	}
	general := expand(d.Approval)
	if general == nil {
		general = []string{}
	}
	demoted := false
	for _, r := range d.Rules {
		own := expand(r.Approval)
		list := general
		if own != nil {
			list = own
		}
		switch r.Decision {
		case "ask":
			normal, critical := asks(r.Resource.Category, r.Actions)
			if normal {
				out.Normal = append(out.Normal, list)
			}
			if critical {
				out.Critical = append(out.Critical, list)
			}
		case "allow":
			if _, critical := asks(r.Resource.Category, r.Actions); critical && !r.AllowCritical {
				demoted = true
			}
		}
	}
	if demoted {
		out.Critical = append(out.Critical, general)
	}
	return out, nil
}

// asks tells whether a rule on category (empty: any category, as for a rule on an area
// or a device) with actions covers ordinary and critical actions of the vocabulary.
// Anything the vocabulary does not know counts as critical.
func asks(category string, actions []string) (normal, critical bool) {
	vocab := criticalActions()
	for _, a := range actions {
		for c, list := range vocab {
			if category != "" && c != category {
				continue
			}
			if a == "*" {
				normal = true
				for _, crit := range list {
					critical = critical || crit
				}
				continue
			}
			if crit, ok := list[a]; ok {
				normal, critical = normal || !crit, critical || crit
			}
		}
		if a != "*" && !known(vocab, category, a) {
			critical = true
		}
		if a == "*" && (category == "" || vocab[category] == nil) {
			critical = true
		}
	}
	return normal, critical
}

func known(vocab map[string]map[string]bool, category, action string) bool {
	for c, list := range vocab {
		if _, ok := list[action]; ok && (category == "" || c == category) {
			return true
		}
	}
	return false
}

// criticalActions reads the vocabulary of SPEC-v0 section 5: category → action →
// critical. An unreadable vocabulary is empty, so that every action counts as critical.
var criticalActions = sync.OnceValue(func() map[string]map[string]bool {
	var doc struct {
		Categories map[string]struct {
			Actions map[string]struct {
				Critical bool `json:"critical"`
			} `json:"actions"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(vocabulary.V0(), &doc); err != nil {
		return nil
	}
	out := make(map[string]map[string]bool, len(doc.Categories))
	for c, cat := range doc.Categories {
		out[c] = make(map[string]bool, len(cat.Actions))
		for a, info := range cat.Actions {
			out[c][a] = info.Critical
		}
	}
	return out
})
