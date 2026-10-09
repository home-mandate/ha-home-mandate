// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

const testLogID = "0198f1c2-7c3a-7000-8000-0000000000aa"

func signer() *audit.Signer {
	return &audit.Signer{LogID: testLogID, KeyID: "log-test", Key: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, ed25519.SeedSize))}
}

// A checkpoint signs the position and digest of the log, so that a log rewritten later
// no longer matches it (SPEC-v0 section 9.5).
func TestCheckpointAnchorsTheLog(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	if _, err := l.Checkpoint(ctx); !errors.Is(err, audit.ErrNoSigner) {
		t.Fatalf("Checkpoint without a signer = %v, want ErrNoSigner", err)
	}
	l.SetSigner(signer())
	last, _ := l.LastSeq(ctx)
	pos, err := l.Checkpoint(ctx)
	if err != nil || pos.Seq != last || pos.LogID != testLogID || pos.Digest == "" {
		t.Fatalf("Checkpoint = %+v, %v; want position %d", pos, err, last)
	}
	r, err := l.Verify(ctx)
	if err != nil || !r.Valid || r.AnchoredSeq != last || r.LogID != testLogID {
		t.Fatalf("Verify = %+v, %v; want anchored up to %d", r, err, last)
	}
	// Nothing new since the checkpoint: no second one.
	again, err := l.Checkpoint(ctx)
	if err != nil || again != (audit.Position{}) {
		t.Errorf("second Checkpoint = %+v, %v; want none", again, err)
	}
	if after, _ := l.LastSeq(ctx); after != last+1 {
		t.Errorf("last seq = %d, want %d", after, last+1)
	}
	// A log signed with another key does not verify against this one.
	other := signer()
	other.Key = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{6}, ed25519.SeedSize))
	l.SetSigner(other)
	if r, _ := l.Verify(ctx); r.Valid {
		t.Error("checkpoint verified with another key")
	}
}

func TestCheckpointOnAnEmptyLogDoesNothing(t *testing.T) {
	l, _ := clocked(t, start)
	l.SetSigner(signer())
	if pos, err := l.Checkpoint(context.Background()); err != nil || pos != (audit.Position{}) {
		t.Errorf("Checkpoint on an empty log = %+v, %v", pos, err)
	}
}

// A checkpoint follows every shortening of the log, so that the entry that accounts
// for the removed beginning is anchored (SPEC-v0 section 9.5).
func TestShorteningIsFollowedByACheckpoint(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	l.SetSigner(signer())
	removed, err := l.Truncate(ctx, start.Add(24*time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"})
	if err != nil || removed == 0 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	r, err := l.Verify(ctx)
	if err != nil || !r.Valid || r.FirstSeq <= 1 || !r.TruncationAnchored || r.Truncation != audit.TruncationAnchored {
		t.Errorf("Verify afterwards = %+v, %v; want the shortening anchored", r, err)
	}
}

// A log.truncated entry needs no key: whoever can write the database can delete the
// beginning and account for it. Only a verified checkpoint over that entry shows that
// Home-Mandate shortened the log itself; without one the log is not valid.
func TestUnanchoredShorteningIsNotValid(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	if r, err := l.Verify(ctx); err != nil || !r.Valid || r.FirstSeq != 1 || r.Truncation != audit.TruncationNone {
		t.Fatalf("Verify before = %+v, %v", r, err)
	}
	// No checkpoints at all: the beginning is gone and nothing proves who removed it.
	if _, err := l.Truncate(ctx, start.Add(4*time.Minute), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil {
		t.Fatal(err)
	}
	r, err := l.Verify(ctx)
	if err != nil || r.Valid || r.Truncation != audit.TruncationUnanchored || r.FirstSeq != 4 || r.BrokenAt != 4 {
		t.Errorf("Verify without checkpoints = %+v, %v; want unanchored from seq 4", r, err)
	}
}

// A log with checkpoints whose truncation no checkpoint covers was shortened behind
// Home-Mandate's back: Home-Mandate itself writes a checkpoint after every truncation.
func TestShorteningBehindTheCheckpointsIsTampering(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	l.SetSigner(signer())
	if _, err := l.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	l.SetSigner(nil) // a forged truncation, written without the key
	if _, err := l.Truncate(ctx, start.Add(4*time.Minute), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*audit.Signer{nil, signer()} {
		l.SetSigner(s)
		r, err := l.Verify(ctx)
		if err != nil || r.Valid || r.Truncation != audit.TruncationTampered || r.BrokenAt != 4 {
			t.Errorf("signer %v: Verify = %+v, %v; want tampered from seq 4", s != nil, r, err)
		}
	}
}

func TestCheckpointFailsWithoutTheDatabase(t *testing.T) {
	l := queryFixture(t)
	l.SetSigner(signer())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if pos, err := l.Checkpoint(ctx); err == nil || pos != (audit.Position{}) {
		t.Errorf("Checkpoint with a cancelled context = %+v, %v", pos, err)
	}
	// Turning the signer off again leaves a log with checkpoints verifiable.
	if _, err := l.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.SetSigner(nil)
	if r, err := l.Verify(context.Background()); err != nil || !r.Valid || r.AnchoredSeq != 0 {
		t.Errorf("Verify without a signer = %+v, %v; want valid, not anchored", r, err)
	}
}

func TestSignerMustBeComplete(t *testing.T) {
	l := queryFixture(t)
	bad := signer()
	bad.LogID = "not-a-uuid"
	l.SetSigner(bad)
	if _, err := l.Checkpoint(context.Background()); err == nil {
		t.Error("checkpoint with an invalid log ID written")
	}
	if r, err := l.Verify(context.Background()); err != nil || !r.Valid {
		t.Errorf("log after the refused checkpoint: %+v, %v", r, err)
	}
}
