// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"slices"
	"testing"
)

// rulesDraft is a draft with exactly these rules.
func rulesDraft(t *testing.T, rules ...map[string]any) map[string]any {
	t.Helper()
	d := draft(t)
	list := make([]any, len(rules))
	for i, r := range rules {
		list[i] = r
	}
	d["rules"] = list
	return d
}

func rule(id string, resource map[string]any, decision string, actions ...string) map[string]any {
	list := make([]any, len(actions))
	for i, a := range actions {
		list[i] = a
	}
	return map[string]any{"id": id, "resource": resource, "actions": list, "decision": decision}
}

// A rename or removal in Home Assistant leaves rules that name an ID nobody has any more
// (decision H-E1: report, never rewrite). Such a rule no longer protects the device.
func TestMandatesReportRulesOnMissingDevicesAndAreas(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	d := rulesDraft(t,
		rule("r-kitchen", map[string]any{"entity_id": "light.kitchen"}, "allow", "turn_on"),
		rule("r-cellar", map[string]any{"entity_id": "lock.cellar"}, "deny", "unlock"),
		rule("r-garden", map[string]any{"area": "garden"}, "ask", "turn_on"),
		rule("r-hall", map[string]any{"area": "hall", "category": "light"}, "allow", "turn_on"),
	)
	var detail wireMandateDetail
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, false), &detail)
	want := []wireStaleReference{{Rule: 1, RuleID: "r-cellar", EntityID: ptr("lock.cellar")}, {Rule: 2, RuleID: "r-garden", Area: ptr("garden")}}
	if !slices.EqualFunc(detail.Summary.StaleReferences, want, sameReference) {
		t.Errorf("detail = %+v", detail.Summary.StaleReferences)
	}
	var list []wireMandateSummary
	h.ok(http.MethodGet, "/api/mandates", nil, &list)
	if len(list) != 1 || !slices.EqualFunc(list[0].StaleReferences, want, sameReference) {
		t.Errorf("list = %+v", list)
	}

	// Without a loaded catalog nothing can be told; the list stays empty, never null.
	h.cat.notReady = true
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &detail)
	if detail.Summary.StaleReferences == nil || len(detail.Summary.StaleReferences) != 0 {
		t.Errorf("catalog not ready = %+v", detail.Summary.StaleReferences)
	}
	h.cat.notReady = false
	// A revoked mandate applies to nothing.
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/revoke", nil, nil)
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &detail)
	if len(detail.Summary.StaleReferences) != 0 {
		t.Errorf("revoked = %+v", detail.Summary.StaleReferences)
	}
}

// Negative catalog: a rule whose category is not the category of the device it names
// can never apply (the PEP takes the category from the catalog). Storing it is refused.
func TestRuleWithCategoryOfAnotherDeviceIsRefused(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	path := "/api/mandates/" + m.ID
	mismatch := rulesDraft(t,
		rule("r-kitchen", map[string]any{"category": "light"}, "allow", "turn_on"),
		rule("r-door", map[string]any{"entity_id": "lock.front_door", "category": "light"}, "deny", "turn_on"),
	)
	if r := h.do(http.MethodPut, path, update("Voice", mismatch, m.Digest, false)); r.code != http.StatusUnprocessableEntity ||
		r.errCode() != codeInvalidMandate || r.field() != "/draft/rules/1/resource/category" {
		t.Errorf("mismatch = %d %s", r.code, r.body)
	}
	// Matching category, or a device the catalog does not know (a warning, not an error).
	for name, resource := range map[string]map[string]any{
		"match":   {"entity_id": "lock.front_door", "category": "lock"},
		"unknown": {"entity_id": "lock.cellar", "category": "lock"},
	} {
		t.Run(name, func(t *testing.T) {
			cur := h.mandateOf(voice.ClientID)
			d := rulesDraft(t, rule("r-"+name, resource, "deny", "unlock"))
			h.ok(http.MethodPut, path, update("Voice", d, cur.Digest, false), nil)
		})
	}
	// Templates are checked the same way.
	if r := h.do(http.MethodPut, "/api/templates/doors", map[string]any{"draft": mismatch}); r.errCode() != codeInvalidMandate ||
		r.field() != "/draft/rules/1/resource/category" {
		t.Errorf("template mismatch = %d %s", r.code, r.body)
	}
	// Without a loaded catalog the check cannot run; the rule is stored and never matches
	// a device of another category anyway.
	h.cat.notReady = true
	cur := h.mandateOf(voice.ClientID)
	h.ok(http.MethodPut, path, update("Voice", mismatch, cur.Digest, false), nil)
}

func sameReference(a, b wireStaleReference) bool {
	eq := func(x, y *string) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return a.Rule == b.Rule && a.RuleID == b.RuleID && eq(a.EntityID, b.EntityID) && eq(a.Area, b.Area)
}
