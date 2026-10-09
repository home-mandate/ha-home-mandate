// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// These tests write mandate.removed: they need a specification with that event
// (home-mandate/spec v0.1.0-alpha.3).

func (e env) inTx(t *testing.T, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (e env) remove(t *testing.T, id string, by audit.Actor) (bool, error) {
	t.Helper()
	var removed bool
	err := e.inTx(t, func(tx *sql.Tx) error {
		var err error
		removed, err = e.mandates.RemoveTx(context.Background(), tx, id, by)
		return err
	})
	return removed, err
}

func TestOnlyARevokedMandateIsRemoved(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	info, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.remove(t, info.ID, admin); !errors.Is(err, mandate.ErrNotRevoked) {
		t.Fatalf("remove active = %v, want ErrNotRevoked", err)
	}
	if _, err := e.remove(t, "m-none", admin); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("remove unknown = %v", err)
	}
	if err := e.mandates.Revoke(ctx, info.ID, admin); err != nil {
		t.Fatal(err)
	}
	if removed, err := e.remove(t, info.ID, admin); err != nil || !removed {
		t.Fatalf("remove revoked = %v, %v", removed, err)
	}
	if removed, err := e.remove(t, info.ID, admin); err != nil || removed {
		t.Errorf("second removal = %v, %v", removed, err)
	}
	got, err := e.mandates.Get(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != mandate.StatusRevoked || got.RemovedAt.IsZero() || got.RemovedBy != admin.ID {
		t.Errorf("removed mandate = %+v", got)
	}
	// Still readable while its data is kept: the audit log refers to its versions.
	if _, _, err := e.mandates.Current(ctx, info.ID); err != nil {
		t.Errorf("Current of a removed mandate = %v", err)
	}
	var buf strings.Builder
	if err := e.log.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"event":"mandate.removed","id":`) ||
		!strings.Contains(buf.String(), `"mandate":{"digest":"`+info.Digest+`","id":"`+info.ID+`"}`) {
		t.Errorf("audit log:\n%s", buf.String())
	}
	if r, err := e.log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

// Removing and deleting the data of a mandate keeps the protection against rollback
// (SPEC-v0 section 3.5): no version of it, older or newer, is accepted again.
func TestARemovedMandateIsNeverAcceptedAgain(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, func(d map[string]any) {
		d["limits"] = map[string]any{"max_actions_per_hour": 10}
	}), admin); err != nil {
		t.Fatal(err)
	}
	old, _, err := e.mandates.VersionDocument(ctx, first.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.Revoke(ctx, first.ID, admin); err != nil {
		t.Fatal(err)
	}
	purge := func() error {
		return e.inTx(t, func(tx *sql.Tx) error { return e.mandates.PurgeTx(ctx, tx, first.ID) })
	}
	if err := purge(); !errors.Is(err, mandate.ErrNotRemoved) {
		t.Fatalf("purge before removal = %v", err)
	}
	if _, err := e.remove(t, first.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.SetName(ctx, first.ID, "Kitchen voice"); err != nil {
		t.Fatal(err)
	}
	if err := purge(); err != nil {
		t.Fatal(err)
	}
	if err := purge(); err != nil {
		t.Errorf("second purge = %v", err)
	}
	other := e.agent(t, "Other")
	later := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["issuer"], d["version"] = issuer, 9 })
	for name, doc := range map[string][]byte{
		"the older version":          old,
		"without a version":          voiceAssistant(t, a.ClientID, nil),
		"a later version":            later,
		"the same id, another agent": voiceAssistant(t, other.ClientID, nil),
	} {
		if _, err := e.mandates.Put(ctx, doc, admin); err == nil {
			t.Errorf("%s accepted after the removal", name)
		}
	}
	var highest int64
	var versions int
	if err := e.db.QueryRow(`SELECT highest_version, (SELECT count(*) FROM mandate_versions WHERE mandate_id = mandates.id)
		FROM mandates WHERE id = ? AND purged_at IS NOT NULL`, first.ID).Scan(&highest, &versions); err != nil {
		t.Fatal(err)
	}
	if highest != 2 || versions != 0 {
		t.Errorf("tombstone: highest version %d, %d versions kept", highest, versions)
	}
	if _, err := e.mandates.Get(ctx, first.ID); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("Get of a purged mandate = %v", err)
	}
	if list, _ := e.mandates.List(ctx); len(list) != 0 {
		t.Errorf("List = %+v", list)
	}
	if name, err := e.mandates.Name(ctx, first.ID); err != nil || name != "Kitchen voice" {
		t.Errorf("Name = %q, %v", name, err)
	}
	if _, err := e.mandates.ForAgent(ctx, a.ClientID); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("ForAgent = %v", err)
	}
	if invalid, err := e.mandates.Invalid(ctx); err != nil || len(invalid) != 0 {
		t.Errorf("Invalid = %+v, %v", invalid, err)
	}
}
