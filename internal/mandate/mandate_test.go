// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mandatespec "github.com/mandate-spec/mandate-spec"
	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/store"
)

const household = "household:hm-0123456789ab"

var admin = audit.Actor{Kind: audit.ActorUser, ID: "user-1"}

type env struct {
	mandates *mandate.Store
	agents   *agent.Store
	log      *audit.Log
}

func newEnv(t *testing.T) env {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log := audit.New(s.DB(), household)
	return env{mandates: mandate.New(s.DB(), log, household), agents: agent.New(s.DB(), log), log: log}
}

func (e env) agent(t *testing.T, name string) agent.Agent {
	t.Helper()
	a, err := e.agents.Register(context.Background(), name, admin)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// voiceAssistant returns the example mandate of mandate-spec for clientID, with edit
// applied to the decoded document.
func voiceAssistant(t *testing.T, clientID string, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["principal"] = household
	doc["agent"] = map[string]any{"client_id": clientID, "display_name": "Voice assistant"}
	if edit != nil {
		edit(doc)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPutStoresAValidMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")

	info, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "m-voice-assistant" || info.ClientID != a.ClientID || info.Status != mandate.StatusActive ||
		!strings.HasPrefix(info.Digest, "sha256:") || info.MaxActionsPerHour != 60 {
		t.Errorf("info = %+v", info)
	}

	loaded, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	res := evaluator.Evaluate(loaded.Mandate, evaluator.Request{
		Resource: evaluator.Resource{EntityID: "light.kitchen", Category: "light"},
		Action:   "turn_on", Time: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), TimeZone: "Europe/Berlin",
		Status: loaded.Status,
	})
	if res.Decision != evaluator.Allow || res.MandateDigest != info.Digest {
		t.Errorf("evaluation = %+v", res)
	}
}

func TestPutRejectsEveryInvalidConformanceCase(t *testing.T) {
	e := newEnv(t)
	data, err := fs.ReadFile(mandatespec.FS(), mandatespec.InvalidCasesPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			ID     string          `json:"id"`
			Inline json.RawMessage `json:"mandate_inline"`
			Raw    *string         `json:"mandate_raw"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for _, c := range file.Cases {
		doc := []byte(c.Inline)
		if c.Raw != nil {
			doc = []byte(*c.Raw)
		}
		if _, err := e.mandates.Put(context.Background(), doc, admin); !errors.Is(err, mandate.ErrInvalid) {
			t.Errorf("%s: Put = %v, want ErrInvalid", c.ID, err)
		}
	}
}

func TestPutRejectsMandatesForOtherPrincipalsOrAgents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	revoked := e.agent(t, "Gone")
	if err := e.agents.Revoke(ctx, revoked.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"other household": voiceAssistant(t, a.ClientID, func(d map[string]any) { d["principal"] = "household:someone-else" }),
		"person":          voiceAssistant(t, a.ClientID, func(d map[string]any) { d["principal"] = "person:p-1" }),
		"unknown agent":   voiceAssistant(t, "hm-client:nobody-00000000", nil),
		"revoked agent":   voiceAssistant(t, revoked.ClientID, nil),
	}
	for name, doc := range tests {
		if _, err := e.mandates.Put(ctx, doc, admin); !errors.Is(err, mandate.ErrInvalid) {
			t.Errorf("%s: Put = %v, want ErrInvalid", name, err)
		}
	}
}

func TestVersionsAndAuditTrail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")

	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	// Same content (with different whitespace) is no new version.
	same := voiceAssistant(t, a.ClientID, nil)
	if again, err := e.mandates.Put(ctx, append([]byte(" "), same...), admin); err != nil || again.Digest != first.Digest {
		t.Fatalf("unchanged Put = %+v, %v", again, err)
	}
	second, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, func(d map[string]any) {
		d["limits"] = map[string]any{"max_actions_per_hour": 10}
	}), admin)
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest == first.Digest || second.MaxActionsPerHour != 10 {
		t.Errorf("second = %+v", second)
	}
	versions, err := e.mandates.Versions(ctx, first.ID)
	if err != nil || len(versions) != 2 || versions[0].Digest != first.Digest || versions[1].Digest != second.Digest {
		t.Errorf("Versions = %+v, %v", versions, err)
	}

	var buf strings.Builder
	if err := e.log.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, `"event":"mandate.created"`) != 1 || strings.Count(out, `"event":"mandate.updated"`) != 1 ||
		!strings.Contains(out, `"previous_digest":"`+first.Digest+`"`) {
		t.Errorf("audit log:\n%s", out)
	}
	if strings.Contains(out, "r-lights") {
		t.Error("audit log contains mandate content")
	}
	if r, err := e.log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

func TestOneMandatePerAgent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.agent(t, "A"), e.agent(t, "B")
	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin); err != nil {
		t.Fatal(err)
	}
	other := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["id"] = "m-second" })
	if _, err := e.mandates.Put(ctx, other, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("second mandate for the same agent = %v, want ErrConflict", err)
	}
	moved := voiceAssistant(t, b.ClientID, nil) // same mandate id, other agent
	if _, err := e.mandates.Put(ctx, moved, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("mandate moved to another agent = %v, want ErrConflict", err)
	}
}

func TestRevokeIsPassedToTheEvaluation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	info, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.Revoke(ctx, info.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.Revoke(ctx, info.ID, admin); err != nil {
		t.Errorf("second Revoke: %v", err)
	}
	loaded, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	res := evaluator.Evaluate(loaded.Mandate, evaluator.Request{
		Resource: evaluator.Resource{EntityID: "light.kitchen", Category: "light"},
		Action:   "turn_on", Time: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), TimeZone: "UTC", Status: loaded.Status,
	})
	if res.Decision != evaluator.Deny || res.Reason != evaluator.ReasonRevoked {
		t.Errorf("evaluation after revoke = %+v", res)
	}
	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("Put on a revoked mandate = %v, want ErrConflict", err)
	}
}

func TestLookupsOfMissingMandates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.mandates.ForAgent(ctx, "hm-client:nobody-00000000"); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("ForAgent = %v, want ErrNotFound", err)
	}
	if err := e.mandates.Revoke(ctx, "m-none", admin); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("Revoke = %v, want ErrNotFound", err)
	}
	if list, err := e.mandates.List(ctx); err != nil || len(list) != 0 {
		t.Errorf("List = %+v, %v", list, err)
	}
}

func TestList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "A")
	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin); err != nil {
		t.Fatal(err)
	}
	list, err := e.mandates.List(ctx)
	if err != nil || len(list) != 1 || list[0].ClientID != a.ClientID {
		t.Errorf("List = %+v, %v", list, err)
	}
}
