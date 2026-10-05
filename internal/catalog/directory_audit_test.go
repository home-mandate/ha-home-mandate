// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/ha"
)

// directoryEntries returns "actor-kind change entity<-former" of every directory.changed entry.
func directoryEntries(t *testing.T, log *audit.Log) []string {
	t.Helper()
	var buf strings.Builder
	if err := log.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e struct {
			Event string `json:"event"`
			Actor struct {
				Kind string `json:"kind"`
			} `json:"actor"`
			Directory audit.Directory `json:"directory"`
		}
		if line == "" || json.Unmarshal([]byte(line), &e) != nil || e.Event != audit.EventDirectoryChanged {
			continue
		}
		s := e.Actor.Kind + " " + e.Directory.Change + " " + e.Directory.EntityID
		if e.Directory.PreviousEntityID != "" {
			s += "<-" + e.Directory.PreviousEntityID
		}
		out = append(out, s)
	}
	return out
}

// SPEC-v0 section 11.4: every change of the directory is an audit entry, written in the
// same transaction as the change; nothing that changes nothing is recorded.
func TestDirectoryChangesAreAudited(t *testing.T) {
	marks, s := newMarks(t)
	ctx := context.Background()
	log := audit.New(s.DB(), "household:t")
	marks.SetRecorder(log)
	renames, err := catalog.LoadRenames(ctx, s.DB())
	if err != nil {
		t.Fatal(err)
	}
	renames.SetRecorder(log)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(marks.Set(ctx, "lock.cellar", true, "user-1"))
	must(marks.Set(ctx, "lock.cellar", true, "user-1")) // already marked: nothing recorded
	must(marks.Set(ctx, "light.x", false, "user-1"))    // not marked: nothing recorded
	renames.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock.cellar"}})
	for _, rn := range renames.Observe([]ha.EntityEntry{{ID: "reg-1", EntityID: "lock.cellar_door"}}) {
		renames.Hold(rn)
	}
	marks.Hold("lock.cellar", "lock.cellar_door")
	if _, err := marks.Carry(ctx, "lock.cellar", "lock.cellar_door"); err != nil {
		t.Fatal(err)
	}
	must(renames.Store(ctx))
	must(renames.Resolve(ctx, "lock.cellar_door", []string{"lock.cellar"}, catalog.ResolutionDismissed, "user-1"))
	must(marks.Set(ctx, "lock.cellar", false, "user-1"))

	want := []string{
		"user critical_marked lock.cellar",
		"system critical_marked lock.cellar_door",
		"system renamed lock.cellar_door<-lock.cellar",
		"user rename_dismissed lock.cellar_door<-lock.cellar",
		"user critical_unmarked lock.cellar",
	}
	if got := directoryEntries(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r, err := log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("log = %+v, %v", r, err)
	}
}

type failingRecorder struct{}

func (failingRecorder) AppendTx(context.Context, *sql.Tx, audit.Entry) (int64, error) {
	return 0, audit.ErrInvalidEntry
}

// A change that cannot be recorded is not made.
func TestAChangeThatCannotBeRecordedIsNotMade(t *testing.T) {
	marks, s := newMarks(t)
	ctx := context.Background()
	marks.SetRecorder(failingRecorder{})
	if err := marks.Set(ctx, "lock.cellar", true, "user-1"); err == nil {
		t.Fatal("Set without a writable audit log succeeded")
	}
	reloaded, err := catalog.LoadMarks(ctx, s.DB())
	if err != nil || reloaded.Critical("lock.cellar") || marks.Critical("lock.cellar") {
		t.Errorf("mark stored without its audit entry: %v", err)
	}
	renames, err := catalog.LoadRenames(ctx, s.DB())
	if err != nil {
		t.Fatal(err)
	}
	renames.SetRecorder(failingRecorder{})
	renames.Hold(catalog.Rename{Old: "lock.a", New: "lock.b"})
	if err := renames.Store(ctx); err == nil {
		t.Error("Store without a writable audit log succeeded")
	}
	if got := renames.Formers("lock.b"); len(got) != 1 {
		t.Errorf("the held rename was lost: %v", got)
	}
}
