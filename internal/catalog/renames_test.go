// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog_test

import (
	"context"
	"slices"
	"testing"

	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/ha"
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

func TestRenamesAreHeldStoredAndResolved(t *testing.T) {
	r, reload := newRenames(t)
	ctx := context.Background()
	if !r.Hold(catalog.Rename{Old: "lock.cellar", New: "lock.cellar_door"}) || r.Hold(catalog.Rename{Old: "lock.cellar", New: "lock.cellar_door"}) {
		t.Fatal("Hold: first not new, or second new")
	}
	// A chain: cellar → cellar_door → basement_door.
	r.Hold(catalog.Rename{Old: "lock.cellar_door", New: "lock.basement_door"})
	if got := r.Formers("lock.basement_door"); !slices.Equal(got, []string{"lock.cellar_door", "lock.cellar"}) {
		t.Errorf("Formers = %v", got)
	}
	if open := r.Open(); len(open) != 1 || len(open["lock.basement_door"]) != 2 {
		t.Errorf("Open = %v", open)
	}
	// Held, not stored yet.
	if got := reload().Formers("lock.basement_door"); len(got) != 0 {
		t.Errorf("stored before Store: %v", got)
	}
	if err := r.Store(ctx); err != nil {
		t.Fatal(err)
	}
	if got := reload().Formers("lock.basement_door"); len(got) != 2 {
		t.Errorf("after Store: %v", got)
	}
	resolved, err := r.Resolve(ctx, "lock.basement_door", catalog.ResolutionDismissed, "user-1")
	if err != nil || len(resolved) != 2 {
		t.Fatalf("Resolve = %v, %v", resolved, err)
	}
	if got := r.Formers("lock.basement_door"); len(got) != 0 {
		t.Errorf("after Resolve: %v", got)
	}
	if got := reload().Formers("lock.basement_door"); len(got) != 0 {
		t.Errorf("Resolve not stored: %v", got)
	}
	for _, bad := range []struct{ resolution, by string }{{"maybe", "user-1"}, {catalog.ResolutionApplied, ""}} {
		if _, err := r.Resolve(ctx, "x.y", bad.resolution, bad.by); err == nil {
			t.Errorf("Resolve(%q, %q) accepted", bad.resolution, bad.by)
		}
	}
}

func TestResolveStoresWhatWasOnlyHeld(t *testing.T) {
	r, reload := newRenames(t)
	r.Hold(catalog.Rename{Old: "lock.a", New: "lock.b"})
	if _, err := r.Resolve(context.Background(), "lock.b", catalog.ResolutionApplied, "user-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Store(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload().Formers("lock.b"); len(got) != 0 {
		t.Errorf("a resolved rename came back: %v", got)
	}
}

func TestRenameBackUndoesTheRename(t *testing.T) {
	r, reload := newRenames(t)
	ctx := context.Background()
	r.Hold(catalog.Rename{Old: "lock.a", New: "lock.b"})
	if err := r.Store(ctx); err != nil {
		t.Fatal(err)
	}
	r.Hold(catalog.Rename{Old: "lock.b", New: "lock.a"})
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

func TestRegistryIDsFindRenamesAfterAnOutage(t *testing.T) {
	r, reload := newRenames(t)
	entries := []ha.EntityEntry{{ID: "reg-1", EntityID: "lock.cellar"}, {ID: "reg-2", EntityID: "light.kitchen"}, {EntityID: "sensor.no_unique_id"}}
	if found := r.Observe(entries); len(found) != 0 {
		t.Fatalf("first observation found %v", found)
	}
	if err := r.Store(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Home-Mandate was not running while cellar became cellar_door.
	later := reload()
	entries[0].EntityID = "lock.cellar_door"
	found := later.Observe(entries)
	if len(found) != 1 || found[0] != (catalog.Rename{Old: "lock.cellar", New: "lock.cellar_door"}) {
		t.Errorf("Observe = %v", found)
	}
	if again := later.Observe(entries); len(again) != 0 {
		t.Errorf("the same registry again = %v", again)
	}
	// Invalid IDs are never taken over.
	if found := later.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock. bad"}}); len(found) != 0 {
		t.Errorf("invalid ID = %v", found)
	}
	if later.Hold(catalog.Rename{Old: "lock.a", New: "lock.a"}) || later.Hold(catalog.Rename{Old: "lock.a", New: "lock. b"}) {
		t.Error("Hold took an invalid rename")
	}
}
