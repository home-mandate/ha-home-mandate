// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/home-mandate/home-mandate/internal/catalog"
)

type fakeStorer struct {
	err   error
	edges []catalog.Rename
}

func (f fakeStorer) Store(context.Context) error { return f.err }
func (f fakeStorer) Edges() []catalog.Rename     { return f.edges }

type fakeCarrier struct {
	marked map[string]bool
	err    error
}

func (f *fakeCarrier) Carry(_ context.Context, oldID, newID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if !f.marked[oldID] || f.marked[newID] {
		return false, nil
	}
	f.marked[newID] = true
	return true, nil
}

func TestDirectoryChangedCarriesMarksAndNotifies(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	marks := &fakeCarrier{marked: map[string]bool{"lock.cellar": true}}
	notified := 0
	renames := []catalog.Rename{{Old: "lock.cellar", New: "lock.cellar_door"}, {Old: "light.kitchen", New: "light.kitchen_ceiling"}}
	directoryChanged(context.Background(), logger, marks, fakeStorer{edges: renames}, func() { notified++ }, renames)
	if !marks.marked["lock.cellar_door"] || marks.marked["light.kitchen_ceiling"] {
		t.Errorf("marks = %v", marks.marked)
	}
	if notified != 1 {
		t.Errorf("notified %d times", notified)
	}
	out := buf.String()
	for _, want := range []string{"old=lock.cellar new=lock.cellar_door", "critical mark carried", "old=light.kitchen new=light.kitchen_ceiling"} {
		if !strings.Contains(out, want) {
			t.Errorf("log misses %q:\n%s", want, out)
		}
	}
}

func TestDirectoryChangedLogsAFailedCarryAndStillNotifies(t *testing.T) {
	var buf bytes.Buffer
	notified := false
	directoryChanged(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)), &fakeCarrier{err: errors.New("disk full")},
		fakeStorer{err: errors.New("database locked"), edges: []catalog.Rename{{Old: "lock.a", New: "lock.b"}}}, func() { notified = true },
		[]catalog.Rename{{Old: "lock.a", New: "lock.b"}})
	if !notified || !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "disk full") ||
		!strings.Contains(buf.String(), "renames not stored") {
		t.Errorf("notified=%v log=%s", notified, buf.String())
	}
}

// A mark that could not be carried, or a restart before it was stored, is made up for by
// the next refresh: every refresh carries along all unresolved renames, chains included.
func TestMarksAreCarriedAgainWithTheNextRefresh(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	marks := &fakeCarrier{marked: map[string]bool{"lock.a": true}, err: errors.New("database locked")}
	// a → b → c, listed in the order that needs a second pass.
	edges := []catalog.Rename{{Old: "lock.b", New: "lock.c"}, {Old: "lock.a", New: "lock.b"}}
	directoryChanged(context.Background(), logger, marks, fakeStorer{edges: edges}, func() {}, nil)
	if marks.marked["lock.b"] || marks.marked["lock.c"] {
		t.Fatalf("carried although the database failed: %v", marks.marked)
	}
	marks.err = nil // the database works again; no new rename arrived
	directoryChanged(context.Background(), logger, marks, fakeStorer{edges: edges}, func() {}, nil)
	if !marks.marked["lock.b"] || !marks.marked["lock.c"] {
		t.Errorf("marks after the next refresh = %v", marks.marked)
	}
}
