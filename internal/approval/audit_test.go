// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

var (
	ui      = audit.Actor{Kind: audit.ActorUser, ID: u1}
	cliUser = audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}
)

func newAuditedApprovers(t *testing.T) (*Approvers, *audit.Log) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), "household:hm-0123456789ab")
	return NewApprovers(st.DB(), log), log
}

type approverChange struct {
	Actor    audit.Actor    `json:"actor"`
	Approver audit.Approver `json:"approver"`
}

// approverChanges returns the approver.changed entries, oldest first, after checking that
// the log verifies with the specification.
func approverChanges(t *testing.T, log *audit.Log) []approverChange {
	t.Helper()
	ctx := context.Background()
	if r, err := log.Verify(ctx); err != nil || !r.Valid {
		t.Fatalf("Verify = %+v, %v", r, err)
	}
	var out bytes.Buffer
	if err := log.Export(ctx, &out); err != nil {
		t.Fatal(err)
	}
	var list []approverChange
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Event string `json:"event"`
			approverChange
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Event != audit.EventApproverChanged {
			t.Errorf("unexpected event %s", entry.Event)
			continue
		}
		list = append(list, entry.approverChange)
	}
	return list
}

// SPEC-v0 section 11.1: adding someone to the approvers and removing them is one
// approver.changed entry each, with the human as actor. Changing the channels of someone
// who is an approver already adds nobody and records nothing.
func TestApproverChangesAreAudited(t *testing.T) {
	a, log := newAuditedApprovers(t)
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u2, Devices: phones("mobile_app_anna")}, cliUser); err != nil {
		t.Fatal(err)
	}
	v, err := a.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PutIf(ctx, Approver{UserID: u1, UI: true}, v, ui); err != nil {
		t.Fatal(err)
	}
	// Other channels for both: no entry.
	if err := a.Put(ctx, Approver{UserID: u2, UI: true}, cliUser); err != nil {
		t.Fatal(err)
	}
	v, _ = a.Version(ctx)
	if err := a.PutIf(ctx, Approver{UserID: u1, Devices: phones("mobile_app_markus")}, v, ui); err != nil {
		t.Fatal(err)
	}
	v, _ = a.Version(ctx)
	if err := a.RemoveIf(ctx, u2, v, ui); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx, u1, cliUser); err != nil {
		t.Fatal(err)
	}
	want := []approverChange{
		{cliUser, audit.Approver{Change: audit.ApproverAdded, ID: u2}},
		{ui, audit.Approver{Change: audit.ApproverAdded, ID: u1}},
		{ui, audit.Approver{Change: audit.ApproverRemoved, ID: u2}},
		{cliUser, audit.Approver{Change: audit.ApproverRemoved, ID: u1}},
	}
	got := approverChanges(t, log)
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A refused or failed change records nothing, and a change that cannot be recorded is
// not made.
func TestRefusedApproverChangesAreNotAudited(t *testing.T) {
	a, log := newAuditedApprovers(t)
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u1, UI: true}, ui); err != nil {
		t.Fatal(err)
	}
	unrecordable := audit.Actor{Kind: audit.ActorUser, ID: "user‮x"}
	for name, change := range map[string]func() error{
		"invalid approver":    func() error { return a.Put(ctx, Approver{UserID: u2}, ui) },
		"stale version":       func() error { return a.PutIf(ctx, Approver{UserID: u2, UI: true}, "stale", ui) },
		"remove stale":        func() error { return a.RemoveIf(ctx, u1, "stale", ui) },
		"remove unknown":      func() error { return a.Remove(ctx, u2, ui) },
		"add unrecordable":    func() error { return a.Put(ctx, Approver{UserID: u2, UI: true}, unrecordable) },
		"remove unrecordable": func() error { return a.Remove(ctx, u1, unrecordable) },
	} {
		if err := change(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := approverChanges(t, log); len(got) != 1 {
		t.Errorf("entries = %+v, want only the first", got)
	}
	list, err := a.List(ctx)
	if err != nil || len(list) != 1 || list[0].UserID != u1 {
		t.Errorf("approvers = %+v, %v", list, err)
	}
	if err := a.Remove(ctx, u1, audit.Actor{}); err == nil || errors.Is(err, ErrApproverNotFound) {
		t.Errorf("Remove without actor = %v", err)
	}
}
