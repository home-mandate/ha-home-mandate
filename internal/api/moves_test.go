// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
)

// A device that leaves or enters an area a rule names: the household is told which
// mandates that affects (SPEC-v0 section 3.4), until a human dismisses it.
func TestAreaMovesAreListedAndDismissed(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	d := rulesDraft(t,
		rule("r-kitchen", map[string]any{"category": "light", "area": "kitchen"}, "allow", "turn_on"),
		rule("r-floor", map[string]any{"category": "light", "area": "floor:ground_floor"}, "deny", "turn_off"),
	)
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, true), nil)
	ctx := context.Background()
	observe := func(areas map[string][]string) {
		t.Helper()
		var devs []catalog.Device
		for id, a := range areas {
			devs = append(devs, catalog.Device{EntityID: id, Areas: a})
		}
		if err := h.moves.Observe(ctx, devs); err != nil {
			t.Fatal(err)
		}
	}
	observe(map[string][]string{"light.kitchen": {"kitchen", "floor:ground_floor"}, "lock.front_door": {"hall"}})
	// The light leaves the kitchen and the floor; the lock moves between areas no rule names.
	observe(map[string][]string{"light.kitchen": {"cellar"}, "lock.front_door": {"garage"}})

	var list []wireAreaMove
	h.ok(http.MethodGet, "/api/area-moves", nil, &list)
	if len(list) != 1 || list[0].EntityID != "light.kitchen" || list[0].Name != "Küchenlicht" ||
		!slices.Equal(list[0].Previous, []string{"floor:ground_floor", "kitchen"}) || !slices.Equal(list[0].Current, []string{"cellar"}) ||
		len(list[0].Mandates) != 1 || list[0].Mandates[0].ID != m.ID || !slices.Equal(list[0].Mandates[0].Rules, []string{"r-kitchen", "r-floor"}) {
		t.Fatalf("moves = %+v", list)
	}
	if r := h.do(http.MethodPost, "/api/area-moves/dismiss", map[string]any{"entity_id": "light.kitchen"}); r.errCode() != codeInvalidInput || r.field() != "/current" {
		t.Errorf("dismiss without current = %d %s", r.code, r.body)
	}
	// Dismissing names the areas that were shown; a newer move is not dismissed unseen.
	if r := h.do(http.MethodPost, "/api/area-moves/dismiss", map[string]any{"entity_id": "light.kitchen", "current": []string{"garage"}}); r.errCode() != codeConflict {
		t.Errorf("dismiss of another move = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPost, "/api/area-moves/dismiss", map[string]any{"entity_id": "light.kitchen", "current": []string{"cellar"}}, nil)
	h.ok(http.MethodGet, "/api/area-moves", nil, &list)
	if len(list) != 0 {
		t.Errorf("moves after dismissing = %+v", list)
	}
}

// Without a store of area moves there are none, and none can be dismissed; a revoked
// mandate and one without rules on the areas are not affected.
func TestAreaMovesWithoutAStoreAndUnaffectedMandates(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	other := h.admit("Other")
	o := h.mandateOf(other.ClientID)
	d := rulesDraft(t, rule("r-kitchen", map[string]any{"category": "light", "area": "kitchen"}, "allow", "turn_on"))
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, true), nil)
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/revoke", nil, nil)
	_ = o
	ctx := context.Background()
	_ = h.moves.Observe(ctx, []catalog.Device{{EntityID: "light.kitchen", Areas: []string{"kitchen"}}})
	_ = h.moves.Observe(ctx, []catalog.Device{{EntityID: "light.kitchen", Areas: []string{"cellar"}}})
	var list []wireAreaMove
	h.ok(http.MethodGet, "/api/area-moves", nil, &list)
	if len(list) != 0 {
		t.Errorf("moves of revoked or unaffected mandates = %+v", list)
	}
	if r := h.do(http.MethodPost, "/api/area-moves/dismiss", map[string]any{"current": []string{}}); r.errCode() != codeInvalidInput || r.field() != "/entity_id" {
		t.Errorf("dismiss without entity_id = %d %s", r.code, r.body)
	}
	h.srv.cfg.AreaMoves = nil
	h.ok(http.MethodGet, "/api/area-moves", nil, &list)
	if len(list) != 0 {
		t.Errorf("moves without a store = %+v", list)
	}
	if r := h.do(http.MethodPost, "/api/area-moves/dismiss", map[string]any{"entity_id": "light.kitchen", "current": []string{"cellar"}}); r.errCode() != codeNotFound {
		t.Errorf("dismiss without a store = %d %s", r.code, r.body)
	}
}
