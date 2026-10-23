// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/store"
)

func movesStore(t *testing.T) *AreaMoves {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m, err := LoadAreaMoves(context.Background(), s.DB())
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) }
	return m
}

func devicesIn(areas map[string][]string) []Device {
	var out []Device
	for id, a := range areas {
		out = append(out, Device{EntityID: id, Areas: a})
	}
	return out
}

func TestTheFirstObservationIsNoMove(t *testing.T) {
	m := movesStore(t)
	if err := m.Observe(context.Background(), devicesIn(map[string][]string{"light.kitchen": {"kitchen", "floor:eg"}, "switch.pump": nil})); err != nil {
		t.Fatal(err)
	}
	if got := m.Open(); len(got) != 0 {
		t.Errorf("moves after the first observation: %+v", got)
	}
}

func TestMovesAreFoundKeptAndUndone(t *testing.T) {
	m := movesStore(t)
	ctx := context.Background()
	observe := func(areas map[string][]string) {
		t.Helper()
		if err := m.Observe(ctx, devicesIn(areas)); err != nil {
			t.Fatal(err)
		}
	}
	observe(map[string][]string{"light.kitchen": {"kitchen", "floor:eg"}, "light.hall": {"hall"}})
	// The kitchen light moves to the cellar, the hall gets a floor, a new device appears
	// in the garage.
	observe(map[string][]string{"light.kitchen": {"cellar"}, "light.hall": {"hall", "floor:eg"}, "cover.garage": {"garage"}})
	want := []AreaMove{
		{EntityID: "cover.garage", Previous: []string{}, Current: []string{"garage"}, MovedAt: m.now()},
		{EntityID: "light.hall", Previous: []string{"hall"}, Current: []string{"floor:eg", "hall"}, MovedAt: m.now()},
		{EntityID: "light.kitchen", Previous: []string{"floor:eg", "kitchen"}, Current: []string{"cellar"}, MovedAt: m.now()},
	}
	if got := m.Open(); !slices.EqualFunc(got, want, sameMove) {
		t.Fatalf("moves = %+v\nwant %+v", got, want)
	}
	// A second move keeps where it came from; a move back is no move; a device missing
	// from a refresh keeps its move.
	observe(map[string][]string{"light.kitchen": {"garage"}, "light.hall": {"hall"}})
	want = []AreaMove{
		{EntityID: "cover.garage", Previous: []string{}, Current: []string{"garage"}, MovedAt: m.now()},
		{EntityID: "light.kitchen", Previous: []string{"floor:eg", "kitchen"}, Current: []string{"garage"}, MovedAt: m.now()},
	}
	if got := m.Open(); !slices.EqualFunc(got, want, sameMove) {
		t.Fatalf("after the second move: %+v", got)
	}
	// Moves and the last seen areas survive a restart.
	again, err := LoadAreaMoves(ctx, m.db)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Open(); !slices.EqualFunc(got, want, sameMove) {
		t.Errorf("after loading: %+v", got)
	}
	again.now = m.now
	if err := again.Observe(ctx, devicesIn(map[string][]string{"light.kitchen": {"garage"}, "light.hall": {"hall"}, "cover.garage": {"garage"}})); err != nil {
		t.Fatal(err)
	}
	if got := again.Open(); !slices.EqualFunc(got, want, sameMove) {
		t.Errorf("an unchanged observation after loading: %+v", got)
	}
}

func TestAMoveIsDismissedOnlyAsSeen(t *testing.T) {
	m := movesStore(t)
	ctx := context.Background()
	_ = m.Observe(ctx, devicesIn(map[string][]string{"light.kitchen": {"kitchen"}}))
	_ = m.Observe(ctx, devicesIn(map[string][]string{"light.kitchen": {"cellar"}}))
	if err := m.Dismiss(ctx, "light.kitchen", []string{"garage"}); !errors.Is(err, ErrMoveChanged) {
		t.Errorf("dismissing a move the caller did not see: %v", err)
	}
	if err := m.Dismiss(ctx, "light.kitchen", []string{"cellar"}); err != nil {
		t.Fatal(err)
	}
	if got := m.Open(); len(got) != 0 {
		t.Errorf("moves after dismissing: %+v", got)
	}
	if err := m.Dismiss(ctx, "light.kitchen", []string{"cellar"}); !errors.Is(err, ErrMoveChanged) {
		t.Errorf("dismissing twice: %v", err)
	}
	// The next move starts from the set the human saw.
	_ = m.Observe(ctx, devicesIn(map[string][]string{"light.kitchen": {"garage"}}))
	if got := m.Open(); len(got) != 1 || !slices.Equal(got[0].Previous, []string{"cellar"}) {
		t.Errorf("next move: %+v", got)
	}
}

func sameMove(a, b AreaMove) bool {
	return a.EntityID == b.EntityID && slices.Equal(a.Previous, b.Previous) && slices.Equal(a.Current, b.Current) && a.MovedAt.Equal(b.MovedAt)
}

func TestChangedAreasOfAMove(t *testing.T) {
	m := AreaMove{Previous: []string{"floor:eg", "kitchen"}, Current: []string{"cellar", "floor:eg"}}
	if got := m.Changed(); !slices.Equal(got, []string{"cellar", "kitchen"}) {
		t.Errorf("Changed = %q", got)
	}
}

// A device missing from refreshes (disabled for a while, Home Assistant starting) neither
// enters its areas anew when it returns nor hides the area it left meanwhile; an empty
// refresh forgets nothing.
func TestAMissingDeviceKeepsItsAreas(t *testing.T) {
	m := movesStore(t)
	ctx := context.Background()
	observe := func(areas map[string][]string) {
		t.Helper()
		if err := m.Observe(ctx, devicesIn(areas)); err != nil {
			t.Fatal(err)
		}
	}
	observe(map[string][]string{"light.kitchen": {"kitchen"}, "lock.door": {"hall"}, "switch.pump": nil})
	observe(map[string][]string{"switch.pump": nil})
	observe(nil)
	observe(map[string][]string{"light.kitchen": {"kitchen"}, "lock.door": {"garage"}, "switch.pump": nil, "sensor.new": nil})
	want := []AreaMove{{EntityID: "lock.door", Previous: []string{"hall"}, Current: []string{"garage"}, MovedAt: m.now()}}
	if got := m.Open(); !slices.EqualFunc(got, want, sameMove) {
		t.Errorf("moves = %+v\nwant %+v", got, want)
	}
	// A dismissed move stays dismissed after a restart.
	if err := m.Dismiss(ctx, "lock.door", []string{"garage"}); err != nil {
		t.Fatal(err)
	}
	again, err := LoadAreaMoves(ctx, m.db)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Open(); len(got) != 0 {
		t.Errorf("after a dismissal and a restart: %+v", got)
	}
}
