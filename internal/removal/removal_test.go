// SPDX-License-Identifier: AGPL-3.0-or-later

package removal_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/spec"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/removal"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

// These tests write mandate.removed and agent.removed: they need a specification with
// those events (home-mandate/spec v0.1.0-alpha.3).

const household = "household:hm-0123456789ab"

var admin = audit.Actor{Kind: audit.ActorUser, ID: "user-1"}

type env struct {
	st       *store.Store
	log      *audit.Log
	agents   *agent.Store
	mandates *mandate.Store
	svc      *removal.Service
}

func newEnv(t *testing.T) env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), household)
	agents := agent.New(st.DB(), log)
	mandates := mandate.New(st.DB(), log, household, "urn:uuid:5b0c9f4e-8f1a-4c2e-9d3b-7a6e5f4d3c2b")
	return env{st: st, log: log, agents: agents, mandates: mandates, svc: removal.New(st.DB(), agents, mandates)}
}

// agentWithMandate registers an agent with the example mandate of the specification
// under the ID m-<suffix>.
func (e env) agentWithMandate(t *testing.T, name, suffix string) (agent.Agent, mandate.Info) {
	t.Helper()
	ctx := context.Background()
	a, err := e.agents.Register(ctx, name, admin)
	if err != nil {
		t.Fatal(err)
	}
	return a, e.mandateFor(t, a, suffix)
}

func (e env) mandateFor(t *testing.T, a agent.Agent, suffix string) mandate.Info {
	t.Helper()
	data, err := fs.ReadFile(spec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["id"], doc["principal"] = "m-"+suffix, household
	doc["agent"] = map[string]any{"client_id": a.ClientID, "display_name": a.DisplayName}
	out, _ := json.Marshal(doc)
	info, err := e.mandates.Put(context.Background(), out, admin)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// events returns the events of the log after seq, in order.
func (e env) events(t *testing.T, after int) []string {
	t.Helper()
	var buf strings.Builder
	if err := e.log.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var entry struct {
			Seq   int    `json:"seq"`
			Event string `json:"event"`
			Actor struct {
				Kind string `json:"kind"`
			} `json:"actor"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Seq > after {
			out = append(out, entry.Event+"/"+entry.Actor.Kind)
		}
	}
	return out
}

func (e env) last(t *testing.T) int {
	t.Helper()
	var seq int
	if err := e.st.DB().QueryRow(`SELECT coalesce(max(seq), 0) FROM audit_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

func (e env) verify(t *testing.T) {
	t.Helper()
	if r, err := e.log.Verify(context.Background()); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

func TestRemoveAgentNeedsItRevoked(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, m := e.agentWithMandate(t, "Kitchen", "kitchen")
	if _, err := e.svc.RemoveAgent(ctx, a.ClientID, removal.AgentOptions{Mandates: true}, admin); !errors.Is(err, agent.ErrNotRevoked) {
		t.Fatalf("remove active = %v", err)
	}
	if _, err := e.svc.RemoveAgent(ctx, "hm-client:none-00000000", removal.AgentOptions{}, admin); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("remove unknown = %v", err)
	}
	if got, _ := e.agents.Get(ctx, a.ClientID); got.Status != agent.StatusActive {
		t.Errorf("agent after refused removal = %+v", got)
	}
	if got, _ := e.mandates.Get(ctx, m.ID); got.Status != mandate.StatusActive {
		t.Errorf("mandate after refused removal = %+v", got)
	}
}

// Revoking and removing in one step records the revocations first (SPEC-v0 section 11.3).
func TestRevokeAndRemoveInOneStep(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, m := e.agentWithMandate(t, "Kitchen", "kitchen")
	seq := e.last(t)
	res, err := e.svc.RemoveAgent(ctx, a.ClientID, removal.AgentOptions{Revoke: true, Mandates: true}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Revoked || !slices.Equal(res.Agents, []string{a.ClientID}) || !slices.Equal(res.Mandates, []string{m.ID}) {
		t.Errorf("result = %+v", res)
	}
	want := []string{"agent.revoked/user", "mandate.revoked/user", "mandate.removed/user", "agent.removed/user"}
	if got := e.events(t, seq); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	e.verify(t)
}

// Removing an agent does not remove its mandates unless asked.
func TestRemoveAgentKeepsItsMandates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, m := e.agentWithMandate(t, "Kitchen", "kitchen")
	seq := e.last(t)
	res, err := e.svc.RemoveAgent(ctx, a.ClientID, removal.AgentOptions{Revoke: true}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mandates) != 0 {
		t.Errorf("result = %+v", res)
	}
	if got := e.events(t, seq); !slices.Equal(got, []string{"agent.revoked/user", "mandate.revoked/user", "agent.removed/user"}) {
		t.Errorf("events = %v", got)
	}
	got, _ := e.mandates.Get(ctx, m.ID)
	if got.Status != mandate.StatusRevoked || !got.RemovedAt.IsZero() {
		t.Errorf("mandate = %+v", got)
	}
	// Its revoked mandates can be removed later with it: a second removal of the agent
	// removes nothing more of the agent itself.
	seq = e.last(t)
	res, err = e.svc.RemoveAgent(ctx, a.ClientID, removal.AgentOptions{Mandates: true}, admin)
	if err != nil || res.Revoked || len(res.Agents) != 0 || !slices.Equal(res.Mandates, []string{m.ID}) {
		t.Errorf("second removal = %+v, %v", res, err)
	}
	if got := e.events(t, seq); !slices.Equal(got, []string{"mandate.removed/user"}) {
		t.Errorf("events = %v", got)
	}
}

func TestRemoveMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, m := e.agentWithMandate(t, "Kitchen", "kitchen")
	if err := e.svc.RemoveMandate(ctx, m.ID, admin); !errors.Is(err, mandate.ErrNotRevoked) {
		t.Fatalf("remove active = %v", err)
	}
	if err := e.mandates.Revoke(ctx, m.ID, admin); err != nil {
		t.Fatal(err)
	}
	seq := e.last(t)
	if err := e.svc.RemoveMandate(ctx, m.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveMandate(ctx, m.ID, admin); err != nil {
		t.Errorf("second removal = %v", err)
	}
	if got := e.events(t, seq); !slices.Equal(got, []string{"mandate.removed/user"}) {
		t.Errorf("events = %v", got)
	}
}

func TestRemoveRevoked(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	active, activeMandate := e.agentWithMandate(t, "Active", "active")
	gone, goneMandate := e.agentWithMandate(t, "Gone", "gone")
	if err := e.mandates.Revoke(ctx, activeMandate.ID, admin); err != nil {
		t.Fatal(err)
	}
	second := e.mandateFor(t, active, "active-2")
	if _, err := e.svc.RemoveAgent(ctx, gone.ClientID, removal.AgentOptions{Revoke: true}, admin); err != nil {
		t.Fatal(err)
	}
	// An agent revoked on the command line keeps an active mandate: it is revoked too.
	legacy, legacyMandate := e.agentWithMandate(t, "Legacy", "legacy")
	if err := e.agents.Revoke(ctx, legacy.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RemoveRevoked(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(res.Mandates)
	if !slices.Equal(res.Agents, []string{legacy.ClientID}) ||
		!slices.Equal(res.Mandates, []string{activeMandate.ID, goneMandate.ID, legacyMandate.ID}) {
		t.Errorf("result = %+v", res)
	}
	for _, id := range []string{activeMandate.ID, goneMandate.ID, legacyMandate.ID} {
		if got, _ := e.mandates.Get(ctx, id); got.RemovedAt.IsZero() || got.Status != mandate.StatusRevoked {
			t.Errorf("%s = %+v", id, got)
		}
	}
	if got, _ := e.mandates.Get(ctx, second.ID); got.Status != mandate.StatusActive || !got.RemovedAt.IsZero() {
		t.Errorf("active mandate = %+v", got)
	}
	if got, _ := e.agents.Get(ctx, active.ClientID); !got.RemovedAt.IsZero() {
		t.Errorf("active agent = %+v", got)
	}
	if res, err := e.svc.RemoveRevoked(ctx, admin); err != nil || len(res.Agents)+len(res.Mandates) != 0 {
		t.Errorf("again = %+v, %v", res, err)
	}
	e.verify(t)
}

// truncateAll deletes every entry but the newest, which is about nobody.
func (e env) truncateAll(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.log.Append(ctx, audit.Entry{Event: audit.EventEmergencyStopReleased, Actor: &admin}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.log.Truncate(ctx, time.Now().Add(time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil {
		t.Fatal(err)
	}
}

// The retention removes revoked agents and mandates once the audit log no longer holds
// entries about them, as the system, and deletes the data of removed ones once no entry
// refers to them (also not their removal).
func TestExpire(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	revoked, revokedMandate := e.agentWithMandate(t, "Revoked", "revoked")
	removed, removedMandate := e.agentWithMandate(t, "Removed", "removed")
	kept, keptMandate := e.agentWithMandate(t, "Kept", "kept")
	if err := e.agents.Revoke(ctx, revoked.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.Revoke(ctx, revokedMandate.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RemoveAgent(ctx, removed.ClientID, removal.AgentOptions{Revoke: true, Mandates: true}, admin); err != nil {
		t.Fatal(err)
	}
	// While entries refer to them, nothing happens.
	if got, err := e.svc.Expire(ctx); err != nil || got.Count() != 0 {
		t.Fatalf("Expire with entries = %+v, %v", got, err)
	}

	e.truncateAll(t)
	seq := e.last(t)
	got, err := e.svc.Expire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Removed.Agents, []string{revoked.ClientID}) || !slices.Equal(got.Removed.Mandates, []string{revokedMandate.ID}) ||
		!slices.Equal(got.Purged.Agents, []string{removed.ClientID}) || !slices.Equal(got.Purged.Mandates, []string{removedMandate.ID}) {
		t.Errorf("first Expire = %+v", got)
	}
	// Removed by a human before: no second entry. Removed now: by the system.
	if events := e.events(t, seq); !slices.Equal(events, []string{"mandate.removed/system", "agent.removed/system"}) {
		t.Errorf("events = %v", events)
	}
	if _, err := e.agents.Get(ctx, removed.ClientID); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("purged agent: %v", err)
	}
	if name, _ := e.agents.Name(ctx, removed.ClientID); name != "Removed" {
		t.Errorf("tombstone name = %q", name)
	}
	if a, err := e.agents.Get(ctx, revoked.ClientID); err != nil || a.RemovedBy != "retention" {
		t.Errorf("removed by the system = %+v, %v", a, err)
	}

	// Once the system's removal has left the log too, their data goes as well.
	e.truncateAll(t)
	got, err = e.svc.Expire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Removed.Agents)+len(got.Removed.Mandates) != 0 ||
		!slices.Equal(got.Purged.Agents, []string{revoked.ClientID}) || !slices.Equal(got.Purged.Mandates, []string{revokedMandate.ID}) {
		t.Errorf("second Expire = %+v", got)
	}
	for _, id := range []string{kept.ClientID} {
		if a, err := e.agents.Get(ctx, id); err != nil || a.Status != agent.StatusActive {
			t.Errorf("active agent = %+v, %v", a, err)
		}
	}
	if m, err := e.mandates.Get(ctx, keptMandate.ID); err != nil || m.Status != mandate.StatusActive {
		t.Errorf("active mandate = %+v, %v", m, err)
	}
}

// A failure stops at once: what was done before stays, nothing is half done.
func TestFailuresAreReported(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, _ := e.agentWithMandate(t, "Kitchen", "kitchen")
	if _, err := e.svc.RemoveAgent(ctx, a.ClientID, removal.AgentOptions{Revoke: true}, admin); err != nil {
		t.Fatal(err)
	}
	b, _ := e.agentWithMandate(t, "Office", "office")
	if err := e.agents.Revoke(ctx, b.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	// A removal the database refuses rolls the agent's removal back.
	if _, err := e.st.DB().Exec(`CREATE TRIGGER refuse BEFORE UPDATE OF removed_at ON mandates BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RemoveRevoked(ctx, admin); err == nil {
		t.Error("RemoveRevoked with a failing mandate removal succeeded")
	}
	if got, _ := e.agents.Get(ctx, b.ClientID); !got.RemovedAt.IsZero() {
		t.Errorf("agent removed although its mandate was not: %+v", got)
	}
	if _, err := e.svc.RemoveAgent(ctx, b.ClientID, removal.AgentOptions{Mandates: true}, admin); err == nil {
		t.Error("RemoveAgent with a failing mandate removal succeeded")
	}
	e.truncateAll(t)
	if _, err := e.svc.Expire(ctx); err == nil {
		t.Error("Expire with a failing mandate removal succeeded")
	}
	_ = e.st.Close()
	if _, err := e.svc.RemoveRevoked(ctx, admin); err == nil {
		t.Error("RemoveRevoked on a closed database succeeded")
	}
	if _, err := e.svc.Expire(ctx); err == nil {
		t.Error("Expire on a closed database succeeded")
	}
	if err := e.svc.RemoveMandate(ctx, "m-kitchen", admin); err == nil {
		t.Error("RemoveMandate on a closed database succeeded")
	}
}
