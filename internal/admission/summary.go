// SPDX-License-Identifier: AGPL-3.0-or-later

package admission

import (
	"encoding/json"
	"fmt"
)

// Summary says in plain terms what a template or mandate allows, where it asks and what
// it never allows, rule by rule (ARCHITECTURE section 6). It is a display aid: what an
// agent may do is decided by the evaluation, which also knows the order of rules. The
// rest is always forbidden (default deny).
type Summary struct {
	Allow, Ask, Deny []SummaryLine
}

// SummaryLine is one rule: what it is about and which actions.
type SummaryLine struct {
	Any            bool   // every device
	Category, Area string // either or both
	Entity         string // one device
	All            bool   // every action ("*")
	Actions        []string
	Conditions     bool // only under conditions (time windows, parameters)
	Critical       bool // critical actions allowed without approval
}

// Summarize reads the rules of a mandate or template document.
func Summarize(document []byte) (Summary, error) {
	var d struct {
		Rules []struct {
			Resource struct {
				Any      bool   `json:"any"`
				Category string `json:"category"`
				Area     string `json:"area"`
				Entity   string `json:"entity_id"`
			} `json:"resource"`
			Actions       []string        `json:"actions"`
			Decision      string          `json:"decision"`
			Conditions    json.RawMessage `json:"conditions"`
			AllowCritical bool            `json:"allow_critical"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(document, &d); err != nil {
		return Summary{}, fmt.Errorf("admission: summary: %w", err)
	}
	var s Summary
	for _, r := range d.Rules {
		line := SummaryLine{Any: r.Resource.Any, Category: r.Resource.Category, Area: r.Resource.Area, Entity: r.Resource.Entity,
			Conditions: len(r.Conditions) > 0, Critical: r.AllowCritical}
		for _, a := range r.Actions {
			if a == "*" {
				line.All = true
			}
		}
		if !line.All {
			line.Actions = r.Actions
		}
		switch r.Decision {
		case "allow":
			s.Allow = append(s.Allow, line)
		case "ask":
			s.Ask = append(s.Ask, line)
		case "deny":
			s.Deny = append(s.Deny, line)
		default:
			return Summary{}, fmt.Errorf("admission: summary: unknown decision %q", r.Decision)
		}
	}
	return s, nil
}
