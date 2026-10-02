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

func TestRevokedAgentRevokesItsMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	if _, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin); err != nil {
		t.Fatal(err)
	}
	if err := e.agents.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	loaded, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil || loaded.Status != evaluator.StatusRevoked {
		t.Errorf("ForAgent after agent revocation = %v, %v; want revoked", loaded.Status, err)
	}
}

// withRule returns an edit that appends rule to the mandate's rules.
func withRule(rule map[string]any) func(map[string]any) {
	return func(d map[string]any) {
		rules, _ := d["rules"].([]any)
		d["rules"] = append(append([]any{}, rules...), rule)
	}
}

func unlockWithoutApproval() map[string]any {
	return map[string]any{"id": "r-unlock", "resource": map[string]any{"category": "lock"},
		"actions": []any{"read", "unlock"}, "decision": "allow", "allow_critical": true}
}

func TestUpdateNeedsTheVersionTheEditStartedFrom(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	limit := func(n int) []byte {
		return voiceAssistant(t, a.ClientID, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": n} })
	}

	second, err := e.mandates.Update(ctx, first.ID, limit(10), mandate.Change{BaseDigest: first.Digest}, admin)
	if err != nil || second.Digest == first.Digest || second.MaxActionsPerHour != 10 {
		t.Fatalf("Update = %+v, %v", second, err)
	}

	// Someone who still edits the first version must not overwrite the second one.
	for name, base := range map[string]string{"outdated": first.Digest, "missing": "", "unknown": "sha256:0000"} {
		if _, err := e.mandates.Update(ctx, first.ID, limit(20), mandate.Change{BaseDigest: base}, admin); !errors.Is(err, mandate.ErrConflict) {
			t.Errorf("%s base digest: err = %v, want ErrConflict", name, err)
		}
	}
	if got, err := e.mandates.Get(ctx, first.ID); err != nil || got.Digest != second.Digest {
		t.Errorf("after refused updates: %+v, %v", got, err)
	}
	if versions, err := e.mandates.Versions(ctx, first.ID); err != nil || len(versions) != 2 {
		t.Errorf("Versions = %+v, %v", versions, err)
	}
}

func TestUpdateRefusesWhatDoesNotBelongToTheMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	change := mandate.Change{BaseDigest: first.Digest}

	if _, err := e.mandates.Update(ctx, "m-unknown", voiceAssistant(t, a.ClientID, nil), change, admin); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("unknown mandate: err = %v, want ErrNotFound", err)
	}
	other := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["id"] = "m-other" })
	if _, err := e.mandates.Update(ctx, first.ID, other, change, admin); !errors.Is(err, mandate.ErrInvalid) {
		t.Errorf("document of another mandate: err = %v, want ErrInvalid", err)
	}
	if _, err := e.mandates.Update(ctx, first.ID, []byte(`{"default":"allow"}`), change, admin); !errors.Is(err, mandate.ErrInvalid) {
		t.Errorf("invalid document: err = %v, want ErrInvalid", err)
	}
	if err := e.mandates.Revoke(ctx, first.ID, admin); err != nil {
		t.Fatal(err)
	}
	limited := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	if _, err := e.mandates.Update(ctx, first.ID, limited, change, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("revoked mandate: err = %v, want ErrConflict", err)
	}
	if versions, err := e.mandates.Versions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Errorf("Versions = %+v, %v", versions, err)
	}
}

func TestUpdateNeedsTheConfirmationForCriticalActions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	current, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	update := func(doc []byte, confirm bool) error {
		t.Helper()
		info, err := e.mandates.Update(ctx, current.ID, doc, mandate.Change{BaseDigest: current.Digest, ConfirmCritical: confirm}, admin)
		if err == nil {
			current = info
		}
		return err
	}
	granted := voiceAssistant(t, a.ClientID, withRule(unlockWithoutApproval()))

	// A new rule that allows critical actions without approval needs the separate confirmation.
	if err := update(granted, false); !errors.Is(err, mandate.ErrCriticalConfirmation) {
		t.Fatalf("new allow_critical without confirmation: err = %v, want ErrCriticalConfirmation", err)
	}
	if versions, _ := e.mandates.Versions(ctx, current.ID); len(versions) != 1 {
		t.Fatalf("refused update stored a version: %+v", versions)
	}
	if err := update(granted, true); err != nil {
		t.Fatalf("confirmed: %v", err)
	}

	// The same rule in the same form needs no new confirmation, however it is written.
	reordered := unlockWithoutApproval()
	reordered["actions"] = []any{"unlock", "read"}
	unchanged := voiceAssistant(t, a.ClientID, func(d map[string]any) {
		withRule(reordered)(d)
		d["limits"] = map[string]any{"max_actions_per_hour": 10}
	})
	if err := update(unchanged, false); err != nil {
		t.Fatalf("unchanged allow_critical rule: %v", err)
	}

	// Changed in any field, or under another id, it is a new grant.
	for name, edit := range map[string]func(map[string]any){
		"more actions":  func(r map[string]any) { r["actions"] = []any{"read", "unlock", "open"} },
		"wider scope":   func(r map[string]any) { r["resource"] = map[string]any{"any": true} },
		"new condition": func(r map[string]any) { r["conditions"] = map[string]any{"time_window": "08:00-18:00"} },
		"another id":    func(r map[string]any) { r["id"] = "r-unlock-2" },
	} {
		rule := unlockWithoutApproval()
		edit(rule)
		doc := voiceAssistant(t, a.ClientID, withRule(rule))
		if err := update(doc, false); !errors.Is(err, mandate.ErrCriticalConfirmation) {
			t.Errorf("%s without confirmation: err = %v, want ErrCriticalConfirmation", name, err)
		}
	}

	// Taking the grant away never needs a confirmation.
	if err := update(voiceAssistant(t, a.ClientID, nil), false); err != nil {
		t.Errorf("removing allow_critical: %v", err)
	}
}

func TestVersionsAreNumberedAndCanRepeatADigest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	original := voiceAssistant(t, a.ClientID, nil)
	first, err := e.mandates.Put(ctx, original, admin)
	if err != nil {
		t.Fatal(err)
	}
	limited := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	second, err := e.mandates.Update(ctx, first.ID, limited, mandate.Change{BaseDigest: first.Digest}, admin)
	if err != nil {
		t.Fatal(err)
	}
	// Restoring the first version stores a third one with the content, and so the digest, of the first.
	third, err := e.mandates.Update(ctx, first.ID, original, mandate.Change{BaseDigest: second.Digest}, admin)
	if err != nil || third.Digest != first.Digest {
		t.Fatalf("restore = %+v, %v", third, err)
	}

	versions, err := e.mandates.Versions(ctx, first.ID)
	if err != nil || len(versions) != 3 {
		t.Fatalf("Versions = %+v, %v", versions, err)
	}
	for i, want := range []string{first.Digest, second.Digest, first.Digest} {
		if versions[i].Number != i+1 || versions[i].Digest != want {
			t.Errorf("version %d = %+v, want number %d and digest %s", i, versions[i], i+1, want)
		}
	}

	doc, v, err := e.mandates.VersionDocument(ctx, first.ID, 2)
	if err != nil || v.Number != 2 || v.Digest != second.Digest || !strings.Contains(string(doc), `"max_actions_per_hour":10`) {
		t.Errorf("VersionDocument(2) = %s, %+v, %v", doc, v, err)
	}
	if _, v, err := e.mandates.VersionDocument(ctx, first.ID, 3); err != nil || v.Number != 3 || v.Digest != first.Digest {
		t.Errorf("VersionDocument(3) = %+v, %v", v, err)
	}
	for _, number := range []int{0, -1, 4} {
		if _, _, err := e.mandates.VersionDocument(ctx, first.ID, number); !errors.Is(err, mandate.ErrNotFound) {
			t.Errorf("VersionDocument(%d): err = %v, want ErrNotFound", number, err)
		}
	}
	if _, _, err := e.mandates.VersionDocument(ctx, "m-unknown", 1); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("unknown mandate: err = %v, want ErrNotFound", err)
	}
	if loaded, err := e.mandates.ForAgent(ctx, a.ClientID); err != nil || loaded.Info.Digest != first.Digest {
		t.Errorf("ForAgent = %+v, %v", loaded.Info, err)
	}
}
