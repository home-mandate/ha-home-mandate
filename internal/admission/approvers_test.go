// SPDX-License-Identifier: AGPL-3.0-or-later

package admission_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

const otherApprover = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d"

func TestApproversForExpandsThePlaceholder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.adm.SetApprovers(func(context.Context) ([]string, error) { return []string{otherApprover, admin.ID}, nil })

	got, err := e.adm.ApproversFor(ctx, "hm-voice-cautious", admin)
	if err != nil {
		t.Fatal(err)
	}
	// The admitting human first, then the approvers set up, each once.
	want := []string{admin.ID, otherApprover}
	if !slices.Equal(got.People, want) {
		t.Errorf("people = %v, want %v", got.People, want)
	}
	// The lock rule asks for unlock and open: critical actions only.
	if len(got.Normal) != 0 {
		t.Errorf("normal = %v, want none: the template asks only for critical actions", got.Normal)
	}
	if len(got.Critical) != 1 || !slices.Equal(got.Critical[0], want) {
		t.Errorf("critical = %v, want [%v]", got.Critical, want)
	}
}

func TestApproversForNamesExplicitApprovers(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	// The example of the specification names user-1 on the mandate and the lock rule.
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, func(d map[string]any) {
		rules := d["rules"].([]any)
		rules = append(rules, map[string]any{"id": "r-ask-lights", "resource": map[string]any{"category": "light"},
			"actions": []any{"turn_off"}, "decision": "ask",
			"approval": map[string]any{"timeout": "PT1M", "approvers": []any{"$approvers", "user-2"}}})
		d["rules"] = rules
	}), admin); err != nil {
		t.Fatal(err)
	}
	got, err := e.adm.ApproversFor(ctx, "voice-assistant", admin)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"user-1", admin.ID, "user-2"}
	if !slices.Equal(got.People, want) {
		t.Errorf("people = %v, want %v", got.People, want)
	}
	if len(got.Normal) != 1 || !slices.Equal(got.Normal[0], []string{admin.ID, "user-2"}) {
		t.Errorf("normal = %v", got.Normal)
	}
	if len(got.Critical) != 1 || !slices.Equal(got.Critical[0], []string{"user-1"}) {
		t.Errorf("critical = %v", got.Critical)
	}
}

func TestApproversForWhatTheRulesCanAsk(t *testing.T) {
	ctx := context.Background()
	rule := func(decision string, resource map[string]any, actions ...any) func(map[string]any) {
		return func(d map[string]any) {
			d["rules"] = []any{map[string]any{"id": "r-1", "resource": resource, "actions": actions, "decision": decision}}
		}
	}
	light, lock, every := map[string]any{"category": "light"}, map[string]any{"category": "lock"}, map[string]any{"any": true}
	cases := []struct {
		name             string
		edit             func(map[string]any)
		normal, critical bool
	}{
		{"ask on an ordinary action", rule("ask", light, "turn_on"), true, false},
		{"ask on a critical action", rule("ask", lock, "unlock"), false, true},
		{"ask on both", rule("ask", lock, "lock", "unlock"), true, true},
		{"ask on everything of a category with critical actions", rule("ask", lock, "*"), true, true},
		{"ask on everything of a category without critical actions", rule("ask", light, "*"), true, false},
		{"ask on reading", rule("ask", every, "read"), true, false},
		{"ask without a category on an ordinary action", rule("ask", every, "turn_on"), true, false},
		{"ask without a category on an action critical somewhere", rule("ask", every, "open"), true, true},
		{"ask on one device", rule("ask", map[string]any{"entity_id": "lock.front"}, "unlock"), false, true},
		{"ask on everything of every device", rule("ask", every, "*"), true, true},
		{"allow of a critical action becomes ask", rule("allow", lock, "unlock"), false, true},
		{"allow of everything of a category with critical actions", rule("allow", lock, "*"), false, true},
		{"allow of ordinary actions asks nobody", rule("allow", light, "turn_on"), false, false},
		{"allow of reading everything asks nobody", rule("allow", every, "read"), false, false},
		{"deny asks nobody", rule("deny", lock, "unlock"), false, false},
		{"allow_critical asks nobody", func(d map[string]any) {
			d["rules"] = []any{map[string]any{"id": "r-1", "resource": lock, "actions": []any{"unlock"}, "decision": "allow",
				"allow_critical": true}}
		}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			if err := e.adm.PutTemplate(ctx, "t", template(t, c.edit), admin); err != nil {
				t.Fatal(err)
			}
			got, err := e.adm.ApproversFor(ctx, "t", admin)
			if err != nil {
				t.Fatal(err)
			}
			if (len(got.Normal) > 0) != c.normal || (len(got.Critical) > 0) != c.critical {
				t.Errorf("normal %v critical %v, want %v %v", got.Normal, got.Critical, c.normal, c.critical)
			}
			if !slices.Equal(got.People, []string{"user-1"}) {
				t.Errorf("people = %v", got.People)
			}
		})
	}
}

func TestApproversForWithoutAnyone(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	// Not a person (the command line): the placeholder stands for nobody, which the
	// preview shows instead of refusing.
	got, err := e.adm.ApproversFor(ctx, "hm-voice-cautious", audit.Actor{Kind: audit.ActorSystem, ID: "local-admin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.People) != 0 || len(got.Critical) != 1 || len(got.Critical[0]) != 0 {
		t.Errorf("got %+v, want nobody for the critical requests", got)
	}
}

func TestApproversForRefuses(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if _, err := e.adm.ApproversFor(ctx, "missing", admin); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("unknown template: %v", err)
	}
	if err := e.adm.SetHidden(ctx, "hm-read-only", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.adm.ApproversFor(ctx, "hm-read-only", admin); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("hidden base template: %v", err)
	}
	e.adm.SetApprovers(func(context.Context) ([]string, error) { return nil, errors.New("database down") })
	if _, err := e.adm.ApproversFor(ctx, "hm-voice-cautious", admin); err == nil {
		t.Error("approvers unreadable: no error")
	}
	_ = e.db.Close()
	if _, err := e.adm.ApproversFor(ctx, "hm-voice-cautious", admin); err == nil {
		t.Error("database closed: no error")
	}
}
