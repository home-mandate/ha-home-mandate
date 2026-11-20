// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	specaudit "github.com/home-mandate/spec/audit"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

const (
	typeV0 = "https://home-mandate.org/audit/v0"
	typeV1 = "https://home-mandate.org/audit/v1"
)

// exported reads the type and event of every entry of the log, in order.
func exported(t *testing.T, l *audit.Log) (lines [][]byte, types, events []string) {
	t.Helper()
	var buf bytes.Buffer
	if err := l.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var e struct{ Type, Event string }
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		lines, types, events = append(lines, line), append(types, e.Type), append(events, e.Event)
	}
	return lines, types, events
}

func emergencyStop(id string) audit.Entry {
	return audit.Entry{Event: audit.EventEmergencyStopActivated, Actor: &audit.Actor{Kind: audit.ActorUser, ID: id}}
}

// SPEC-v0 section 14: new entries are written in audit/v1, the log is not rewritten, and a
// log whose checkpoints are signed gets a checkpoint of version 0 as the last entry before
// the first of version 1, so that tools that know only version 0 can verify the part before.
func TestTheFirstEntryOfVersion1FollowsACheckpointOfVersion0(t *testing.T) {
	ctx := context.Background()
	l, db := newLog(t)
	l.SetSigner(signer())
	audit.WriteVersion0(l)
	appendAll(t, l, []audit.Entry{emergencyStop("u1"), emergencyStop("u2")})
	// The same database, opened by the new version.
	l2 := audit.New(db, principal)
	l2.SetSigner(signer())
	appendAll(t, l2, []audit.Entry{emergencyStop("u3"), emergencyStop("u4")})

	lines, types, events := exported(t, l2)
	if want := []string{typeV0, typeV0, typeV0, typeV1, typeV1}; !equal(types, want) {
		t.Fatalf("types = %v, want %v", types, want)
	}
	if events[2] != audit.EventLogCheckpoint {
		t.Fatalf("events = %v, want a checkpoint before the first entry of version 1", events)
	}
	r, err := l2.Verify(ctx)
	if err != nil || !r.Valid || r.AnchoredSeq != 2 {
		t.Fatalf("Verify = %+v, %v; want valid and anchored up to 2", r, err)
	}
	// The part up to the checkpoint is a log of version 0 alone, valid on its own.
	if r, err := specaudit.Verify(lines[:3]); err != nil || !r.Valid || r.UnknownVersion.Found() {
		t.Errorf("version 0 part = %+v, %v", r, err)
	}
}

// Without a signing key there is nothing to anchor: the log moves on without a checkpoint.
func TestWithoutCheckpointsTheLogMovesOnDirectly(t *testing.T) {
	l, db := newLog(t)
	audit.WriteVersion0(l)
	appendAll(t, l, []audit.Entry{emergencyStop("u1")})
	l2 := audit.New(db, principal)
	appendAll(t, l2, []audit.Entry{emergencyStop("u2")})
	if _, types, _ := exported(t, l2); !equal(types, []string{typeV0, typeV1}) {
		t.Errorf("types = %v", types)
	}
}

// A checkpoint that comes first after the move is itself the one of version 0; nothing is
// written twice. A new log starts in version 1.
func TestACheckpointRightAfterTheMoveIsTheOneOfVersion0(t *testing.T) {
	ctx := context.Background()
	l, db := newLog(t)
	l.SetSigner(signer())
	audit.WriteVersion0(l)
	appendAll(t, l, []audit.Entry{emergencyStop("u1")})
	l2 := audit.New(db, principal)
	l2.SetSigner(signer())
	if _, err := l2.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	appendAll(t, l2, []audit.Entry{emergencyStop("u2")})
	if _, types, events := exported(t, l2); !equal(types, []string{typeV0, typeV0, typeV1}) || events[1] != audit.EventLogCheckpoint {
		t.Errorf("types = %v, events = %v", types, events)
	}

	fresh, _ := newLog(t)
	fresh.SetSigner(signer())
	appendAll(t, fresh, []audit.Entry{emergencyStop("u1")})
	if _, types, _ := exported(t, fresh); !equal(types, []string{typeV1}) {
		t.Errorf("new log: types = %v", types)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SPEC-v0 sections 9.2 and 13: every export is recorded with log.exported and the range of
// entries it contained, after them; the next export contains that entry as well.
func TestAnExportIsRecorded(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	appendAll(t, l, []audit.Entry{emergencyStop("u1"), emergencyStop("u2")})
	by := audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}
	var first bytes.Buffer
	if err := l.ExportRecorded(ctx, &first, by); err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(first.Bytes(), []byte("\n")); n != 2 {
		t.Errorf("first export has %d entries, want 2", n)
	}
	lines, _, events := exported(t, l)
	if len(events) != 3 || events[2] != audit.EventLogExported ||
		!bytes.Contains(lines[2], []byte(`"exported":{"from_seq":1,"to_seq":2}`)) || !bytes.Contains(lines[2], []byte(`"id":"local-admin"`)) {
		t.Fatalf("log after the export:\n%s", bytes.Join(lines, []byte("\n")))
	}
	if r, err := l.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("Verify = %+v, %v", r, err)
	}
	// An empty log: the export contains nothing, and its entry names no range.
	empty, _ := newLog(t)
	var none bytes.Buffer
	if err := empty.ExportRecorded(ctx, &none, by); err != nil || none.Len() != 0 {
		t.Fatalf("export of an empty log = %q, %v", none.String(), err)
	}
	if lines, _, events := exported(t, empty); len(events) != 1 || events[0] != audit.EventLogExported || bytes.Contains(lines[0], []byte(`"exported":{`)) {
		t.Errorf("empty log after the export: %s", lines)
	}
}

// If the checkpoint of the move cannot be signed, the entry of version 1 is not written
// either: both commit or neither.
func TestTheMoveNeedsItsCheckpoint(t *testing.T) {
	l, db := newLog(t)
	audit.WriteVersion0(l)
	appendAll(t, l, []audit.Entry{emergencyStop("u1")})
	l2 := audit.New(db, principal)
	broken := signer()
	broken.Key = nil
	l2.SetSigner(broken)
	if _, err := l2.Append(context.Background(), emergencyStop("u2")); err == nil {
		t.Fatal("an entry of version 1 was written without the checkpoint of the move")
	}
	if _, types, _ := exported(t, l2); !equal(types, []string{typeV0}) {
		t.Errorf("types after the failed move = %v", types)
	}
}
