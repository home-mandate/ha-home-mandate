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
	"time"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/store"
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
	for _, name := range []string{"", "   ", strings.Repeat("x", 81), "line\nbreak", "bidi‮override", "zero​width", "sep arator"} {
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

func TestTokenAuthenticatesItsAgent(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "Voice assistant")
	token, expires, err := s.IssueToken(ctx, a.ClientID, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "hma_") || len(token) < 40 || time.Until(expires) < 29*24*time.Hour {
		t.Errorf("token %q expires %v", token, expires)
	}
	got, err := s.Authenticate(ctx, token)
	if err != nil || got.ClientID != a.ClientID {
		t.Errorf("Authenticate = %+v, %v", got, err)
	}
}

func TestTokensAreStoredOnlyAsHashes(t *testing.T) {
	s, _, db := newStore(t)
	a := register(t, s, "Voice assistant")
	token, _, err := s.IssueToken(context.Background(), a.ClientID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimPrefix(token, "hma_")
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		if n := dumpContains(t, db, table, secret); n != 0 {
			t.Errorf("token found in table %s", table)
		}
	}
}

// dumpContains counts values in table that contain needle, as text or as bytes.
func dumpContains(t *testing.T, db *sql.DB, table, needle string) int {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM "` + table + `"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	found := 0
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		_ = rows.Scan(ptrs...)
		for _, v := range values {
			switch x := v.(type) {
			case string:
				if strings.Contains(x, needle) {
					found++
				}
			case []byte:
				if strings.Contains(string(x), needle) {
					found++
				}
			}
		}
	}
	return found
}

func TestAuthenticateRejects(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "Voice assistant")
	valid, _, _ := s.IssueToken(ctx, a.ClientID, time.Hour)
	for name, token := range map[string]string{
		"empty":           "",
		"no prefix":       strings.TrimPrefix(valid, "hma_"),
		"wrong prefix":    "hmx_" + strings.TrimPrefix(valid, "hma_"),
		"unknown":         "hma_" + strings.Repeat("A", 43),
		"truncated":       valid[:len(valid)-1],
		"too long":        valid + strings.Repeat("A", 4096),
		"other alphabet":  "hma_" + strings.Repeat("+", 43),
		"with whitespace": valid + " ",
	} {
		if _, err := s.Authenticate(ctx, token); !errors.Is(err, agent.ErrUnauthorized) {
			t.Errorf("%s: Authenticate = %v, want ErrUnauthorized", name, err)
		}
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	a := register(t, s, "Voice assistant")
	token, _, _ := s.IssueToken(ctx, a.ClientID, time.Hour)

	s.SetClock(func() time.Time { return now.Add(time.Hour - time.Second) })
	if _, err := s.Authenticate(ctx, token); err != nil {
		t.Errorf("just before expiry: %v", err)
	}
	s.SetClock(func() time.Time { return now.Add(time.Hour) })
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("at expiry: %v, want ErrUnauthorized", err)
	}
}

func TestRevokeTakesEffectImmediately(t *testing.T) {
	s, log, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "Voice assistant")
	other := register(t, s, "Other")
	token, _, _ := s.IssueToken(ctx, a.ClientID, time.Hour)
	otherToken, _, _ := s.IssueToken(ctx, other.ClientID, time.Hour)

	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("revoked agent's token: %v, want ErrUnauthorized", err)
	}
	if _, err := s.Authenticate(ctx, otherToken); err != nil {
		t.Errorf("other agent affected: %v", err)
	}
	if _, _, err := s.IssueToken(ctx, a.ClientID, time.Hour); !errors.Is(err, agent.ErrRevoked) {
		t.Errorf("IssueToken for revoked agent = %v, want ErrRevoked", err)
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
	if _, _, err := s.IssueToken(ctx, "hm-client:nobody-00000000", time.Hour); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("IssueToken = %v, want ErrNotFound", err)
	}
	if _, _, err := s.IssueToken(ctx, "hm-client:nobody-00000000", 0); err == nil {
		t.Error("IssueToken with zero lifetime succeeded")
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
	if _, _, err := s.IssueToken(ctx, a.ClientID, time.Hour); err == nil {
		t.Error("IssueToken succeeded")
	}
	if _, err := s.Authenticate(ctx, "hma_"+strings.Repeat("A", 43)); err == nil || errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("Authenticate = %v, want a database error", err)
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err == nil {
		t.Error("Revoke succeeded")
	}
	if _, err := s.List(ctx); err == nil {
		t.Error("List succeeded")
	}
}
