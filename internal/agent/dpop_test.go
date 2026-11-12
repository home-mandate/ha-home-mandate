// SPDX-License-Identifier: AGPL-3.0-or-later

package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
)

// Thumbprints of two DPoP keys (43 characters of base64url).
var (
	keyA = strings.Repeat("A", 43)
	keyB = strings.Repeat("B", 43)
)

func bind(t *testing.T, s *agent.Store, p agent.TokenPair, h agent.Holder) agent.TokenPair {
	t.Helper()
	bound, err := s.Bind(context.Background(), p, h)
	if err != nil {
		t.Fatalf("Bind(%+v): %v", h, err)
	}
	return bound
}

func binding(t *testing.T, s *agent.Store, clientID string) string {
	t.Helper()
	a, err := s.Get(context.Background(), clientID)
	if err != nil {
		t.Fatal(err)
	}
	return a.Binding
}

// authenticates reports which presented keys an access token is accepted with.
func authenticates(s *agent.Store, token string) map[string]bool {
	out := map[string]bool{}
	for name, jkt := range map[string]string{"bearer": "", "A": keyA, "B": keyB} {
		_, err := s.Authenticate(context.Background(), token, resource, jkt)
		out[name] = err == nil
	}
	return out
}

func TestBindBindsTheFamilyAndTheAgent(t *testing.T) {
	s, log, _ := newStore(t)
	withClock(s)
	ctx := context.Background()
	a := register(t, s, "TS client")
	if binding(t, s, a.ClientID) != agent.BindingNone {
		t.Fatal("a new agent is bound")
	}
	p := bind(t, s, issue(t, s, a.ClientID), agent.Holder{JKT: keyA, Bind: true})
	if p.JKT != keyA || binding(t, s, a.ClientID) != agent.BindingDPoP {
		t.Fatalf("pair %+v, binding %s", p, binding(t, s, a.ClientID))
	}
	if got := authenticates(s, p.AccessToken); got["bearer"] || !got["A"] || got["B"] {
		t.Errorf("bound access token accepted with %v", got)
	}
	// A refresh needs a proof of the same key; a refusal changes nothing.
	for name, h := range map[string]agent.Holder{"no proof": {Bind: true}, "other key": {JKT: keyB, Bind: true}} {
		if _, err := s.Refresh(ctx, p.RefreshToken, resource, "", h); !errors.Is(err, agent.ErrDPoPKey) || errors.Is(err, agent.ErrInvalidGrant) {
			t.Errorf("refresh with %s: %v", name, err)
		}
	}
	next, err := s.Refresh(ctx, p.RefreshToken, resource, "", agent.Holder{JKT: keyA, Bind: true})
	if err != nil || next.JKT != keyA {
		t.Fatalf("refresh with the key: %+v, %v", next, err)
	}
	if got := authenticates(s, next.AccessToken); got["bearer"] || !got["A"] {
		t.Errorf("refreshed access token accepted with %v", got)
	}
	// Within the grace period the same holds (#71).
	if _, err := s.Refresh(ctx, p.RefreshToken, resource, "", agent.Holder{JKT: keyB, Bind: true}); !errors.Is(err, agent.ErrDPoPKey) {
		t.Errorf("repeated refresh with another key: %v", err)
	}
	if again, err := s.Refresh(ctx, p.RefreshToken, resource, "", agent.Holder{JKT: keyA}); err != nil || !again.Repeated || again.JKT != keyA {
		t.Errorf("repeated refresh with the key: %+v, %v", again, err)
	}
	if out := exported(t, log); strings.Count(out, `"error":"invalid_dpop_proof"`) != 3 {
		t.Errorf("audit log:\n%s", out)
	}
}

func TestBindWithoutAProofLeavesBearerTokens(t *testing.T) {
	s, _, _ := newStore(t)
	a := register(t, s, "claude.ai")
	for _, h := range []agent.Holder{{}, {Bind: true}, {JKT: keyA}} { // no proof, or a proof while DPoP is off
		p := bind(t, s, issue(t, s, a.ClientID), h)
		if p.JKT != "" {
			t.Errorf("%+v: bound to %s", h, p.JKT)
		}
		if got := authenticates(s, p.AccessToken); !got["bearer"] || got["A"] || got["B"] {
			t.Errorf("%+v: Bearer token accepted with %v", h, got)
		}
		if _, err := s.Refresh(context.Background(), p.RefreshToken, resource, "", agent.Holder{}); err != nil {
			t.Errorf("%+v: refresh: %v", h, err)
		}
	}
	if binding(t, s, a.ClientID) != agent.BindingNone {
		t.Error("agent bound")
	}
}

func TestBindRequiredWithoutAProofRevokesThePair(t *testing.T) {
	s, log, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	p := issue(t, s, a.ClientID)
	if _, err := s.Bind(ctx, p, agent.Holder{Bind: true, Require: true}); !errors.Is(err, agent.ErrDPoPRequired) {
		t.Fatalf("Bind = %v, want ErrDPoPRequired", err)
	}
	if got := authenticates(s, p.AccessToken); got["bearer"] || got["A"] {
		t.Errorf("refused pair still authenticates: %v", got)
	}
	if _, err := s.Refresh(ctx, p.RefreshToken, resource, "", agent.Holder{JKT: keyA, Bind: true}); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("refused pair still refreshes: %v", err)
	}
	if out := exported(t, log); !strings.Contains(out, `"error":"dpop_required"`) {
		t.Errorf("audit log:\n%s", out)
	}
}

// A Bearer agent that sends its first proof becomes bound, and there is no way back.
func TestABearerAgentBecomesBoundAtRefresh(t *testing.T) {
	s, _, db := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	old := bind(t, s, issue(t, s, a.ClientID), agent.Holder{})
	next, err := s.Refresh(ctx, old.RefreshToken, resource, "", agent.Holder{JKT: keyA, Bind: true})
	if err != nil || next.JKT != keyA || binding(t, s, a.ClientID) != agent.BindingDPoP {
		t.Fatalf("refresh with a first proof: %+v, %v, %s", next, err, binding(t, s, a.ClientID))
	}
	// The Bearer access token of before no longer counts: the agent is bound.
	if got := authenticates(s, old.AccessToken); got["bearer"] || got["A"] {
		t.Errorf("old Bearer token accepted with %v", got)
	}
	if _, err := s.Refresh(ctx, next.RefreshToken, resource, "", agent.Holder{}); !errors.Is(err, agent.ErrDPoPKey) {
		t.Errorf("refresh without a proof: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE agents SET binding = 'none' WHERE client_id = '` + a.ClientID + `'`,
		`UPDATE tokens SET dpop_jkt = NULL WHERE dpop_jkt IS NOT NULL`,
		`UPDATE tokens SET dpop_jkt = '` + keyB + `' WHERE dpop_jkt IS NOT NULL`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Errorf("%s succeeded", stmt)
		}
	}
}

// A bound agent gets no Bearer tokens any more, also not in a new family after a
// reconnect, while DPoP is switched off, or at a refresh of a family from before; a new
// key is fine (the client lost its key).
func TestABoundAgentGetsNoBearerTokens(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	before := bind(t, s, issue(t, s, a.ClientID), agent.Holder{})
	bind(t, s, issue(t, s, a.ClientID), agent.Holder{JKT: keyA, Bind: true})
	if _, err := s.Refresh(ctx, before.RefreshToken, resource, "", agent.Holder{}); !errors.Is(err, agent.ErrDPoPRequired) {
		t.Errorf("refresh of a Bearer family of a bound agent: %v", err)
	}
	if _, err := s.Bind(ctx, issue(t, s, a.ClientID), agent.Holder{}); !errors.Is(err, agent.ErrDPoPRequired) {
		t.Errorf("Bearer pair for a bound agent: %v", err)
	}
	// With DPoP off (Bind false) a bound agent still binds, to any key it proves.
	if p := bind(t, s, issue(t, s, a.ClientID), agent.Holder{JKT: keyB}); p.JKT != keyB {
		t.Errorf("new key while DPoP is off: %+v", p)
	}
}

func TestStillAuthorizedChecksOnlyTheToken(t *testing.T) {
	s, _, _ := newStore(t)
	c := withClock(s)
	a := register(t, s, "A")
	p := bind(t, s, issue(t, s, a.ClientID), agent.Holder{JKT: keyA, Bind: true})
	c.Set(t0.Add(time.Hour))
	if _, err := s.StillAuthorized(context.Background(), p.AccessToken, resource); err != nil {
		t.Errorf("StillAuthorized of a bound token: %v", err)
	}
}

func TestBindRejects(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	a := register(t, s, "A")
	for _, jkt := range []string{"short", strings.Repeat("A", 42) + "=", strings.Repeat("A", 44)} {
		if _, err := s.Bind(ctx, issue(t, s, a.ClientID), agent.Holder{JKT: jkt, Bind: true}); err == nil || errors.Is(err, agent.ErrDPoP) {
			t.Errorf("thumbprint %q: %v", jkt, err)
		}
		if _, err := s.Refresh(ctx, issue(t, s, a.ClientID).RefreshToken, resource, "", agent.Holder{JKT: jkt, Bind: true}); err == nil {
			t.Errorf("refresh with thumbprint %q accepted", jkt)
		}
	}
	if _, err := s.Bind(ctx, agent.TokenPair{RefreshToken: "hmr_" + strings.Repeat("A", 43)}, agent.Holder{}); !errors.Is(err, agent.ErrInvalidGrant) {
		t.Errorf("unknown pair: %v", err)
	}
	p := bind(t, s, issue(t, s, a.ClientID), agent.Holder{JKT: keyA, Bind: true})
	if _, err := s.Bind(ctx, p, agent.Holder{JKT: keyB, Bind: true}); !errors.Is(err, agent.ErrDPoPKey) {
		t.Errorf("binding a bound pair to another key: %v", err)
	}
}
