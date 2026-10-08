// SPDX-License-Identifier: AGPL-3.0-or-later

package admission_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// templateChange is the template member of a template.changed entry with its actor.
type templateChange struct {
	Actor    audit.Actor    `json:"actor"`
	Template audit.Template `json:"template"`
}

// templateChanges returns the template.changed entries of the log, oldest first, after
// checking that the whole log verifies with the specification.
func templateChanges(t *testing.T, e env) []templateChange {
	t.Helper()
	ctx := context.Background()
	if r, err := e.log.Verify(ctx); err != nil || !r.Valid {
		t.Fatalf("Verify = %+v, %v", r, err)
	}
	var out bytes.Buffer
	if err := e.log.Export(ctx, &out); err != nil {
		t.Fatal(err)
	}
	var list []templateChange
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		var entry struct {
			Event string `json:"event"`
			templateChange
		}
		if len(line) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Event == audit.EventTemplateChanged {
			list = append(list, entry.templateChange)
		}
	}
	return list
}

// digestOf is the digest of a template as Home-Mandate shows it.
func digestOf(t *testing.T, e env, name string) string {
	t.Helper()
	_, info, err := e.adm.TemplateDocument(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return info.Digest
}

// Every change of a template is one template.changed entry with the human as actor:
// stored (new and changed, naming the digest it replaced), removed (with the last digest).
func TestTemplateChangesAreAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cli := audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}
	if err := e.adm.PutTemplate(ctx, "evening", template(t, nil), cli); err != nil {
		t.Fatal(err)
	}
	first := digestOf(t, e, "evening")
	edited := template(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	if err := e.adm.UpdateTemplate(ctx, "evening", edited, first, false, admin); err != nil {
		t.Fatal(err)
	}
	second := digestOf(t, e, "evening")
	if err := e.adm.RemoveTemplate(ctx, "evening", admin); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.UpdateTemplate(ctx, "fresh", template(t, nil), "", false, admin); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.PutTemplate(ctx, "fresh", edited, cli); err != nil {
		t.Fatal(err)
	}
	want := []templateChange{
		{cli, audit.Template{Change: audit.TemplateStored, Name: "evening", Digest: first}},
		{admin, audit.Template{Change: audit.TemplateStored, Name: "evening", Digest: second, PreviousDigest: first}},
		{admin, audit.Template{Change: audit.TemplateRemoved, Name: "evening", Digest: second}},
		{admin, audit.Template{Change: audit.TemplateStored, Name: "fresh", Digest: first}},
		{cli, audit.Template{Change: audit.TemplateStored, Name: "fresh", Digest: second, PreviousDigest: first}},
	}
	got := templateChanges(t, e)
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Hiding and showing a base template is recorded once per actual change; hiding a hidden
// one or showing a shown one changes nothing and records nothing.
func TestHidingBaseTemplatesIsAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	steps := []struct {
		hide bool
		want string // "" for no entry
	}{{false, ""}, {true, audit.TemplateHidden}, {true, ""}, {false, audit.TemplateShown}, {false, ""}}
	var want []templateChange
	for _, s := range steps {
		if err := e.adm.SetHidden(ctx, "hm-voice-cautious", s.hide, admin); err != nil {
			t.Fatal(err)
		}
		if s.want != "" {
			want = append(want, templateChange{admin, audit.Template{Change: s.want, Name: "hm-voice-cautious", Digest: digestOf(t, e, "hm-voice-cautious")}})
		}
	}
	got := templateChanges(t, e)
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Saving unchanged content stores nothing and records nothing: previous_digest must
// differ from digest. The digest follows SPEC-v0 section 3.2, so key order and white
// space do not count as a change.
func TestUnchangedTemplatesAreNotAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "same", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	base := digestOf(t, e, "same")
	if err := e.adm.UpdateTemplate(ctx, "same", template(t, nil), base, false, admin); err != nil {
		t.Fatal(err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, template(t, nil), "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.PutTemplate(ctx, "same", indented.Bytes(), admin); err != nil {
		t.Fatal(err)
	}
	if got := templateChanges(t, e); len(got) != 1 {
		t.Errorf("entries = %+v, want only the first store", got)
	}
	if digestOf(t, e, "same") != base {
		t.Error("the digest changed with the form of the document")
	}
}

// A refused or failed change records nothing, and a change that cannot be recorded is
// not made.
func TestRefusedTemplateChangesAreNotAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "mine", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	base := digestOf(t, e, "mine")
	unrecordable := audit.Actor{Kind: audit.ActorUser, ID: "user\u202Ex"}
	edited := template(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	refused := map[string]func() error{
		"invalid document":   func() error { return e.adm.PutTemplate(ctx, "mine", []byte(`{}`), admin) },
		"base template name": func() error { return e.adm.PutTemplate(ctx, "hm-read-only", template(t, nil), admin) },
		"stale base": func() error {
			return e.adm.UpdateTemplate(ctx, "mine", edited, "sha256:"+string(bytes.Repeat([]byte("0"), 64)), false, admin)
		},
		"critical unconfirmed": func() error {
			return e.adm.UpdateTemplate(ctx, "mine", criticalTemplate(t), base, false, admin)
		},
		"remove unknown":      func() error { return e.adm.RemoveTemplate(ctx, "nothing", admin) },
		"remove base":         func() error { return e.adm.RemoveTemplate(ctx, "hm-read-only", admin) },
		"hide own template":   func() error { return e.adm.SetHidden(ctx, "mine", true, admin) },
		"put unrecordable":    func() error { return e.adm.PutTemplate(ctx, "mine", edited, unrecordable) },
		"update unrecordable": func() error { return e.adm.UpdateTemplate(ctx, "mine", edited, base, false, unrecordable) },
		"remove unrecordable": func() error { return e.adm.RemoveTemplate(ctx, "mine", unrecordable) },
		"hide unrecordable":   func() error { return e.adm.SetHidden(ctx, "hm-read-only", true, unrecordable) },
	}
	for name, change := range refused {
		if err := change(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := templateChanges(t, e); len(got) != 1 {
		t.Errorf("entries = %+v, want only the first store", got)
	}
	if digestOf(t, e, "mine") != base {
		t.Error("a change that could not be recorded was made")
	}
	list, err := e.adm.Templates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range list {
		if tmpl.Hidden {
			t.Errorf("%s hidden without an entry", tmpl.Name)
		}
	}
}

func TestTemplateChangesReportDatabaseErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.db.Close()
	if err := e.adm.SetHidden(ctx, "hm-read-only", true, admin); err == nil || errors.Is(err, admission.ErrInvalidTemplate) {
		t.Errorf("SetHidden = %v", err)
	}
	if err := e.adm.UpdateTemplate(ctx, "mine", template(t, nil), "", false, admin); err == nil || errors.Is(err, mandate.ErrConflict) {
		t.Errorf("UpdateTemplate = %v", err)
	}
}
