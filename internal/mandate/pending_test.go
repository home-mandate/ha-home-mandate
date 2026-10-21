// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

var other = audit.Actor{Kind: audit.ActorUser, ID: "user-2"}

func audited(d map[string]any) { d["enforce"] = false }

// A new mandate in audit mode cannot be confirmed before it exists: it is refused.
func TestANewMandateInAuditModeIsRefused(t *testing.T) {
	e := newEnv(t)
	a := e.agent(t, "Voice assistant")
	if _, err := e.mandates.Put(context.Background(), voiceAssistant(t, a.ClientID, audited), admin); !errors.Is(err, mandate.ErrAuditModeNeedsConfirmation) {
		t.Errorf("Put = %v, want ErrAuditModeNeedsConfirmation", err)
	}
}

// Switching audit mode on waits for a human's confirmation of exactly that version; the
// current version stays in force (SPEC-v0 section 11.6 item 4).
func TestAuditModeWaitsForTheConfirmationOfItsDigest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, audited), mandate.Change{BaseDigest: first.Digest, Name: "Trial", MayPropose: true}, admin)
	if !errors.Is(err, mandate.ErrAuditModeProposed) {
		t.Fatalf("Update = %v, want ErrAuditModeProposed", err)
	}
	now, _ := e.mandates.Get(ctx, first.ID)
	if now.Digest != first.Digest || now.Name != "Trial" {
		t.Errorf("after the proposal: %+v; the current version must stay, the name may change", now)
	}
	p, ok, err := e.mandates.Pending(ctx, first.ID)
	if err != nil || !ok || p.BaseDigest != first.Digest || p.ProposedBy != admin.ID || !strings.Contains(string(p.Document), `"enforce":false`) {
		t.Fatalf("Pending = %+v, %t, %v", p, ok, err)
	}
	if m, err := evaluator.Parse(p.Document); err != nil || m.Digest() != p.Digest || m.Enforce() {
		t.Fatalf("the waiting version: %v, %v", m, err)
	}
	// The confirmation counts only for the digest that was shown.
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, "sha256:"+strings.Repeat("0", 64), other); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("confirmed another digest: %v", err)
	}
	confirmed, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other)
	if err != nil || confirmed.Digest != p.Digest {
		t.Fatalf("ConfirmAuditMode = %+v, %v", confirmed, err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); ok {
		t.Error("the proposal is still waiting after the confirmation")
	}
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("confirmed twice: %v", err)
	}
	var log strings.Builder
	_ = e.log.Export(ctx, &log)
	if !strings.Contains(log.String(), `"actor":{"id":"user-2","kind":"user"},"event":"mandate.updated"`) {
		t.Errorf("the confirming human is the actor of mandate.updated:\n%s", log.String())
	}

	// In audit mode a change needs no confirmation; switching it off neither.
	changed, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, func(d map[string]any) {
		audited(d)
		d["limits"] = map[string]any{"max_actions_per_hour": 10}
	}), mandate.Change{BaseDigest: confirmed.Digest}, admin)
	if err != nil || changed.MaxActionsPerHour != 10 {
		t.Fatalf("change in audit mode = %+v, %v", changed, err)
	}
	off, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, nil), mandate.Change{BaseDigest: changed.Digest}, admin)
	if err != nil || off.Digest == changed.Digest {
		t.Fatalf("switching off = %+v, %v", off, err)
	}
}

// A proposal ends when another version takes effect, when the mandate is revoked, or when
// a human discards it.
func TestAProposalEnds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	propose := func(base string) mandate.Pending {
		t.Helper()
		if _, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, audited), mandate.Change{BaseDigest: base, MayPropose: true}, admin); !errors.Is(err, mandate.ErrAuditModeProposed) {
			t.Fatalf("Update = %v", err)
		}
		p, _, _ := e.mandates.Pending(ctx, first.ID)
		return p
	}
	p := propose(first.Digest)
	next, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, func(d map[string]any) {
		d["limits"] = map[string]any{"max_actions_per_hour": 5}
	}), mandate.Change{BaseDigest: first.Digest}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); ok {
		t.Error("the proposal waits on after another version took effect")
	}
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("confirming an ended proposal: %v", err)
	}

	propose(next.Digest)
	if all, err := e.mandates.PendingAll(ctx); err != nil || len(all) != 1 {
		t.Errorf("PendingAll = %v, %v", all, err)
	}
	if err := e.mandates.DiscardAuditMode(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.DiscardAuditMode(ctx, first.ID); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("discarding twice: %v", err)
	}

	propose(next.Digest)
	if err := e.mandates.Revoke(ctx, first.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); ok {
		t.Error("the proposal waits on after the revocation")
	}
}

// Only the editor proposes: any other path that would switch audit mode on (a template,
// a rename taken over) is refused and leaves nothing waiting.
func TestOnlyTheEditorProposesAuditMode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, audited), mandate.Change{BaseDigest: first.Digest}, admin); !errors.Is(err, mandate.ErrAuditModeNeedsConfirmation) {
		t.Errorf("Update = %v, want ErrAuditModeNeedsConfirmation", err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); ok {
		t.Error("a proposal was left behind")
	}
}
