// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	specaudit "github.com/home-mandate/spec/audit"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// withEntity is a decision entry about entityID: request values are limited in their
// characters only by I-JSON (SPEC-v0 section 9.1).
func withEntity(entityID string) audit.Entry {
	for _, e := range samples() {
		if e.Request != nil {
			request := *e.Request
			request.Resource.EntityID = entityID
			e.Request = &request
			return e
		}
	}
	panic("no sample with a request")
}

// A new entry must not contain a noncharacter (SPEC-v0 section 9.1), although a verifier
// keeps accepting one in an existing log (Section 14, correction noncharacters).
func TestAppendRefusesNoncharacters(t *testing.T) {
	l, db := newLog(t)
	for _, entityID := range []string{"light.a￾", "light.a﷐", "light.a\U0001FFFF"} {
		if _, err := l.Append(context.Background(), withEntity(entityID)); !errors.Is(err, audit.ErrInvalidEntry) {
			t.Errorf("Append(entity %q) = %v, want ErrInvalidEntry", entityID, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d entries stored, %v", n, err)
	}
}

// The characters next to the noncharacters stay allowed.
func TestAppendKeepsTheCharactersNextToNoncharacters(t *testing.T) {
	l, _ := newLog(t)
	for _, entityID := range []string{"light.a﷏", "light.aﷰ", "light.a�", "light.a\U0001FFFD"} {
		if _, err := l.Append(context.Background(), withEntity(entityID)); err != nil {
			t.Errorf("Append(entity %q) = %v", entityID, err)
		}
	}
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify = %+v", r)
	}
}

// A log that already holds a noncharacter stays valid and can be written on.
func TestExistingNoncharacterStaysValid(t *testing.T) {
	l, db := newLog(t)
	appendAll(t, l, []audit.Entry{withEntity("light.existing")})
	var entry string
	if err := db.QueryRow(`SELECT entry FROM audit_log WHERE seq = 1`).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(entry, "light.existing", "light.existing￾", 1)
	if altered == entry {
		t.Fatalf("entry %s does not name the entity", entry)
	}
	digest, err := specaudit.Digest([]byte(altered))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit_log SET entry = ?, digest = ? WHERE seq = 1`, altered, digest); err != nil {
		t.Fatal(err)
	}
	if r := verify(t, l); !r.Valid {
		t.Fatalf("Verify of the existing entry = %+v", r)
	}
	appendAll(t, l, []audit.Entry{withEntity("light.next")})
	if r := verify(t, l); !r.Valid {
		t.Errorf("Verify after the next entry = %+v", r)
	}
}
