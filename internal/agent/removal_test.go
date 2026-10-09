// SPDX-License-Identifier: AGPL-3.0-or-later

package agent_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// These tests write agent.removed and agent.reconnected: they need a specification with
// those events (home-mandate/spec v0.1.0-alpha.3).

var system = audit.Actor{Kind: audit.ActorSystem, ID: "retention"}

func inTx(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func remove(t *testing.T, s *agent.Store, db *sql.DB, clientID string, by audit.Actor) (bool, error) {
	t.Helper()
	var removed bool
	err := inTx(t, db, func(tx *sql.Tx) error {
		var err error
		removed, err = s.RemoveTx(context.Background(), tx, clientID, by)
		return err
	})
	return removed, err
}

func admitted(t *testing.T, s *agent.Store, db *sql.DB, name string, client agent.Client) agent.Agent {
	t.Helper()
	var a agent.Agent
	if err := inTx(t, db, func(tx *sql.Tx) error {
		var err error
		a, err = s.RegisterTx(context.Background(), tx, name, client, admin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOnlyARevokedAgentIsRemoved(t *testing.T) {
	s, log, db := newStore(t)
	withClock(s)
	ctx := context.Background()
	a := register(t, s, "Voice assistant")
	if _, err := remove(t, s, db, a.ClientID, admin); !errors.Is(err, agent.ErrNotRevoked) {
		t.Fatalf("remove active = %v, want ErrNotRevoked", err)
	}
	if _, err := remove(t, s, db, "hm-client:unknown-00000000", admin); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("remove unknown = %v", err)
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if removed, err := remove(t, s, db, a.ClientID, admin); err != nil || !removed {
		t.Fatalf("remove revoked = %v, %v", removed, err)
	}
	if removed, err := remove(t, s, db, a.ClientID, system); err != nil || removed {
		t.Errorf("second removal = %v, %v (want no-op)", removed, err)
	}
	got, err := s.Get(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != agent.StatusRevoked || !got.RemovedAt.Equal(t0) || got.RemovedBy != admin.ID {
		t.Errorf("removed agent = %+v", got)
	}
	out := exported(t, log)
	if strings.Count(out, `"event":"agent.removed"`) != 1 ||
		!strings.Contains(out, `"actor":{"id":"user-1","kind":"user"},"agent":{"client_id":"`+a.ClientID+`","display_name":"Voice assistant"},"event":"agent.removed"`) {
		t.Errorf("audit log:\n%s", out)
	}
	if r, err := log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
	// A removed agent stays revoked and gets no tokens.
	if _, err := s.IssueTokens(ctx, a.ClientID, resource); !errors.Is(err, agent.ErrRevoked) {
		t.Errorf("tokens for a removed agent: %v", err)
	}
}

func TestPurgeKeepsATombstone(t *testing.T) {
	s, _, db := newStore(t)
	withClock(s)
	ctx := context.Background()
	a := admitted(t, s, db, "Voice assistant", agent.Client{ID: "https://client.example/cimd", Verified: true,
		RedirectURIs: []string{"https://client.example/cb"}})
	issue(t, s, a.ClientID)
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	purge := func() error {
		return inTx(t, db, func(tx *sql.Tx) error { return s.PurgeTx(ctx, tx, a.ClientID) })
	}
	if err := purge(); !errors.Is(err, agent.ErrNotRemoved) {
		t.Fatalf("purge before removal = %v", err)
	}
	if _, err := remove(t, s, db, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if err := purge(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, a.ClientID); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("Get of a purged agent = %v", err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("List = %+v", list)
	}
	var name, oauth, uris, createdBy, revokedBy string
	var tokens int
	if err := db.QueryRow(`SELECT display_name, oauth_client, redirect_uris, created_by, revoked_by,
		(SELECT count(*) FROM tokens WHERE client_id = agents.client_id) FROM agents WHERE client_id = ? AND purged_at IS NOT NULL`, a.ClientID).
		Scan(&name, &oauth, &uris, &createdBy, &revokedBy, &tokens); err != nil {
		t.Fatal(err)
	}
	if name != "Voice assistant" || oauth != "" || uris != "[]" || createdBy != "" || revokedBy != "" || tokens != 0 {
		t.Errorf("tombstone: name %q, oauth %q, uris %s, by %q/%q, %d tokens", name, oauth, uris, createdBy, revokedBy, tokens)
	}
	if name, err := s.Name(ctx, a.ClientID); err != nil || name != "Voice assistant" {
		t.Errorf("Name of a purged agent = %q, %v", name, err)
	}
	if err := purge(); err != nil {
		t.Errorf("second purge = %v", err)
	}
}

func TestDisconnectedAgentsOfAClient(t *testing.T) {
	s, _, db := newStore(t)
	clk := withClock(s)
	ctx := context.Background()
	cimd := agent.Client{ID: "https://client.example/cimd", Verified: true}
	stopped := admitted(t, s, db, "Kitchen", cimd)
	connected := admitted(t, s, db, "Office", cimd)
	revoked := admitted(t, s, db, "Old", cimd)
	other := admitted(t, s, db, "Other", agent.Client{ID: "https://other.example/cimd", Verified: true})
	unverified := admitted(t, s, db, "Claims the same", agent.Client{ID: cimd.ID})
	for _, a := range []agent.Agent{stopped, connected, revoked, other, unverified} {
		issue(t, s, a.ClientID)
	}
	if list, err := s.Disconnected(ctx, cimd); err != nil || len(list) != 0 {
		t.Fatalf("before the stop = %+v, %v", list, err)
	}
	if _, err := s.SetEmergencyStop(ctx, true, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEmergencyStop(ctx, false, admin); err != nil {
		t.Fatal(err)
	}
	issue(t, s, connected.ClientID) // signed in again as itself, e.g. by reconnecting
	if err := s.Revoke(ctx, revoked.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	list, err := s.Disconnected(ctx, cimd)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ClientID != stopped.ClientID || list[0].Connected {
		t.Errorf("disconnected = %+v, want only %s", list, stopped.ClientID)
	}
	if list, _ := s.Disconnected(ctx, agent.Client{ID: cimd.ID}); len(list) != 1 || list[0].ClientID != unverified.ClientID {
		t.Errorf("unverified client = %+v, want only %s", list, unverified.ClientID)
	}
	if list, _ := s.Disconnected(ctx, agent.Client{}); len(list) != 0 {
		t.Errorf("no client = %+v", list)
	}
	all, _ := s.List(ctx)
	for _, a := range all {
		if want := a.ClientID == connected.ClientID; a.Connected != want {
			t.Errorf("%s connected = %v, want %v", a.DisplayName, a.Connected, want)
		}
	}
	// Tokens that expired count as none.
	clk.Set(t0.Add(agent.RefreshTokenTTL + time.Minute))
	if list, _ := s.Disconnected(ctx, cimd); len(list) != 2 {
		t.Errorf("after the refresh tokens expired = %+v", list)
	}
}

func TestReconnectIssuesNewTokensToTheExistingAgent(t *testing.T) {
	s, log, db := newStore(t)
	withClock(s)
	ctx := context.Background()
	cimd := agent.Client{ID: "https://client.example/cimd", Verified: true}
	a := admitted(t, s, db, "Kitchen", cimd)
	old := issue(t, s, a.ClientID)
	reconnect := func(clientID string, client agent.Client) (agent.TokenPair, error) {
		var p agent.TokenPair
		err := inTx(t, db, func(tx *sql.Tx) error {
			var err error
			p, err = s.ReconnectTx(ctx, tx, clientID, client, resource, admin)
			return err
		})
		return p, err
	}
	if _, err := reconnect(a.ClientID, cimd); !errors.Is(err, agent.ErrNotReconnectable) {
		t.Fatalf("reconnect with valid tokens = %v", err)
	}
	if _, err := s.SetEmergencyStop(ctx, true, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := reconnect(a.ClientID, cimd); !errors.Is(err, agent.ErrEmergencyStop) {
		t.Errorf("reconnect during the stop = %v", err)
	}
	if _, err := s.SetEmergencyStop(ctx, false, admin); err != nil {
		t.Fatal(err)
	}
	for _, c := range []agent.Client{{ID: "https://other.example/cimd", Verified: true}, {ID: cimd.ID}} {
		if _, err := reconnect(a.ClientID, c); !errors.Is(err, agent.ErrNotReconnectable) {
			t.Errorf("reconnect for client %+v = %v", c, err)
		}
	}
	if _, err := reconnect("hm-client:unknown-00000000", cimd); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("reconnect unknown = %v", err)
	}
	fresh, err := reconnect(a.ClientID, cimd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Authenticate(ctx, fresh.AccessToken, resource)
	if err != nil || got.ClientID != a.ClientID {
		t.Errorf("new token = %+v, %v", got, err)
	}
	if _, err := s.Authenticate(ctx, old.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("withdrawn token after reconnect: %v", err)
	}
	if _, err := s.Refresh(ctx, old.RefreshToken, resource, cimd.ID); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("withdrawn refresh token after reconnect: %v", err)
	}
	if _, err := s.Refresh(ctx, fresh.RefreshToken, resource, cimd.ID); err != nil {
		t.Errorf("refresh of the new tokens: %v", err)
	}
	out := exported(t, log)
	if strings.Count(out, `"event":"agent.reconnected"`) != 1 ||
		!strings.Contains(out, `"actor":{"id":"user-1","kind":"user"},"agent":{"client_id":"`+a.ClientID+`","display_name":"Kitchen"},"event":"agent.reconnected"`) {
		t.Errorf("audit log:\n%s", out)
	}
	if list, _ := s.List(ctx); len(list) != 1 {
		t.Errorf("agents after reconnect = %+v", list)
	}
	// A revoked agent is never reconnected.
	if _, err := s.SetEmergencyStop(ctx, true, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEmergencyStop(ctx, false, admin); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := reconnect(a.ClientID, cimd); !errors.Is(err, agent.ErrRevoked) {
		t.Errorf("reconnect revoked = %v", err)
	}
	if r, err := log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

func TestReconnectNeedsAHuman(t *testing.T) {
	s, _, db := newStore(t)
	ctx := context.Background()
	cimd := agent.Client{ID: "https://client.example/cimd", Verified: true}
	a := admitted(t, s, db, "Kitchen", cimd)
	err := inTx(t, db, func(tx *sql.Tx) error {
		_, err := s.ReconnectTx(ctx, tx, a.ClientID, cimd, resource, system)
		return err
	})
	if err == nil || errors.Is(err, agent.ErrNotReconnectable) {
		t.Errorf("reconnect by the system = %v", err)
	}
}
