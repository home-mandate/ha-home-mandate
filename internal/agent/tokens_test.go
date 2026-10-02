// SPDX-License-Identifier: AGPL-3.0-or-later

package agent_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
)

const resource = "https://hm.example.org/mcp"

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func withClock(s *agent.Store) *clock {
	c := &clock{now: t0}
	s.SetClock(c.Now)
	return c
}

func issue(t *testing.T, s *agent.Store, clientID string) agent.TokenPair {
	t.Helper()
	p, err := s.IssueTokens(context.Background(), clientID, resource)
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	return p
}

func exported(t *testing.T, log *audit.Log) string {
	t.Helper()
	var buf strings.Builder
	if err := log.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestIssueTokens(t *testing.T) {
	s, _, _ := newStore(t)
	withClock(s)
	a := register(t, s, "Voice assistant")
	p := issue(t, s, a.ClientID)
	if !strings.HasPrefix(p.AccessToken, "hma_") || len(p.AccessToken) != 47 ||
		!strings.HasPrefix(p.RefreshToken, "hmr_") || len(p.RefreshToken) != 47 {
		t.Errorf("tokens %q %q", p.AccessToken, p.RefreshToken)
	}
	if !p.ExpiresAt.Equal(t0.Add(agent.AccessTokenTTL)) || agent.AccessTokenTTL != 10*time.Minute ||
		agent.RefreshTokenTTL != 30*24*time.Hour {
		t.Errorf("expires %v", p.ExpiresAt)
	}
	got, err := s.Authenticate(context.Background(), p.AccessToken, resource)
	if err != nil || got.ClientID != a.ClientID {
		t.Errorf("Authenticate = %+v, %v", got, err)
	}
	if other := issue(t, s, a.ClientID); other.AccessToken == p.AccessToken || other.RefreshToken == p.RefreshToken {
		t.Error("tokens repeat")
	}
}

func TestIssueTokensRejects(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	if _, err := s.IssueTokens(ctx, a.ClientID, ""); err == nil {
		t.Error("empty resource accepted")
	}
	if _, err := s.IssueTokens(ctx, "hm-client:nobody-00000000", resource); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("unknown agent: %v", err)
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueTokens(ctx, a.ClientID, resource); !errors.Is(err, agent.ErrRevoked) {
		t.Errorf("revoked agent: %v", err)
	}
}

func TestTokensAreStoredOnlyAsHashes(t *testing.T) {
	s, _, db := newStore(t)
	a := register(t, s, "Voice assistant")
	p := issue(t, s, a.ClientID)
	refreshed, err := s.Refresh(context.Background(), p.RefreshToken, resource)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{p.AccessToken, p.RefreshToken, refreshed.AccessToken, refreshed.RefreshToken} {
		secret := token[4:]
		for _, table := range tables(t, db) {
			if n := dumpContains(t, db, table, secret); n != 0 {
				t.Errorf("token found in table %s", table)
			}
		}
	}
}

func tables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		names = append(names, name)
	}
	return names
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

// Negative catalog: no token, wrong scheme, expired, revoked, other resource → 401.
func TestAuthenticateRejects(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "Voice assistant")
	p := issue(t, s, a.ClientID)
	valid := p.AccessToken
	for name, tc := range map[string]struct{ token, resource string }{
		"empty":               {"", resource},
		"no prefix":           {valid[4:], resource},
		"wrong prefix":        {"hmx_" + valid[4:], resource},
		"refresh as access":   {p.RefreshToken, resource},
		"access as refresh":   {"hmr_" + valid[4:], resource},
		"unknown":             {"hma_" + strings.Repeat("A", 43), resource},
		"truncated":           {valid[:len(valid)-1], resource},
		"too long":            {valid + strings.Repeat("A", 4096), resource},
		"other alphabet":      {"hma_" + strings.Repeat("+", 43), resource},
		"with whitespace":     {valid + " ", resource},
		"other resource":      {valid, "https://other.example.org/mcp"},
		"resource with slash": {valid, resource + "/"},
		"no resource":         {valid, ""},
	} {
		if _, err := s.Authenticate(ctx, tc.token, tc.resource); !errors.Is(err, agent.ErrUnauthorized) {
			t.Errorf("%s: Authenticate = %v, want ErrUnauthorized", name, err)
		}
	}
}

func TestAccessTokenExpiresAfterTenMinutes(t *testing.T) {
	s, _, _ := newStore(t)
	c := withClock(s)
	ctx := context.Background()
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	c.Set(t0.Add(10*time.Minute - time.Second))
	if _, err := s.Authenticate(ctx, p.AccessToken, resource); err != nil {
		t.Errorf("just before expiry: %v", err)
	}
	c.Set(t0.Add(10 * time.Minute))
	if _, err := s.Authenticate(ctx, p.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("at expiry: %v", err)
	}
}

func TestRefreshRotates(t *testing.T) {
	s, _, _ := newStore(t)
	c := withClock(s)
	ctx := context.Background()
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)

	c.Set(t0.Add(time.Hour))
	next, err := s.Refresh(ctx, p.RefreshToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if next.RefreshToken == p.RefreshToken || next.AccessToken == p.AccessToken || !next.ExpiresAt.Equal(t0.Add(time.Hour+10*time.Minute)) {
		t.Errorf("refreshed = %+v", next)
	}
	if got, err := s.Authenticate(ctx, next.AccessToken, resource); err != nil || got.ClientID != a.ClientID {
		t.Errorf("new access token: %+v, %v", got, err)
	}
	// The resource of a refresh request must match the original one.
	if _, err := s.Refresh(ctx, next.RefreshToken, "https://other.example.org/mcp"); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("other resource: %v", err)
	}
	if _, err := s.Refresh(ctx, next.RefreshToken, resource); err != nil {
		t.Errorf("same resource after a refused attempt: %v", err)
	}
}

func TestRefreshTokenExpiresAfterThirtyDays(t *testing.T) {
	s, _, _ := newStore(t)
	c := withClock(s)
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	c.Set(t0.Add(30 * 24 * time.Hour))
	if _, err := s.Refresh(context.Background(), p.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("expired refresh token: %v", err)
	}
}

// Negative catalog: refresh token used twice → whole chain revoked.
func TestRefreshTokenReuseRevokesTheWholeChain(t *testing.T) {
	s, log, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	first := issue(t, s, a.ClientID)
	second, err := s.Refresh(ctx, first.RefreshToken, resource)
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.Refresh(ctx, second.RefreshToken, resource)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := issue(t, s, a.ClientID) // another family of the same agent

	// An attacker replays the first refresh token.
	if _, err := s.Refresh(ctx, first.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) || !errors.Is(err, agent.ErrRefreshReused) {
		t.Fatalf("reuse = %v", err)
	}
	for name, token := range map[string]string{"first access": first.AccessToken, "second access": second.AccessToken, "third access": third.AccessToken} {
		if _, err := s.Authenticate(ctx, token, resource); !errors.Is(err, agent.ErrUnauthorized) {
			t.Errorf("%s after reuse: %v", name, err)
		}
	}
	if _, err := s.Refresh(ctx, third.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("latest refresh token after reuse: %v", err)
	}
	if _, err := s.Authenticate(ctx, unrelated.AccessToken, resource); err != nil {
		t.Errorf("other family affected: %v", err)
	}
	out := exported(t, log)
	if !strings.Contains(out, `"event":"auth.rejected"`) || !strings.Contains(out, `"error":"refresh_token_reused"`) ||
		!strings.Contains(out, `"client_id":"`+a.ClientID+`"`) {
		t.Errorf("audit log:\n%s", out)
	}
	if strings.Contains(out, first.RefreshToken[4:]) {
		t.Error("audit log contains the token")
	}
}

// Two concurrent refreshes with the same token: exactly one wins, the other is reuse.
func TestConcurrentRefreshHasOneWinner(t *testing.T) {
	s, _, _ := newStore(t)
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() { _, errs[i] = s.Refresh(context.Background(), p.RefreshToken, resource) })
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, agent.ErrInvalidGrant) {
			t.Errorf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d refreshes succeeded, want 1", ok)
	}
}

func TestRefreshRejects(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	for name, token := range map[string]string{
		"empty": "", "access token": p.AccessToken, "unknown": "hmr_" + strings.Repeat("A", 43),
		"bad encoding": "hmr_" + strings.Repeat("*", 43), "wrong length": p.RefreshToken + "A",
	} {
		if _, err := s.Refresh(ctx, token, resource); !errors.Is(err, agent.ErrInvalidGrant) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(ctx, p.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("revoked agent: %v", err)
	}
}

func TestRevokeRevokesAccessAndRefreshTokens(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a, other := register(t, s, "A"), register(t, s, "B")
	p, q := issue(t, s, a.ClientID), issue(t, s, other.ClientID)
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, p.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("access token of revoked agent: %v", err)
	}
	if _, err := s.Refresh(ctx, p.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("refresh token of revoked agent: %v", err)
	}
	if _, err := s.Authenticate(ctx, q.AccessToken, resource); err != nil {
		t.Errorf("other agent affected: %v", err)
	}
}

// E2E scenario 7 at unit level: emergency stop blocks all agents immediately; after
// lifting it, only newly issued tokens work.
func TestEmergencyStop(t *testing.T) {
	s, log, _ := newStore(t)
	ctx := context.Background()
	a, b := register(t, s, "A"), register(t, s, "B")
	pa, pb := issue(t, s, a.ClientID), issue(t, s, b.ClientID)

	if on, err := s.EmergencyStopActive(ctx); err != nil || on {
		t.Fatalf("initially active = %v, %v", on, err)
	}
	if changed, err := s.SetEmergencyStop(ctx, true, admin); err != nil || !changed {
		t.Fatalf("activate = %v, %v", changed, err)
	}
	if changed, err := s.SetEmergencyStop(ctx, true, admin); err != nil || changed {
		t.Errorf("second activation = %v, %v (want no-op)", changed, err)
	}
	if on, _ := s.EmergencyStopActive(ctx); !on {
		t.Error("not active")
	}
	for _, p := range []agent.TokenPair{pa, pb} {
		if _, err := s.Authenticate(ctx, p.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
			t.Errorf("access during stop: %v", err)
		}
		if _, err := s.Refresh(ctx, p.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
			t.Errorf("refresh during stop: %v", err)
		}
	}
	if _, err := s.IssueTokens(ctx, a.ClientID, resource); !errors.Is(err, agent.ErrEmergencyStop) {
		t.Errorf("issue during stop: %v", err)
	}

	if changed, err := s.SetEmergencyStop(ctx, false, admin); err != nil || !changed {
		t.Fatalf("release = %v, %v", changed, err)
	}
	if _, err := s.Authenticate(ctx, pa.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("old token after release: %v", err)
	}
	if _, err := s.Refresh(ctx, pa.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("old refresh token after release: %v", err)
	}
	fresh := issue(t, s, a.ClientID)
	if _, err := s.Authenticate(ctx, fresh.AccessToken, resource); err != nil {
		t.Errorf("new token after release: %v", err)
	}
	out := exported(t, log)
	if strings.Count(out, `"event":"emergency_stop.activated"`) != 1 || strings.Count(out, `"event":"emergency_stop.released"`) != 1 {
		t.Errorf("audit log:\n%s", out)
	}
	if r, err := log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

// The stop is read from the database on every request, so a stop set through another
// process (the CLI) is effective without a restart.
func TestEmergencyStopSetByAnotherStoreTakesEffect(t *testing.T) {
	s, log, db := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	cli := agent.New(db, log)
	if _, err := cli.SetEmergencyStop(ctx, true, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, p.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("Authenticate = %v", err)
	}
}

// Even a token that escaped revocation is refused while the stop is active.
func TestAuthenticateChecksTheStopItself(t *testing.T) {
	s, _, db := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('emergency_stop', 'on', '2026-10-13T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, p.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("Authenticate = %v", err)
	}
	if _, err := s.Refresh(ctx, p.RefreshToken, resource); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("Refresh = %v", err)
	}
}

func TestPurgeExpiredTokens(t *testing.T) {
	s, _, db := newStore(t)
	c := withClock(s)
	ctx := context.Background()
	a := register(t, s, "A")
	issue(t, s, a.ClientID)
	c.Set(t0.Add(31 * 24 * time.Hour))
	keep := issue(t, s, a.ClientID)
	n, err := s.PurgeExpiredTokens(ctx, c.Now())
	if err != nil || n != 2 {
		t.Errorf("purged %d, %v; want the expired pair", n, err)
	}
	var left int
	_ = db.QueryRow(`SELECT count(*) FROM tokens`).Scan(&left)
	if left != 2 {
		t.Errorf("%d tokens left", left)
	}
	if _, err := s.Authenticate(ctx, keep.AccessToken, resource); err != nil {
		t.Errorf("current token: %v", err)
	}
}

func TestTokenFunctionsReportDatabaseErrors(t *testing.T) {
	s, _, db := newStore(t)
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	db.Close()
	ctx := context.Background()
	if _, err := s.IssueTokens(ctx, a.ClientID, resource); err == nil {
		t.Error("IssueTokens succeeded")
	}
	if _, err := s.Authenticate(ctx, p.AccessToken, resource); err == nil || errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("Authenticate = %v, want a database error", err)
	}
	if _, err := s.Refresh(ctx, p.RefreshToken, resource); err == nil || errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("Refresh = %v, want a database error", err)
	}
	if _, err := s.SetEmergencyStop(ctx, true, admin); err == nil {
		t.Error("SetEmergencyStop succeeded")
	}
	if _, err := s.EmergencyStopActive(ctx); err == nil {
		t.Error("EmergencyStopActive succeeded")
	}
	if _, err := s.PurgeExpiredTokens(ctx, time.Now()); err == nil {
		t.Error("PurgeExpiredTokens succeeded")
	}
}
