// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/home-mandate/home-mandate/internal/catalog"
)

// A rename in Home Assistant: rules on the former ID keep applying until a human takes
// the rename over into the mandates or dismisses it.
func TestRenamesAreListedAppliedAndDismissed(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	critical := rule("r-door", map[string]any{"entity_id": "lock.old_door"}, "allow", "unlock")
	critical["allow_critical"] = true
	d := rulesDraft(t,
		rule("r-kitchen", map[string]any{"entity_id": "light.old_kitchen"}, "deny", "turn_on"),
		critical,
	)
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, true), nil)
	h.renames.Hold(catalog.Rename{Old: "light.old_kitchen", New: "light.kitchen"})
	h.renames.Hold(catalog.Rename{Old: "lock.old_door", New: "lock.front_door"})
	h.renames.Hold(catalog.Rename{Old: "sensor.unused", New: "sensor.outside"}) // named by no mandate

	var list []wireRename
	h.ok(http.MethodGet, "/api/renames", nil, &list)
	if len(list) != 2 || list[0].EntityID != "light.kitchen" || list[0].Name != "Küchenlicht" || list[0].Formers[0] != "light.old_kitchen" ||
		len(list[0].Mandates) != 1 || list[0].Mandates[0].Rules[0] != "r-kitchen" || list[0].Mandates[0].Critical ||
		list[1].EntityID != "lock.front_door" || !list[1].Mandates[0].Critical {
		t.Fatalf("renames = %+v", list)
	}
	var detail wireMandateDetail
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &detail)
	if refs := detail.Summary.StaleReferences; len(refs) != 2 || refs[0].RenamedTo == nil || *refs[0].RenamedTo != "light.kitchen" {
		t.Errorf("stale references = %+v", refs)
	}

	// Dismissed: the rule on the former ID no longer applies to the light; the mandate stays.
	// Dismissing needs the explicit confirmation and the former IDs that were shown.
	if r := h.do(http.MethodPost, "/api/renames/dismiss", map[string]any{"entity_id": "light.kitchen", "formers": []string{"light.old_kitchen"}}); r.errCode() != codeInvalidInput || r.field() != "/confirm" {
		t.Errorf("dismiss without confirm = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/renames/dismiss", map[string]any{"entity_id": "light.kitchen", "formers": []string{"light.other"}, "confirm": true}); r.errCode() != codeConflict {
		t.Errorf("dismiss with other formers = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPost, "/api/renames/dismiss", map[string]any{"entity_id": "light.kitchen", "formers": []string{"light.old_kitchen"}, "confirm": true}, nil)
	if f := h.renames.Formers("light.kitchen"); len(f) != 0 {
		t.Errorf("formers after dismiss = %v", f)
	}
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &detail)
	if len(detail.Versions) != 2 {
		t.Errorf("dismiss stored a version: %d", len(detail.Versions))
	}

	// Taking over a rule that allows critical actions without approval is a new grant.
	door := []string{"lock.old_door"}
	if r := h.do(http.MethodPost, "/api/renames/apply", map[string]any{"entity_id": "lock.front_door", "formers": door}); r.errCode() != codeCriticalConfirm {
		t.Errorf("apply without confirmation = %d %s", r.code, r.body)
	}
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &detail)
	if len(detail.Versions) != 2 || len(h.renames.Formers("lock.front_door")) != 1 {
		t.Fatal("a refused apply changed something")
	}
	h.ok(http.MethodPost, "/api/renames/apply", map[string]any{"entity_id": "lock.front_door", "formers": door, "confirm_critical": true}, nil)
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &detail)
	if len(detail.Versions) != 3 || !strings.Contains(string(detail.Document), `"entity_id":"lock.front_door"`) ||
		strings.Contains(string(detail.Document), "lock.old_door") {
		t.Errorf("after apply: %d versions, %s", len(detail.Versions), detail.Document)
	}
	if f := h.renames.Formers("lock.front_door"); len(f) != 0 {
		t.Errorf("formers after apply = %v", f)
	}
	h.ok(http.MethodGet, "/api/renames", nil, &list)
	if len(list) != 0 {
		t.Errorf("renames after resolving = %+v", list)
	}

	// Refusals.
	for _, tc := range []struct {
		path string
		body any
		code string
	}{
		{"/api/renames/apply", map[string]any{"entity_id": "light.kitchen", "formers": []string{}}, codeNotFound},
		{"/api/renames/dismiss", map[string]any{"entity_id": "lock.nothing", "formers": []string{}, "confirm": true}, codeNotFound},
		{"/api/renames/apply", map[string]any{}, codeInvalidInput},
		{"/api/renames/apply", map[string]any{"entity_id": "light.kitchen"}, codeInvalidInput},
		{"/api/renames/dismiss", map[string]any{"entity_id": 7}, codeInvalidInput},
	} {
		if r := h.do(http.MethodPost, tc.path, tc.body); r.errCode() != tc.code {
			t.Errorf("%s %v = %d %s", tc.path, tc.body, r.code, r.body)
		}
	}
}

// A rename to a device whose category does not fit the rule, or to a device that is gone
// again, is not taken over.
func TestApplyNeedsTheRenamedDevice(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	d := rulesDraft(t, rule("r-x", map[string]any{"entity_id": "light.old", "category": "light"}, "deny", "turn_on"))
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, false), nil)
	h.renames.Hold(catalog.Rename{Old: "light.old", New: "lock.front_door"})
	if r := h.do(http.MethodPost, "/api/renames/apply", map[string]any{"entity_id": "lock.front_door", "formers": []string{"light.old"}}); r.errCode() != codeInvalidMandate {
		t.Errorf("category mismatch = %d %s", r.code, r.body)
	}
	h.renames.Hold(catalog.Rename{Old: "light.old2", New: "light.gone"})
	d2 := rulesDraft(t, rule("r-y", map[string]any{"entity_id": "light.old2"}, "deny", "turn_on"))
	cur := h.mandateOf(voice.ClientID)
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d2, cur.Digest, false), nil)
	if r := h.do(http.MethodPost, "/api/renames/apply", map[string]any{"entity_id": "light.gone", "formers": []string{"light.old2"}}); r.errCode() != codeNotFound {
		t.Errorf("device gone = %d %s", r.code, r.body)
	}
	// A device that is gone is not listed.
	var list []wireRename
	h.ok(http.MethodGet, "/api/renames", nil, &list)
	for _, r := range list {
		if r.EntityID == "light.gone" {
			t.Errorf("listed a device that is gone: %+v", r)
		}
	}
}

// A former ID another device has now: its rules may be meant for that device, so the
// rename is not taken over.
func TestRenameOntoAnIDInUseIsNotTakenOver(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	d := rulesDraft(t, rule("r-k", map[string]any{"entity_id": "light.kitchen"}, "deny", "turn_on"))
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, false), nil)
	// light.kitchen became lock.front_door, and another device has light.kitchen now.
	h.renames.Hold(catalog.Rename{Old: "light.kitchen", New: "lock.front_door"})
	var list []wireRename
	h.ok(http.MethodGet, "/api/renames", nil, &list)
	if len(list) != 1 || len(list[0].FormersInUse) != 1 || list[0].FormersInUse[0] != "light.kitchen" {
		t.Fatalf("renames = %+v", list)
	}
	if r := h.do(http.MethodPost, "/api/renames/apply", map[string]any{"entity_id": "lock.front_door", "formers": []string{"light.kitchen"}}); r.errCode() != codeConflict {
		t.Errorf("apply onto an ID in use = %d %s", r.code, r.body)
	}
}
