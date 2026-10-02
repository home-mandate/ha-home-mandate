// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/store"
)

func newEnvWithDB(t *testing.T) (env, *sql.DB) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log := audit.New(s.DB(), household)
	return env{mandates: mandate.New(s.DB(), log, household), agents: agent.New(s.DB(), log), log: log}, s.DB()
}

func TestTamperedStoredVersionIsNotEvaluated(t *testing.T) {
	e, db := newEnvWithDB(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin); err != nil {
		t.Fatal(err)
	}
	// Someone with database access widens the stored rule: the digest no longer matches.
	if _, err := db.Exec(`UPDATE mandate_versions SET document = replace(document, '"camera"', '"sensor"')`); err != nil {
		t.Fatal(err)
	}
	fresh := mandate.New(db, e.log, household) // no cached parse
	if _, err := fresh.ForAgent(ctx, a.ClientID); !errors.Is(err, mandate.ErrInvalid) {
		t.Errorf("ForAgent on a tampered version = %v, want ErrInvalid", err)
	}
}

func TestDatabaseErrorsAreReported(t *testing.T) {
	e, db := newEnvWithDB(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	doc := voiceAssistant(t, a.ClientID, nil)
	if _, err := e.mandates.Put(ctx, doc, admin); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, func(d map[string]any) { d["id"] = "m-other" }), admin); err == nil {
		t.Error("Put succeeded")
	}
	if err := e.mandates.Revoke(ctx, "m-voice-assistant", admin); err == nil {
		t.Error("Revoke succeeded")
	}
	if _, err := e.mandates.ForAgent(ctx, a.ClientID); err == nil || errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("ForAgent = %v, want a database error", err)
	}
	if _, err := e.mandates.List(ctx); err == nil {
		t.Error("List succeeded")
	}
	if _, err := e.mandates.Versions(ctx, "m-voice-assistant"); err == nil {
		t.Error("Versions succeeded")
	}
}

func TestPutRejectsMalformedJSON(t *testing.T) {
	e, _ := newEnvWithDB(t)
	for _, doc := range []string{``, `{`, `[]`, `{"type":"https://mandate-spec.org/mandate/v0"}`} {
		if _, err := e.mandates.Put(context.Background(), []byte(doc), admin); !errors.Is(err, mandate.ErrInvalid) {
			t.Errorf("Put(%q) = %v, want ErrInvalid", doc, err)
		}
	}
}
