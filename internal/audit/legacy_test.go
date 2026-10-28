// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	specaudit "github.com/home-mandate/spec/audit"
	"github.com/home-mandate/spec/jcs"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// legacyLog is a log as an earlier version wrote it: its decision entries name the single
// area of the resource (request.resource.area), which the audit schema no longer has.
// The chain is intact.
func legacyLog(t *testing.T) (*audit.Log, *sql.DB) {
	t.Helper()
	l, db := newLog(t)
	l.SetSigner(signer())
	appendAll(t, l, []audit.Entry{
		{Event: audit.EventAgentRegistered, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u1"}, Agent: &audit.Agent{ClientID: "hm-client:voice-1"}},
		decision("hm-client:voice-1", "Voice", "light.kitchen", "kitchen", "allow", "rule"),
		decision("hm-client:voice-1", "Voice", "lock.front_door", "hall", "deny", "rule"),
	})
	if _, err := l.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	appendAll(t, l, []audit.Entry{decision("hm-client:voice-1", "Voice", "light.hall", "hall", "allow", "rule")})
	rewrite(t, db, true, nil)
	return l, db
}

// rewrite turns every decision entry into the earlier format (single area) and chains
// the log again; resign: the checkpoints are signed again with the test key, as the
// installation did. forge changes an entry before it is chained.
func rewrite(t *testing.T, db *sql.DB, resign bool, forge func(seq int64, e map[string]any)) {
	t.Helper()
	rows, err := db.Query(`SELECT seq, entry FROM audit_log ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	var entries []map[string]any
	for rows.Next() {
		var seq int64
		var text string
		if err := rows.Scan(&seq, &text); err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var e map[string]any
		if err := dec.Decode(&e); err != nil {
			t.Fatal(err)
		}
		seqs, entries = append(seqs, seq), append(entries, e)
	}
	_ = rows.Close()
	var prev any
	for i, e := range entries {
		if request, ok := e["request"].(map[string]any); ok {
			resource := request["resource"].(map[string]any)
			if areas, ok := resource["areas"].([]any); ok {
				resource["area"] = areas[0]
				delete(resource, "areas")
			}
		}
		if i > 0 {
			e["prev"] = prev
		}
		if forge != nil {
			forge(seqs[i], e)
		}
		if cp, ok := e["checkpoint"].(map[string]any); ok && resign {
			k := signer()
			sig, err := specaudit.SignCheckpoint(k.LogID, seqs[i]-1, prev.(string), k.KeyID, k.Key)
			if err != nil {
				t.Fatal(err)
			}
			cp["signature"] = sig
		}
		canonical, err := jcs.Canonicalize(e)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := specaudit.Digest(canonical)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE audit_log SET entry = ?, digest = ? WHERE seq = ?`, string(canonical), digest, seqs[i]); err != nil {
			t.Fatal(err)
		}
		prev = digest
	}
}

type closer struct{ io.Writer }

func (closer) Close() error { return nil }

func TestALegacyLogIsArchivedAndTruncated(t *testing.T) {
	l, _ := legacyLog(t)
	ctx := context.Background()
	if r, _ := l.Verify(ctx); r.Valid {
		t.Fatal("the legacy log verifies; the test does not test anything")
	}
	var before bytes.Buffer
	_ = l.Export(ctx, &before)
	var archive bytes.Buffer
	n, err := l.ArchiveLegacy(ctx, func() (io.WriteCloser, error) { return closer{&archive}, nil }, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"})
	if err != nil || n != 5 {
		t.Fatalf("ArchiveLegacy = %d, %v", n, err)
	}
	if archive.String() != before.String() {
		t.Errorf("archive differs from the log:\n%s\nwant\n%s", archive.String(), before.String())
	}
	r, err := l.Verify(ctx)
	if err != nil || !r.Valid || r.Truncation != audit.TruncationAnchored {
		t.Fatalf("after archiving: %+v, %v", r, err)
	}
	var after bytes.Buffer
	_ = l.Export(ctx, &after)
	if strings.Contains(after.String(), `"area":`) || !strings.Contains(after.String(), `"event":"log.truncated"`) {
		t.Errorf("log after archiving:\n%s", after.String())
	}
	// Once archived, nothing is left to do.
	if n, err := l.ArchiveLegacy(ctx, func() (io.WriteCloser, error) { t.Fatal("archived twice"); return nil, nil }, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}); n != 0 || err != nil {
		t.Errorf("second ArchiveLegacy = %d, %v", n, err)
	}
}

func TestATamperedLegacyLogIsNotArchived(t *testing.T) {
	l, db := legacyLog(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE audit_log SET entry = replace(entry, '"kitchen"', '"garden"') WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	var before bytes.Buffer
	_ = l.Export(ctx, &before)
	n, err := l.ArchiveLegacy(ctx, func() (io.WriteCloser, error) { t.Fatal("a tampered log was archived"); return nil, nil }, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"})
	if n != 0 || !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("ArchiveLegacy = %d, %v; want ErrChainBroken", n, err)
	}
	var after bytes.Buffer
	_ = l.Export(ctx, &after)
	if after.String() != before.String() {
		t.Error("a tampered log was changed")
	}
}

func TestAValidLogIsNotArchived(t *testing.T) {
	l := queryFixture(t)
	n, err := l.ArchiveLegacy(context.Background(), func() (io.WriteCloser, error) { t.Fatal("a valid log was archived"); return nil, nil }, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"})
	if n != 0 || err != nil {
		t.Errorf("ArchiveLegacy = %d, %v", n, err)
	}
}

// An invalid log that has no entry of the earlier format is not this version's to clean
// up: start-up reports it as broken.
func TestAnInvalidLogWithoutLegacyEntriesIsNotArchived(t *testing.T) {
	l, db := newLog(t)
	l.SetSigner(signer())
	ctx := context.Background()
	appendAll(t, l, []audit.Entry{decision("hm-client:voice-1", "Voice", "light.kitchen", "kitchen", "allow", "rule")})
	if _, err := db.Exec(`UPDATE audit_log SET entry = replace(entry, '"allow"', '"maybe"')`); err != nil {
		t.Fatal(err)
	}
	n, err := l.ArchiveLegacy(ctx, func() (io.WriteCloser, error) { t.Fatal("archived"); return nil, nil }, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"})
	if n != 0 || err != nil {
		t.Errorf("ArchiveLegacy = %d, %v", n, err)
	}
}

type brokenArchive struct{ writeErr, closeErr error }

func (f brokenArchive) Write(p []byte) (int, error) { return len(p), f.writeErr }
func (f brokenArchive) Close() error                { return f.closeErr }

// Archiving stops before it changes the log when the archive cannot be written, and needs
// the checkpoint key that anchors the truncation.
func TestALegacyLogIsKeptWhenItCannotBeArchived(t *testing.T) {
	upgrade := audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}
	ctx := context.Background()
	for name, archive := range map[string]func() (io.WriteCloser, error){
		"no file": func() (io.WriteCloser, error) { return nil, errors.New("disk full") },
		"write":   func() (io.WriteCloser, error) { return brokenArchive{writeErr: errors.New("disk full")}, nil },
		"close":   func() (io.WriteCloser, error) { return brokenArchive{closeErr: errors.New("disk full")}, nil },
	} {
		l, _ := legacyLog(t)
		var before bytes.Buffer
		_ = l.Export(ctx, &before)
		if n, err := l.ArchiveLegacy(ctx, archive, upgrade); n != 0 || err == nil {
			t.Errorf("%s: ArchiveLegacy = %d, %v; want an error", name, n, err)
		}
		var after bytes.Buffer
		_ = l.Export(ctx, &after)
		if after.String() != before.String() {
			t.Errorf("%s: the log changed although nothing was archived", name)
		}
	}
	l, _ := legacyLog(t)
	l.SetSigner(nil)
	if _, err := l.ArchiveLegacy(ctx, func() (io.WriteCloser, error) { t.Fatal("archived without a key"); return nil, nil }, upgrade); !errors.Is(err, audit.ErrNoSigner) {
		t.Errorf("without a key: %v, want ErrNoSigner", err)
	}
}

func TestAnEmptyLogIsNotArchived(t *testing.T) {
	l, _ := newLog(t)
	if n, err := l.ArchiveLegacy(context.Background(), nil, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}); n != 0 || err != nil {
		t.Errorf("ArchiveLegacy = %d, %v", n, err)
	}
}

// enforced: false is written only for a deny that was not enforced, and read back.
func TestEvaluationNamesOnlyANotEnforcedDeny(t *testing.T) {
	for _, tc := range []struct {
		e    audit.Evaluation
		want string
	}{
		{audit.Evaluation{Decision: "deny", Reason: "no_match", NotEnforced: true}, `{"decision":"deny","reason":"no_match","rule_id":null,"enforced":false}`},
		{audit.Evaluation{Decision: "deny", Reason: "rule", RuleID: str("r-1")}, `{"decision":"deny","reason":"rule","rule_id":"r-1"}`},
		{audit.Evaluation{Decision: "deny", Reason: "rule", RuleID: str("r-1"), ApprovalTimeout: "PT1M", NotEnforced: true, Confirm: true},
			`{"decision":"deny","reason":"rule","rule_id":"r-1","approval_timeout":"PT1M","enforced":false,"confirm":true}`},
		// confirm only with enforced: false (SPEC-v0 section 9.1)
		{audit.Evaluation{Decision: "deny", Reason: "rule", RuleID: str("r-1"), Confirm: true}, `{"decision":"deny","reason":"rule","rule_id":"r-1"}`},
	} {
		data, err := json.Marshal(tc.e)
		if err != nil || string(data) != tc.want {
			t.Errorf("Marshal = %s, %v; want %s", data, err, tc.want)
		}
		var back audit.Evaluation
		if err := json.Unmarshal(data, &back); err != nil || back.NotEnforced != tc.e.NotEnforced || back.Confirm != (tc.e.Confirm && tc.e.NotEnforced) || back.Reason != tc.e.Reason {
			t.Errorf("Unmarshal(%s) = %+v, %v", data, back, err)
		}
	}
	var e audit.Evaluation
	if err := json.Unmarshal([]byte(`{"decision":"deny","reason":"rule","rule_id":null,"enforced":true}`), &e); err != nil || e.NotEnforced {
		t.Errorf("enforced: true = %+v, %v", e, err)
	}
	if err := json.Unmarshal([]byte(`{"decision":"deny","reason":"rule","rule_id":null,"confirm":true}`), &e); err != nil || e.Confirm {
		t.Errorf("confirm without enforced: false = %+v, %v", e, err)
	}
	if err := json.Unmarshal([]byte(`[]`), &e); err == nil {
		t.Error("an array is no evaluation")
	}
}

func TestArchivingReportsAnUnreadableLog(t *testing.T) {
	upgrade := audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}
	ctx := context.Background()
	never := func() (io.WriteCloser, error) { t.Fatal("archived"); return nil, nil }
	l, db := legacyLog(t)
	if _, err := db.Exec(`UPDATE audit_log SET entry = '{"seq":"one"}' WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	if n, err := l.ArchiveLegacy(ctx, never, upgrade); n != 0 || err == nil {
		t.Errorf("an entry that is no entry: %d, %v", n, err)
	}
	l, db = legacyLog(t)
	db.Close()
	if n, err := l.ArchiveLegacy(ctx, never, upgrade); n != 0 || err == nil {
		t.Errorf("a closed database: %d, %v", n, err)
	}
}

// A log that cannot be truncated after the archive was written stays as it was; the next
// start tries again with a new archive.
func TestArchivingStopsWhenTheLogCannotBeTruncated(t *testing.T) {
	upgrade := audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}
	ctx := context.Background()
	l, db := legacyLog(t)
	if _, err := db.Exec(`CREATE TRIGGER keep BEFORE DELETE ON audit_log BEGIN SELECT RAISE(ABORT, 'kept'); END`); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if n, err := l.ArchiveLegacy(ctx, func() (io.WriteCloser, error) { return closer{&archive}, nil }, upgrade); n != 0 || err == nil {
		t.Errorf("ArchiveLegacy = %d, %v; want an error", n, err)
	}
	l, db = legacyLog(t)
	closing := func() (io.WriteCloser, error) { db.Close(); return closer{io.Discard}, nil }
	if n, err := l.ArchiveLegacy(ctx, closing, upgrade); n != 0 || err == nil {
		t.Errorf("without the database after archiving: %d, %v", n, err)
	}
}

// Someone who can write the database but has no key changes an entry, chains the log
// again and adds the earlier format: the checkpoint no longer verifies, nothing is archived.
func TestAForgedLegacyLogIsNotArchived(t *testing.T) {
	l, db := legacyLog(t)
	rewrite(t, db, false, func(seq int64, e map[string]any) {
		if seq == 2 {
			e["request"].(map[string]any)["resource"].(map[string]any)["entity_id"] = "light.garden"
		}
	})
	never := func() (io.WriteCloser, error) { t.Fatal("a forged log was archived"); return nil, nil }
	if n, err := l.ArchiveLegacy(context.Background(), never, audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}); n != 0 || !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("ArchiveLegacy = %d, %v; want ErrChainBroken", n, err)
	}
}

// The earlier format belongs to decision entries of an earlier version: one elsewhere,
// or after an entry of the current format, is no upgrade.
func TestTheEarlierFormatOnlyBeforeTheCurrentOne(t *testing.T) {
	never := func() (io.WriteCloser, error) { t.Fatal("archived"); return nil, nil }
	upgrade := audit.Actor{Kind: audit.ActorSystem, ID: "upgrade"}
	for name, forge := range map[string]func(int64, map[string]any){
		"current before earlier": func(seq int64, e map[string]any) {
			if seq == 2 {
				resource := e["request"].(map[string]any)["resource"].(map[string]any)
				resource["areas"] = []any{resource["area"]}
				delete(resource, "area")
			}
		},
		"not a decision": func(seq int64, e map[string]any) {
			if seq == 1 {
				e["request"] = map[string]any{"resource": map[string]any{"area": "hall"}}
			}
		},
	} {
		l, db := legacyLog(t)
		rewrite(t, db, true, forge)
		if n, err := l.ArchiveLegacy(context.Background(), never, upgrade); n != 0 || !errors.Is(err, audit.ErrChainBroken) {
			t.Errorf("%s: ArchiveLegacy = %d, %v; want ErrChainBroken", name, n, err)
		}
	}
}
