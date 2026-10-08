// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"strconv"

	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// wireStaleReference is a rule that names a device or area Home Assistant does not have
// (any more): renamed or removed. Such a rule applies to nothing; a deny or ask rule on a
// renamed device no longer protects it (decision H-E1: report, never rewrite).
type wireStaleReference struct {
	// Rule is the index of the rule in the mandate.
	Rule     int     `json:"rule"`
	RuleID   string  `json:"rule_id"`
	EntityID *string `json:"entity_id,omitempty"`
	// RenamedTo is the entity the device was renamed to, while nobody resolved the rename:
	// the rule keeps applying to it until then.
	RenamedTo *string `json:"renamed_to,omitempty"`
	Area      *string `json:"area,omitempty"`
}

// ruleResources are the resources of a document's rules.
type ruleResources struct {
	Rules []struct {
		ID       string `json:"id"`
		Resource struct {
			EntityID string `json:"entity_id"`
			Category string `json:"category"`
			Area     string `json:"area"`
		} `json:"resource"`
	} `json:"rules"`
}

// staleReferences lists the rules of an active mandate that name a device or area the
// catalog does not have. Without a loaded catalog it cannot tell and lists none.
func (s *Server) staleReferences(info mandate.Info, document []byte) []wireStaleReference {
	out := []wireStaleReference{}
	if info.Status != mandate.StatusActive || !s.cfg.Catalog.Ready() {
		return out
	}
	var doc ruleResources
	if json.Unmarshal(document, &doc) != nil {
		return out
	}
	renamedTo := map[string]string{}
	for current, formers := range s.cfg.Renames.Open() {
		for _, f := range formers {
			renamedTo[f] = current
		}
	}
	areas := map[string]bool{}
	for _, a := range s.cfg.Catalog.Areas() {
		areas[a.ID] = true
	}
	for i, r := range doc.Rules {
		ref := wireStaleReference{Rule: i, RuleID: r.ID}
		if id := r.Resource.EntityID; id != "" {
			if _, ok := s.cfg.Catalog.Lookup(id); !ok {
				ref.EntityID = &id
				if to, renamed := renamedTo[id]; renamed {
					ref.RenamedTo = &to
				}
			}
		}
		if area := r.Resource.Area; area != "" && !areas[area] {
			ref.Area = &area
		}
		if ref.EntityID != nil || ref.Area != nil {
			out = append(out, ref)
		}
	}
	return out
}

// checkResources refuses a rule that names a device together with a category the device
// does not have: the PEP takes the category from the catalog, so the rule could never
// apply. A device the catalog does not know is no error; the mandate reports it as a
// stale reference. Without a loaded catalog there is nothing to check against.
func (s *Server) checkResources(document []byte, prefix string) error {
	if !s.cfg.Catalog.Ready() {
		return nil
	}
	var doc ruleResources
	if err := json.Unmarshal(document, &doc); err != nil {
		return failField(codeInvalidMandate, prefix)
	}
	for i, r := range doc.Rules {
		if r.Resource.EntityID == "" || r.Resource.Category == "" {
			continue
		}
		if d, ok := s.cfg.Catalog.Lookup(r.Resource.EntityID); ok && d.Category != r.Resource.Category {
			return failField(codeInvalidMandate, prefix+"/rules/"+strconv.Itoa(i)+"/resource/category")
		}
	}
	return nil
}
