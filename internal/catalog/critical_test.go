// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/store"
)

func newMarks(t *testing.T) (*catalog.Marks, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m, err := catalog.LoadMarks(context.Background(), s.DB())
	if err != nil {
		t.Fatal(err)
	}
	return m, s
}

func TestMarksAreStoredAndSurviveARestart(t *testing.T) {
	m, s := newMarks(t)
	ctx := context.Background()
	if m.Critical("switch.door_opener") || len(m.All()) != 0 {
		t.Fatal("a new household has critical entities")
	}
	for _, id := range []string{"switch.door_opener", "cover.ground_floor"} {
		if err := m.Set(ctx, id, true, "user-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Set(ctx, "switch.door_opener", true, "user-2"); err != nil {
		t.Errorf("marking twice: %v", err)
	}
	if !m.Critical("switch.door_opener") || m.Critical("switch.other") || !slices.Equal(m.All(), []string{"cover.ground_floor", "switch.door_opener"}) {
		t.Errorf("marks = %v", m.All())
	}
	again, err := catalog.LoadMarks(ctx, s.DB())
	if err != nil || !slices.Equal(again.All(), m.All()) {
		t.Fatalf("after a restart: %v, %v", again.All(), err)
	}
	if err := m.Set(ctx, "cover.ground_floor", false, "user-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(ctx, "cover.never_marked", false, "user-1"); err != nil {
		t.Errorf("removing a mark that does not exist: %v", err)
	}
	if m.Critical("cover.ground_floor") || len(m.All()) != 1 {
		t.Errorf("marks after removing one = %v", m.All())
	}
}

func TestMarksRejectBadInput(t *testing.T) {
	m, s := newMarks(t)
	ctx := context.Background()
	for name, id := range map[string]string{"empty": "", "space": "switch door", "non-ASCII": "switch.tür", "too long": strings.Repeat("a", 256)} {
		if err := m.Set(ctx, id, true, "user-1"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := m.Set(ctx, "switch.x", true, ""); err == nil {
		t.Error("mark without a user accepted")
	}
	_ = s.Close()
	if err := m.Set(ctx, "switch.x", true, "user-1"); err == nil || m.Critical("switch.x") {
		t.Error("a mark that could not be stored is in effect")
	}
	if _, err := catalog.LoadMarks(ctx, s.DB()); err == nil {
		t.Error("LoadMarks on a closed database succeeded")
	}
}
