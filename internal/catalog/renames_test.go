// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

func newRenames(t *testing.T) (*catalog.Renames, func() *catalog.Renames) {
	t.Helper()
	_, s := newMarks(t)
	r, err := catalog.LoadRenames(context.Background(), s.DB())
	if err != nil {
		t.Fatal(err)
	}
	reload := func() *catalog.Renames {
		r, err := catalog.LoadRenames(context.Background(), s.DB())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	return r, reload
}

func rn(old, new string) catalog.Rename { return catalog.Rename{Old: old, New: new} }

func TestRenamesAreHeldStoredAndResolved(t *testing.T) {
	r, reload := newRenames(t)
	ctx := context.Background()
	if !r.Hold(rn("lock.cellar", "lock.cellar_door")) || r.Hold(rn("lock.cellar", "lock.cellar_door")) {
		t.Fatal("Hold: first not new, or second new")
	}
	// A chain: cellar → cellar_door → basement_door.
	r.Hold(rn("lock.cellar_door", "lock.basement_door"))
	if got := r.Formers("lock.basement_door"); !slices.Equal(got, []string{"lock.cellar_door", "lock.cellar"}) {
		t.Errorf("Formers = %v", got)
	}
	if got := r.Open()["lock.basement_door"]; len(got) != 2 {
		t.Errorf("Open = %v", r.Open())
	}
	if got := reload().Formers("lock.basement_door"); len(got) != 0 {
		t.Errorf("stored before Store: %v", got)
	}
	if err := r.Store(ctx); err != nil {
		t.Fatal(err)
	}
	if got := reload().Formers("lock.basement_door"); len(got) != 2 {
		t.Errorf("after Store: %v", got)
	}
	// What the human saw no longer holds: nothing is resolved.
	if err := r.Resolve(ctx, "lock.basement_door", []string{"lock.cellar_door"}, catalog.ResolutionDismissed, "user-1"); !errors.Is(err, catalog.ErrRenamesChanged) {
		t.Fatalf("Resolve with other formers: %v", err)
	}
	if err := r.Resolve(ctx, "lock.basement_door", []string{"lock.cellar", "lock.cellar_door"}, catalog.ResolutionDismissed, "user-1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Formers("lock.basement_door"); len(got) != 0 {
		t.Errorf("after Resolve: %v", got)
	}
	if got := reload().Formers("lock.basement_door"); len(got) != 0 {
		t.Errorf("Resolve not stored: %v", got)
	}
	for _, bad := range []struct{ resolution, by string }{{"maybe", "user-1"}, {catalog.ResolutionApplied, ""}} {
		if err := r.Resolve(ctx, "x.y", nil, bad.resolution, bad.by); err == nil {
			t.Errorf("Resolve(%q, %q) accepted", bad.resolution, bad.by)
		}
	}
}

func TestResolveStoresWhatWasOnlyHeld(t *testing.T) {
	r, reload := newRenames(t)
	r.Hold(rn("lock.a", "lock.b"))
	if err := r.Resolve(context.Background(), "lock.b", []string{"lock.a"}, catalog.ResolutionApplied, "user-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Store(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload().Formers("lock.b"); len(got) != 0 {
		t.Errorf("a resolved rename came back: %v", got)
	}
}

// The same entity (same registry ID) getting its former ID back undoes the rename.
func TestRenameBackUndoesTheRename(t *testing.T) {
	r, reload := newRenames(t)
	ctx := context.Background()
	r.Hold(catalog.Rename{Old: "lock.a", New: "lock.b", Registry: "reg-1"})
	if err := r.Store(ctx); err != nil {
		t.Fatal(err)
	}
	r.Hold(catalog.Rename{Old: "lock.b", New: "lock.a", Registry: "reg-1"})
	if got := r.Formers("lock.a"); len(got) != 0 {
		t.Errorf("Formers after renaming back = %v", got)
	}
	if err := r.Store(ctx); err != nil {
		t.Fatal(err)
	}
	if open := reload().Open(); len(open) != 0 {
		t.Errorf("open after renaming back = %v", open)
	}
}

// Two entities that swap their IDs are two renames: each keeps the rules on its former
// ID, never cancelled against each other.
func TestSwappedIDsAreTwoRenames(t *testing.T) {
	r, _ := newRenames(t)
	r.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock.front"}, {ID: "reg-2", EntityID: "lock.back"}})
	found := r.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock.back"}, {ID: "reg-2", EntityID: "lock.front"}})
	for _, f := range found {
		r.Hold(f)
	}
	if a, b := r.Formers("lock.back"), r.Formers("lock.front"); !slices.Equal(a, []string{"lock.front"}) || !slices.Equal(b, []string{"lock.back"}) {
		t.Errorf("formers after a swap: back %v, front %v", a, b)
	}
	if open := r.Open(); len(open) != 2 {
		t.Errorf("Open = %v", open)
	}
	// An event-only rename back (no registry ID) is no undo either.
	r2, _ := newRenames(t)
	r2.Hold(rn("lock.x", "lock.y"))
	r2.Hold(rn("lock.y", "lock.x"))
	if got := r2.Formers("lock.x"); !slices.Equal(got, []string{"lock.y"}) {
		t.Errorf("event rename back: %v", got)
	}
	// Resolving one side leaves the other.
	if err := r.Resolve(context.Background(), "lock.back", []string{"lock.front"}, catalog.ResolutionDismissed, "user-1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Formers("lock.front"); !slices.Equal(got, []string{"lock.back"}) {
		t.Errorf("the other side after resolving one: %v", got)
	}
}

// Every former ID counts: two renames into one ID, and one ID renamed twice.
func TestEveryFormerIDCounts(t *testing.T) {
	r, reload := newRenames(t)
	r.Hold(rn("lock.a", "lock.c"))
	r.Hold(rn("lock.b", "lock.c"))
	if got := r.Formers("lock.c"); !slices.Equal(got, []string{"lock.a", "lock.b"}) {
		t.Errorf("two into one: %v", got)
	}
	r.Hold(rn("lock.a", "lock.d"))
	if got := r.Formers("lock.d"); !slices.Equal(got, []string{"lock.a"}) {
		t.Errorf("renamed twice, second: %v", got)
	}
	if got := r.Formers("lock.c"); !slices.Contains(got, "lock.a") {
		t.Errorf("renamed twice, first lost: %v", got)
	}
	if err := r.Store(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload().Formers("lock.c"); !slices.Equal(got, []string{"lock.a", "lock.b"}) {
		t.Errorf("after a restart: %v", got)
	}
}

// A cycle a → b → c → a stays visible and resolvable.
func TestCyclesStayVisible(t *testing.T) {
	r, _ := newRenames(t)
	r.Hold(rn("lock.a", "lock.b"))
	r.Hold(rn("lock.b", "lock.c"))
	r.Hold(rn("lock.c", "lock.a"))
	open := r.Open()
	if len(open) != 3 || !slices.Equal(open["lock.a"], []string{"lock.c", "lock.b"}) {
		t.Errorf("Open = %v", open)
	}
	if err := r.Resolve(context.Background(), "lock.a", []string{"lock.b", "lock.c"}, catalog.ResolutionDismissed, "user-1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Formers("lock.a"); len(got) != 0 {
		t.Errorf("after resolving: %v", got)
	}
}

func TestRegistryIDsFindRenamesAfterAnOutage(t *testing.T) {
	r, reload := newRenames(t)
	entries := []ha.EntityEntry{{ID: "reg-1", EntityID: "lock.cellar"}, {ID: "reg-2", EntityID: "light.kitchen"}, {EntityID: "sensor.no_unique_id"}}
	if found := r.Observe(entries); len(found) != 0 {
		t.Fatalf("first observation found %v", found)
	}
	if err := r.Store(context.Background()); err != nil {
		t.Fatal(err)
	}
	later := reload()
	// reg-2 is missing from one listing; it is not forgotten.
	if found := later.Observe(entries[:1]); len(found) != 0 {
		t.Fatalf("a missing entry = %v", found)
	}
	entries[0].EntityID, entries[1].EntityID = "lock.cellar_door", "light.kitchen_ceiling"
	found := later.Observe(entries)
	want := []catalog.Rename{{Old: "lock.cellar", New: "lock.cellar_door", Registry: "reg-1"}, {Old: "light.kitchen", New: "light.kitchen_ceiling", Registry: "reg-2"}}
	if !slices.Equal(found, want) {
		t.Errorf("Observe = %v", found)
	}
	if again := later.Observe(entries); len(again) != 0 {
		t.Errorf("the same registry again = %v", again)
	}
	if found := later.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock. bad"}}); len(found) != 0 {
		t.Errorf("invalid ID = %v", found)
	}
	if later.Hold(rn("lock.a", "lock.a")) || later.Hold(rn("lock.a", "lock. b")) {
		t.Error("Hold took an invalid rename")
	}
}

// A rename that cannot be stored stays in force in memory and is stored later.
func TestRenamesThatCannotBeStoredStayHeld(t *testing.T) {
	_, s := newMarks(t)
	ctx := context.Background()
	r, err := catalog.LoadRenames(ctx, s.DB())
	if err != nil {
		t.Fatal(err)
	}
	r.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock.a"}})
	r.Hold(rn("lock.a", "lock.b"))
	_ = s.Close()
	if err := r.Store(ctx); err == nil {
		t.Fatal("Store without a database succeeded")
	}
	if got := r.Formers("lock.b"); len(got) != 1 {
		t.Errorf("held rename lost: %v", got)
	}
	if err := r.Resolve(ctx, "lock.b", []string{"lock.a"}, catalog.ResolutionApplied, "user-1"); err == nil {
		t.Error("Resolve without a database succeeded")
	}
	if got := r.Formers("lock.b"); len(got) != 1 {
		t.Errorf("a failed Resolve resolved: %v", got)
	}
	if _, err := catalog.LoadRenames(ctx, s.DB()); err == nil {
		t.Error("LoadRenames without a database succeeded")
	}
}

func TestEdgesListTheUnresolvedRenames(t *testing.T) {
	r, reload := newRenames(t)
	r.Hold(rn("lock.b", "lock.c"))
	r.Hold(rn("lock.a", "lock.b"))
	if err := r.Store(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []catalog.Rename{rn("lock.a", "lock.b"), rn("lock.b", "lock.c")}
	if got := reload().Edges(); !slices.Equal(got, want) {
		t.Errorf("Edges after a restart = %v", got)
	}
}

// Renames that cannot be stored are held up to MaxUnsaved; beyond, nothing more is held
// and the catalog is not ready (every request is denied) until storing works again.
func TestRenamesHeldWhileStoringFailsAreBounded(t *testing.T) {
	_, s := newMarks(t)
	ctx := context.Background()
	r, err := catalog.LoadRenames(ctx, s.DB())
	if err != nil {
		t.Fatal(err)
	}
	for i := range catalog.MaxUnsaved {
		r.Hold(rn(fmt.Sprintf("light.a%d", i), fmt.Sprintf("light.b%d", i)))
	}
	if r.Overflowing() {
		t.Fatal("overflowing at the limit")
	}
	if r.Hold(rn("light.x", "light.y")) || !r.Overflowing() {
		t.Error("held beyond the limit")
	}
	if !r.FailingSince().IsZero() {
		t.Error("failing before a store failed")
	}
	if err := r.Store(ctx); err != nil || r.Overflowing() || !r.FailingSince().IsZero() {
		t.Errorf("after storing: %v, overflow %v", err, r.Overflowing())
	}
	r.Hold(rn("light.p", "light.q"))
	_ = s.Close()
	if err := r.Store(ctx); err == nil || r.FailingSince().IsZero() {
		t.Errorf("a failed store is not reported: %v", err)
	}
}
