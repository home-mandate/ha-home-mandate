// SPDX-License-Identifier: AGPL-3.0-or-later

package agent_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

var admin = audit.Actor{Kind: audit.ActorUser, ID: "user-1"}

func newStore(t *testing.T) (*agent.Store, *audit.Log, *sql.DB) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log := audit.New(s.DB(), "household:hm-0123456789ab")
	return agent.New(s.DB(), log), log, s.DB()
}

func register(t *testing.T, s *agent.Store, name string) agent.Agent {
	t.Helper()
	a, err := s.Register(context.Background(), name, admin)
	if err != nil {
		t.Fatalf("Register(%q): %v", name, err)
	}
	return a
}

func TestRegisterAssignsASpecConformingClientID(t *testing.T) {
	s, _, _ := newStore(t)
	// SPEC-v0 schema, agent.client_id: <namespace>:<id>
	pattern := regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}:[A-Za-z0-9._~-]{1,128}$`)
	for _, name := range []string{"Voice assistant", "Küchen-Agent ✨", "   n8n   ", strings.Repeat("x", 80)} {
		a := register(t, s, name)
		if !pattern.MatchString(a.ClientID) || !strings.HasPrefix(a.ClientID, "hm-client:") {
			t.Errorf("client id %q for %q", a.ClientID, name)
		}
		if a.DisplayName != strings.TrimSpace(name) || a.Status != agent.StatusActive {
			t.Errorf("agent = %+v", a)
		}
	}
	one, two := register(t, s, "Same"), register(t, s, "Same")
	if one.ClientID == two.ClientID {
		t.Error("two agents got the same client id")
	}
}

func TestRegisterRejectsInvalidNames(t *testing.T) {
	s, _, db := newStore(t)
	for _, name := range []string{"", "   ", strings.Repeat("x", 81), "line\nbreak", "bidi\u202eoverride", "zero\u200bwidth", "sep\u2028arator"} {
		if _, err := s.Register(context.Background(), name, admin); !errors.Is(err, agent.ErrInvalidName) {
			t.Errorf("Register(%q) = %v, want ErrInvalidName", name, err)
		}
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM agents`).Scan(&n)
	if n != 0 {
		t.Errorf("%d agents stored", n)
	}
}

func TestRevokeTakesEffectImmediately(t *testing.T) {
	s, log, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "Voice assistant")
	other := register(t, s, "Other")
	token := issue(t, s, a.ClientID).AccessToken
	otherToken := issue(t, s, other.ClientID).AccessToken

	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("revoked agent's token: %v, want ErrUnauthorized", err)
	}
	if _, err := s.Authenticate(ctx, otherToken, resource); err != nil {
		t.Errorf("other agent affected: %v", err)
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Errorf("second Revoke: %v", err)
	}
	got, err := s.Get(ctx, a.ClientID)
	if err != nil || got.Status != agent.StatusRevoked {
		t.Errorf("Get = %+v, %v", got, err)
	}
	// agent.registered ×2, agent.revoked ×1 (the second revoke is a no-op).
	var buf strings.Builder
	if err := log.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), `"event":"agent.registered"`); n != 2 {
		t.Errorf("agent.registered entries = %d", n)
	}
	if n := strings.Count(buf.String(), `"event":"agent.revoked"`); n != 1 {
		t.Errorf("agent.revoked entries = %d", n)
	}
	if r, err := log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

func TestUnknownAgents(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Get(ctx, "hm-client:nobody-00000000"); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
	if err := s.Revoke(ctx, "hm-client:nobody-00000000", admin); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("Revoke = %v, want ErrNotFound", err)
	}
}

func TestList(t *testing.T) {
	s, _, _ := newStore(t)
	register(t, s, "B")
	register(t, s, "A")
	list, err := s.List(context.Background())
	if err != nil || len(list) != 2 || list[0].DisplayName != "B" {
		t.Errorf("List = %+v, %v (want registration order)", list, err)
	}
}

func TestStoreReportsDatabaseErrors(t *testing.T) {
	s, _, db := newStore(t)
	a := register(t, s, "A")
	db.Close()
	ctx := context.Background()
	if _, err := s.Register(ctx, "B", admin); err == nil {
		t.Error("Register succeeded")
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err == nil {
		t.Error("Revoke succeeded")
	}
	if _, err := s.List(ctx); err == nil {
		t.Error("List succeeded")
	}
}

// A display name must show a letter or digit once cleaned as the UI shows it (TESTING.md
// section 4, UI: blank-looking names).
func TestRegisterRejectsNamesWithoutVisibleText(t *testing.T) {
	s, _, _ := newStore(t)
	for _, name := range []string{"\u3164\u3164", "\u2800", "---", "\u0301\u0302", "x\ufe0f", "\u115f\u1160"} {
		_, err := s.Register(context.Background(), name, admin)
		wantOK := name == "x\ufe0f" // the selector is cleaned away, the letter stays
		if (err == nil) != wantOK || !wantOK && !errors.Is(err, agent.ErrInvalidName) {
			t.Errorf("Register(%q) = %v", name, err)
		}
	}
	if n, ok := agent.ValidName("  Küche 2  "); !ok || n != "Küche 2" {
		t.Errorf("ValidName = %q, %v", n, ok)
	}
	if _, ok := agent.ValidName("\u3164"); ok {
		t.Error("ValidName accepted a blank name")
	}
}

func TestRegisterTxKeepsTheRedirectURIs(t *testing.T) {
	s, _, db := newStore(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := []string{"https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1/callback"}
	a, err := s.RegisterTx(ctx, tx, "Claude", agent.Client{ID: "https://claude.ai/oauth/client.json", Verified: true, RedirectURIs: uris}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, a.ClientID)
	if err != nil || len(got.RedirectURIs) != 2 || got.RedirectURIs[1] != uris[1] || !got.ClientVerified || !got.RevokedAt.IsZero() {
		t.Errorf("Get = %+v, %v", got, err)
	}
	plain := register(t, s, "Pairing")
	if got, _ := s.Get(ctx, plain.ClientID); got.RedirectURIs == nil || len(got.RedirectURIs) != 0 {
		t.Errorf("pairing agent redirect URIs = %#v", got.RedirectURIs)
	}
}

func TestRevokeRecordsWhoAndWhen(t *testing.T) {
	s, _, db := newStore(t)
	ctx := context.Background()
	a := register(t, s, "Voice")
	tx, _ := db.BeginTx(ctx, nil)
	changed, err := s.RevokeTx(ctx, tx, a.ClientID, admin)
	if err != nil || !changed {
		t.Fatalf("RevokeTx = %v, %v", changed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a.ClientID)
	if got.Status != agent.StatusRevoked || got.RevokedBy != admin.ID || got.RevokedAt.IsZero() {
		t.Errorf("revoked agent = %+v", got)
	}
	tx, _ = db.BeginTx(ctx, nil)
	defer func() { _ = tx.Rollback() }()
	if changed, err := s.RevokeTx(ctx, tx, a.ClientID, audit.Actor{Kind: "user", ID: "other"}); err != nil || changed {
		t.Errorf("second RevokeTx = %v, %v", changed, err)
	}
	if _, err := s.RevokeTx(ctx, tx, "hm-client:nobody-00000000", admin); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("RevokeTx(unknown) = %v", err)
	}
}

func TestListReportsUnreadableRedirectURIs(t *testing.T) {
	s, _, db := newStore(t)
	a := register(t, s, "Voice")
	if _, err := db.Exec(`UPDATE agents SET redirect_uris = 'not json' WHERE client_id = ?`, a.ClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background()); err == nil {
		t.Error("List succeeded with unreadable redirect URIs")
	}
}
