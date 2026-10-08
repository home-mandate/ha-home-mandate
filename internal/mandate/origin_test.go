// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func fromTemplate(name, digest string) mandate.Origin {
	return mandate.Origin{Kind: mandate.OriginTemplate, Template: name, TemplateDigest: digest}
}

// putFrom stores document as a new mandate whose rules came from a template.
func (e env) putFrom(t *testing.T, document []byte, origin mandate.Origin) mandate.Info {
	t.Helper()
	tx, err := e.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	info, err := e.mandates.PutOriginTx(context.Background(), tx, document, origin, admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, err := e.mandates.Get(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestVersionsKeepWhereTheirRulesCameFrom(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice")
	first := e.putFrom(t, voiceAssistant(t, a.ClientID, nil), fromTemplate("voice", digestA))
	limited := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	second, err := e.mandates.Update(ctx, first.ID, limited, mandate.Change{BaseDigest: first.Digest}, admin)
	if err != nil {
		t.Fatal(err)
	}
	strict := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 5} })
	if _, err := e.mandates.Update(ctx, first.ID, strict, mandate.Change{BaseDigest: second.Digest, Origin: fromTemplate("strict", digestB)}, admin); err != nil {
		t.Fatal(err)
	}
	versions, err := e.mandates.Versions(ctx, first.ID)
	if err != nil || len(versions) != 3 {
		t.Fatalf("Versions = %+v, %v", versions, err)
	}
	for i, want := range []mandate.Origin{fromTemplate("voice", digestA), {Kind: mandate.OriginEdit}, fromTemplate("strict", digestB)} {
		if versions[i].Origin != want {
			t.Errorf("version %d origin = %+v, want %+v", i+1, versions[i].Origin, want)
		}
	}
	// Put without an origin is an edit (the command line).
	b := e.agent(t, "Other")
	other, err := e.mandates.Put(ctx, voiceAssistant(t, b.ClientID, func(d map[string]any) { d["id"] = "m-other" }), admin)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := e.mandates.Versions(ctx, other.ID); len(v) != 1 || v[0].Origin != (mandate.Origin{Kind: mandate.OriginEdit}) {
		t.Errorf("origin of Put = %+v", v)
	}
}

func TestAnInvalidOriginIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice")
	first, err := e.mandates.Put(ctx, voiceAssistant(t, a.ClientID, nil), admin)
	if err != nil {
		t.Fatal(err)
	}
	limited := voiceAssistant(t, a.ClientID, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	for name, origin := range map[string]mandate.Origin{
		"template without name":   {Kind: mandate.OriginTemplate, TemplateDigest: digestA},
		"template without digest": {Kind: mandate.OriginTemplate, Template: "voice"},
		"edit with a template":    {Kind: mandate.OriginEdit, Template: "voice", TemplateDigest: digestA},
		"unknown":                 {Kind: mandate.OriginUnknown},
		"other kind":              {Kind: "magic"},
	} {
		if _, err := e.mandates.Update(ctx, first.ID, limited, mandate.Change{BaseDigest: first.Digest, Origin: origin}, admin); !errors.Is(err, mandate.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if v, _ := e.mandates.Versions(ctx, first.ID); len(v) != 1 {
		t.Errorf("refused origins stored versions: %d", len(v))
	}
}

// Content whose rules, approval settings, limits and validity equal the current version
// stores no version, also when only its metadata or the order of lists differ.
func TestUpdateWithTheSameEditableContentStoresNoVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t, "Voice")
	first := e.putFrom(t, voiceAssistant(t, a.ClientID, func(d map[string]any) {
		d["approval"] = map[string]any{"timeout": "PT2M", "approvers": []any{"user-1", "user-2"}}
	}), fromTemplate("voice", digestA))
	entries := func() int {
		var n int
		if err := e.db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := entries()
	same := voiceAssistant(t, a.ClientID, func(d map[string]any) {
		d["created_at"] = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
		d["created_by"] = "user-2"
		d["approval"] = map[string]any{"approvers": []any{"user-2", "user-1"}, "timeout": "PT2M"}
		rules := d["rules"].([]any)
		lights := rules[1].(map[string]any)
		lights["actions"] = []any{"set", "turn_off", "turn_on"}
	})
	got, err := e.mandates.Update(ctx, first.ID, same, mandate.Change{BaseDigest: first.Digest, Origin: fromTemplate("voice", digestB), Name: "Voice"}, admin)
	if err != nil || got.Digest != first.Digest || got.Name != "Voice" {
		t.Fatalf("Update with the same content = %+v, %v", got, err)
	}
	if v, _ := e.mandates.Versions(ctx, first.ID); len(v) != 1 || v[0].Origin != fromTemplate("voice", digestA) {
		t.Errorf("versions after an unchanged update = %+v", v)
	}
	if n := entries(); n != before {
		t.Errorf("an unchanged update wrote %d audit entries", n-before)
	}
	// The conflict rules apply all the same.
	if _, err := e.mandates.Update(ctx, first.ID, same, mandate.Change{BaseDigest: digestB}, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("unchanged content on an old base: err = %v, want ErrConflict", err)
	}
}

func TestSameEditable(t *testing.T) {
	base := `{"rules":[{"id":"r","actions":["a","b"]}],"approval":{"approvers":["u1","u2"],"timeout":"PT2M"},"limits":{"max_actions_per_hour":60},"valid_from":"2026-10-01T00:00:00Z","created_at":"x","created_by":"u1"}`
	for _, tc := range []struct {
		name  string
		other string
		same  bool
	}{
		{"identical", base, true},
		{"metadata and order", `{"created_by":"u9","limits":{"max_actions_per_hour":60},"approval":{"timeout":"PT2M","approvers":["u2","u1"]},"rules":[{"actions":["b","a"],"id":"r"}],"valid_from":"2026-10-01T00:00:00Z","created_at":"y"}`, true},
		{"other limit", `{"rules":[{"id":"r","actions":["a","b"]}],"approval":{"approvers":["u1","u2"],"timeout":"PT2M"},"limits":{"max_actions_per_hour":61},"valid_from":"2026-10-01T00:00:00Z"}`, false},
		{"other approver", `{"rules":[{"id":"r","actions":["a","b"]}],"approval":{"approvers":["u1"],"timeout":"PT2M"},"limits":{"max_actions_per_hour":60},"valid_from":"2026-10-01T00:00:00Z"}`, false},
		{"rules in another order", `{"rules":[{"id":"s"},{"id":"r","actions":["a","b"]}],"approval":{"approvers":["u1","u2"],"timeout":"PT2M"},"limits":{"max_actions_per_hour":60},"valid_from":"2026-10-01T00:00:00Z"}`, false},
		{"with expiry", `{"rules":[{"id":"r","actions":["a","b"]}],"approval":{"approvers":["u1","u2"],"timeout":"PT2M"},"limits":{"max_actions_per_hour":60},"valid_from":"2026-10-01T00:00:00Z","expires":"2027-01-01T00:00:00Z"}`, false},
		{"other validity", `{"rules":[{"id":"r","actions":["a","b"]}],"approval":{"approvers":["u1","u2"],"timeout":"PT2M"},"limits":{"max_actions_per_hour":60},"valid_from":"2026-10-02T00:00:00Z"}`, false},
		{"not JSON", `{`, false},
	} {
		if got := mandate.SameEditable([]byte(base), []byte(tc.other)); got != tc.same {
			t.Errorf("%s: SameEditable = %v, want %v", tc.name, got, tc.same)
		}
	}
	if mandate.SameEditable([]byte(`[`), []byte(base)) {
		t.Error("invalid current document counts as the same")
	}
}

func TestTemplateUses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	doc := func(clientID, id string, limit int) []byte {
		return voiceAssistant(t, clientID, func(d map[string]any) {
			d["id"] = id
			d["limits"] = map[string]any{"max_actions_per_hour": limit}
		})
	}
	a, b, c, d := e.agent(t, "A"), e.agent(t, "B"), e.agent(t, "C"), e.agent(t, "D")
	// a: from voice; b: from voice, edited since; c: edited only; d: from voice, then from strict.
	ma := e.putFrom(t, doc(a.ClientID, "m-aaa", 60), fromTemplate("voice", digestA))
	mb := e.putFrom(t, doc(b.ClientID, "m-bbb", 60), fromTemplate("voice", digestA))
	if _, err := e.mandates.Update(ctx, mb.ID, doc(b.ClientID, "m-bbb", 10), mandate.Change{BaseDigest: mb.Digest}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mandates.Put(ctx, doc(c.ClientID, "m-ccc", 60), admin); err != nil {
		t.Fatal(err)
	}
	md := e.putFrom(t, doc(d.ClientID, "m-ddd", 60), fromTemplate("voice", digestA))
	if _, err := e.mandates.Update(ctx, md.ID, doc(d.ClientID, "m-ddd", 5), mandate.Change{BaseDigest: md.Digest, Origin: fromTemplate("strict", digestB)}, admin); err != nil {
		t.Fatal(err)
	}
	uses, err := e.mandates.TemplateUses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 3 {
		t.Errorf("uses = %+v", uses)
	}
	for id, want := range map[string]struct {
		template, digest string
		edited           bool
	}{"m-aaa": {"voice", digestA, false}, "m-bbb": {"voice", digestA, true}, "m-ddd": {"strict", digestB, false}} {
		got, ok := uses[id]
		if !ok || got.Template != want.template || got.TemplateDigest != want.digest || got.EditedSince != want.edited || got.At.IsZero() {
			t.Errorf("use of %s = %+v (%v), want %+v", id, got, ok, want)
		}
	}
	if _, ok := uses["m-ccc"]; ok {
		t.Error("an edited mandate uses a template")
	}
	if got := uses[ma.ID]; !got.At.Equal(ma.UpdatedAt.Truncate(time.Nanosecond)) {
		t.Errorf("taken at %v, want %v", got.At, ma.UpdatedAt)
	}
}
