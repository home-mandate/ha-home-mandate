// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, "sha256:"+strings.Repeat("0", 64), other, true); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("confirmed another digest: %v", err)
	}
	confirmed, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other, true)
	if err != nil || confirmed.Digest != p.Digest {
		t.Fatalf("ConfirmAuditMode = %+v, %v", confirmed, err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); ok {
		t.Error("the proposal is still waiting after the confirmation")
	}
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other, true); !errors.Is(err, mandate.ErrNotFound) {
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
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other, true); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("confirming an ended proposal: %v", err)
	}

	propose(next.Digest)
	if all, err := e.mandates.PendingAll(ctx); err != nil || len(all) != 1 {
		t.Errorf("PendingAll = %v, %v", all, err)
	}
	if err := e.mandates.DiscardAuditMode(ctx, first.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.DiscardAuditMode(ctx, first.ID, admin); !errors.Is(err, mandate.ErrNotFound) {
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

// proposalEntries returns the mandate.proposed and mandate.discarded entries of the log,
// in order, each as "event cause actor digest previous_digest".
func proposalEntries(t *testing.T, e env) []string {
	t.Helper()
	var log strings.Builder
	if err := e.log.Export(context.Background(), &log); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(log.String()), "\n") {
		var entry struct {
			Event string `json:"event"`
			Actor struct {
				ID string `json:"id"`
			} `json:"actor"`
			Mandate struct {
				Digest         string `json:"digest"`
				PreviousDigest string `json:"previous_digest"`
				Cause          string `json:"cause"`
			} `json:"mandate"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Event == audit.EventMandateProposed || entry.Event == audit.EventMandateDiscarded {
			out = append(out, strings.Join([]string{entry.Event, entry.Mandate.Cause, entry.Actor.ID, entry.Mandate.Digest, entry.Mandate.PreviousDigest}, " "))
		}
	}
	return out
}

// Every proposal is recorded with mandate.proposed, and every end other than its
// confirmation with mandate.discarded and its cause (SPEC-v0 sections 9.1, 9.2 and 11.6
// item 4, issue #171).
func TestProposalsAreRecorded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	propose := func(base string, by audit.Actor, edit func(map[string]any)) mandate.Pending {
		t.Helper()
		doc := voiceAssistant(t, a.ClientID, func(d map[string]any) {
			audited(d)
			if edit != nil {
				edit(d)
			}
		})
		if _, err := e.mandates.Update(ctx, first.ID, doc, mandate.Change{BaseDigest: base, MayPropose: true}, by); !errors.Is(err, mandate.ErrAuditModeProposed) {
			t.Fatalf("Update = %v", err)
		}
		p, _, _ := e.mandates.Pending(ctx, first.ID)
		return p
	}

	// A proposal, then another in its place: the first is superseded.
	p1 := propose(first.Digest, admin, nil)
	p2 := propose(first.Digest, other, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 7} })
	// Another version takes effect: the waiting one is superseded.
	next, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, func(d map[string]any) {
		d["limits"] = map[string]any{"max_actions_per_hour": 5}
	}), mandate.Change{BaseDigest: first.Digest}, admin)
	if err != nil {
		t.Fatal(err)
	}
	// A human discards one: rejected, by that human.
	p3 := propose(next.Digest, admin, nil)
	if err := e.mandates.DiscardAuditMode(ctx, first.ID, other); err != nil {
		t.Fatal(err)
	}
	// The mandate is revoked: revoked.
	p4 := propose(next.Digest, admin, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 3} })
	if err := e.mandates.Revoke(ctx, first.ID, other); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"mandate.proposed  user-1 " + p1.Digest + " " + first.Digest,
		"mandate.discarded superseded user-2 " + p1.Digest + " " + first.Digest,
		"mandate.proposed  user-2 " + p2.Digest + " " + first.Digest,
		"mandate.discarded superseded user-1 " + p2.Digest + " " + first.Digest,
		"mandate.proposed  user-1 " + p3.Digest + " " + next.Digest,
		"mandate.discarded rejected user-2 " + p3.Digest + " " + next.Digest,
		"mandate.proposed  user-1 " + p4.Digest + " " + next.Digest,
		"mandate.discarded revoked user-2 " + p4.Digest + " " + next.Digest,
	}
	if got := proposalEntries(t, e); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if v, err := e.log.Verify(ctx); err != nil || !v.Valid {
		t.Errorf("the audit log does not verify: %+v, %v", v, err)
	}
}

// A proposal expires after 24 hours, recorded as discarded with the cause expired, so that
// an old notification cannot adopt it (SPEC-v0 section 11.6 item 4, issue #171).
func TestAProposalExpires(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, audited), mandate.Change{BaseDigest: first.Digest, MayPropose: true}, admin); !errors.Is(err, mandate.ErrAuditModeProposed) {
		t.Fatalf("Update = %v", err)
	}
	p, _, _ := e.mandates.Pending(ctx, first.ID)
	if !p.ExpiresAt.Equal(p.ProposedAt.Add(mandate.ProposalLifetime)) || mandate.ProposalLifetime != 24*time.Hour {
		t.Errorf("expires at %v, proposed at %v", p.ExpiresAt, p.ProposedAt)
	}
	if n, err := e.mandates.ExpireProposals(ctx, p.ExpiresAt.Add(-time.Second)); err != nil || n != 0 {
		t.Errorf("before the end: %d, %v", n, err)
	}
	if n, err := e.mandates.ExpireProposals(ctx, p.ExpiresAt); err != nil || n != 1 {
		t.Errorf("at the end: %d, %v", n, err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); ok {
		t.Error("the expired proposal still waits")
	}
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other, false); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("confirming an expired proposal: %v", err)
	}
	want := []string{
		"mandate.proposed  user-1 " + p.Digest + " " + first.Digest,
		"mandate.discarded expired proposals " + p.Digest + " " + first.Digest,
	}
	if got := proposalEntries(t, e); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Where another administrator exists, the one who proposed audit mode does not confirm it
// (SPEC-v0 section 11.6 item 4, issue #171); alone, they may.
func TestTheProposerConfirmsOnlyWhenAlone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, audited), mandate.Change{BaseDigest: first.Digest, MayPropose: true}, admin); !errors.Is(err, mandate.ErrAuditModeProposed) {
		t.Fatalf("Update = %v", err)
	}
	p, _, _ := e.mandates.Pending(ctx, first.ID)
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, admin, false); !errors.Is(err, mandate.ErrOwnProposal) {
		t.Errorf("the proposer confirmed with another administrator around: %v", err)
	}
	if _, ok, _ := e.mandates.Pending(ctx, first.ID); !ok {
		t.Fatal("the refused confirmation ended the proposal")
	}
	if got, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, admin, true); err != nil || got.Digest != p.Digest {
		t.Errorf("the only administrator confirms: %+v, %v", got, err)
	}
	// A confirmed proposal is not discarded: the log holds its proposal and mandate.updated.
	if got := proposalEntries(t, e); len(got) != 1 || !strings.HasPrefix(got[0], "mandate.proposed ") {
		t.Errorf("entries after the confirmation: %v", got)
	}
}

// A proposal older than its lifetime cannot be confirmed, even before anything ended it
// (SPEC-v0 section 11.6 item 4): the confirmation checks the age in its own transaction.
func TestAnOldProposalCannotBeConfirmed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice assistant")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.mandates.Update(ctx, first.ID, voiceAssistant(t, a.ClientID, audited), mandate.Change{BaseDigest: first.Digest, MayPropose: true}, admin); !errors.Is(err, mandate.ErrAuditModeProposed) {
		t.Fatalf("Update = %v", err)
	}
	p, _, _ := e.mandates.Pending(ctx, first.ID)
	old := time.Now().Add(-mandate.ProposalLifetime - time.Second).UTC().Format(time.RFC3339Nano)
	if _, err := e.db.ExecContext(ctx, `UPDATE mandate_pending SET proposed_at = ?`, old); err != nil {
		t.Fatal(err)
	}
	// Reading does not offer it any more, and writes nothing.
	if _, ok, err := e.mandates.Pending(ctx, first.ID); ok || err != nil {
		t.Errorf("Pending = %t, %v", ok, err)
	}
	if got := proposalEntries(t, e); len(got) != 1 {
		t.Errorf("reading wrote entries: %v", got)
	}
	if _, err := e.mandates.ConfirmAuditMode(ctx, first.ID, p.Digest, other, true); !errors.Is(err, mandate.ErrNotFound) {
		t.Errorf("confirming an old proposal: %v", err)
	}
	if got := proposalEntries(t, e); len(got) != 2 || !strings.HasPrefix(got[1], "mandate.discarded expired ") {
		t.Errorf("entries: %v", got)
	}
	if n, err := e.mandates.ExpireProposals(ctx, time.Now()); n != 0 || err != nil {
		t.Errorf("nothing is left to expire: %d, %v", n, err)
	}
}
