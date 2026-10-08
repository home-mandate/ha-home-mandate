// SPDX-License-Identifier: AGPL-3.0-or-later

package admission_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

var baseNames = []string{"hm-read-only", "hm-light-climate", "hm-voice-cautious"}

func TestBaseTemplatesAreListedFirstWithTitles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "aaa-own", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	list, err := e.adm.Templates(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("templates = %+v, %v", list, err)
	}
	for i, name := range baseNames {
		b := list[i]
		if b.Name != name || !b.Builtin || b.Hidden || b.Title["de"] == "" || b.Title["en"] == "" ||
			b.Description["de"] == "" || b.Description["en"] == "" {
			t.Errorf("base template %d = %+v", i, b)
		}
	}
	if list[3].Name != "aaa-own" || list[3].Builtin {
		t.Errorf("own template = %+v", list[3])
	}
}

// Every base template yields a valid mandate whose approvers are real people.
func TestBaseTemplatesAdmitWithTheAdmittingHuman(t *testing.T) {
	for _, name := range baseNames {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			req := request()
			req.Template = name
			a, _, err := e.adm.Admit(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			doc := documentOf(t, e, a.ClientID)
			if slices.Contains(approversIn(t, doc), mandate.ApproversPlaceholder) {
				t.Errorf("placeholder left in %s", doc)
			}
			if got := approversIn(t, doc); !slices.Equal(got, []string{admin.ID}) {
				t.Errorf("approvers = %v, want the admitting human", got)
			}
		})
	}
}

// documentOf is the stored mandate of an agent.
func documentOf(t *testing.T, e env, clientID string) []byte {
	t.Helper()
	var doc string
	if err := e.db.QueryRow(`SELECT v.document FROM mandates m JOIN mandate_versions v ON v.mandate_id = m.id
		WHERE m.client_id = ? ORDER BY v.version DESC LIMIT 1`, clientID).Scan(&doc); err != nil {
		t.Fatal(err)
	}
	return []byte(doc)
}

// approversIn returns the distinct approvers of a mandate, mandate level first.
func approversIn(t *testing.T, document []byte) []string {
	t.Helper()
	var d struct {
		Approval struct{ Approvers []string } `json:"approval"`
		Rules    []struct {
			Approval *struct{ Approvers []string } `json:"approval"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(document, &d); err != nil {
		t.Fatal(err)
	}
	var out []string
	add := func(l []string) {
		for _, a := range l {
			if !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	add(d.Approval.Approvers)
	for _, r := range d.Rules {
		if r.Approval != nil {
			add(r.Approval.Approvers)
		}
	}
	return out
}

func TestPlaceholderTakesTheApproversToo(t *testing.T) {
	e := newEnv(t)
	other := "1a2b3c4d5e6f708192a3b4c5d6e7f809"
	e.adm.SetApprovers(func(context.Context) ([]string, error) { return []string{other, admin.ID}, nil })
	req := request()
	req.Template = "hm-voice-cautious"
	a, _, err := e.adm.Admit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := approversIn(t, documentOf(t, e, a.ClientID)); !slices.Equal(got, []string{admin.ID, other}) {
		t.Errorf("approvers = %v", got)
	}
}

func TestPlaceholderWithoutAnyoneRefusesTheAdmission(t *testing.T) {
	e := newEnv(t)
	req := request()
	req.Template = "hm-read-only"
	req.By = audit.Actor{Kind: audit.ActorUser, ID: "local-admin"} // the command line is no person
	if _, _, err := e.adm.Admit(context.Background(), req); !errors.Is(err, mandate.ErrNoApprovers) {
		t.Errorf("err = %v, want ErrNoApprovers", err)
	}
	if n := count(t, e.db, "agents"); n != 0 {
		t.Errorf("%d agents after a refused admission", n)
	}
	e.adm.SetApprovers(func(context.Context) ([]string, error) { return nil, errors.New("database down") })
	req.By = admin
	if _, _, err := e.adm.Admit(context.Background(), req); err == nil {
		t.Error("admitted although the approvers could not be read")
	}
}

func TestBaseTemplatesCannotBeChanged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, name := range baseNames {
		if err := e.adm.PutTemplate(ctx, name, template(t, nil), admin); !errors.Is(err, admission.ErrBuiltinTemplate) {
			t.Errorf("put %s = %v", name, err)
		}
		if err := e.adm.UpdateTemplate(ctx, name, template(t, nil), "", true, admin); !errors.Is(err, admission.ErrBuiltinTemplate) {
			t.Errorf("update %s = %v", name, err)
		}
		if err := e.adm.RemoveTemplate(ctx, name); !errors.Is(err, admission.ErrBuiltinTemplate) {
			t.Errorf("remove %s = %v", name, err)
		}
		doc, info, err := e.adm.TemplateDocument(ctx, name)
		if err != nil || !info.Builtin || !json.Valid(doc) || info.Digest == "" {
			t.Errorf("document of %s = %v, %+v", name, err, info)
		}
		// Saved under a new name it is an ordinary template.
		if err := e.adm.PutTemplate(ctx, "copy-of-"+name[3:], doc, admin); err != nil {
			t.Errorf("saving %s as a new template: %v", name, err)
		}
	}
}

func TestHiddenBaseTemplatesAreNeitherOfferedNorAccepted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.SetHidden(ctx, "hm-voice-cautious", true); err != nil {
		t.Fatal(err)
	}
	list, _ := e.adm.Templates(ctx)
	if i := slices.IndexFunc(list, func(t admission.Template) bool { return t.Name == "hm-voice-cautious" }); i < 0 || !list[i].Hidden {
		t.Errorf("templates = %+v", list)
	}
	req := request()
	req.Template = "hm-voice-cautious"
	if _, _, err := e.adm.Admit(ctx, req); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("admission with a hidden template = %v", err)
	}
	// Still loadable into the editor.
	if _, _, err := e.adm.TemplateDocument(ctx, "hm-voice-cautious"); err != nil {
		t.Errorf("document of a hidden template: %v", err)
	}
	if err := e.adm.SetHidden(ctx, "hm-voice-cautious", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.adm.Admit(ctx, req); err != nil {
		t.Errorf("admission after showing it again: %v", err)
	}
	if err := e.adm.PutTemplate(ctx, "mine", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mine", "hm-unknown", "unknown"} {
		if err := e.adm.SetHidden(ctx, name, true); !errors.Is(err, admission.ErrInvalidTemplate) {
			t.Errorf("hiding %s = %v", name, err)
		}
	}
}

func TestTemplatesRefuseUnknownPlaceholders(t *testing.T) {
	e := newEnv(t)
	doc := template(t, func(d map[string]any) {
		d["approval"].(map[string]any)["approvers"] = []any{"$owner"}
	})
	if err := e.adm.PutTemplate(context.Background(), "x", doc, admin); !errors.Is(err, admission.ErrInvalidTemplate) {
		t.Errorf("err = %v, want ErrInvalidTemplate", err)
	}
	ok := template(t, func(d map[string]any) {
		d["approval"].(map[string]any)["approvers"] = []any{mandate.ApproversPlaceholder}
	})
	if err := e.adm.PutTemplate(context.Background(), "y", ok, admin); err != nil {
		t.Errorf("a template with the placeholder: %v", err)
	}
}

func TestNewMandateFromABaseTemplate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	req := request()
	req.Template = "hm-read-only"
	a, _, err := e.adm.Admit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.Revoke(ctx, m.Info.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.adm.NewMandate(ctx, a.ClientID, "hm-light-climate", "", false, admin); err != nil {
		t.Errorf("new mandate from a base template: %v", err)
	}
}

func TestResolvedTemplateForAnExistingMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	doc, err := e.adm.Resolved(ctx, "hm-voice-cautious", admin)
	if err != nil {
		t.Fatal(err)
	}
	if got := approversIn(t, doc); !slices.Equal(got, []string{admin.ID}) {
		t.Errorf("approvers = %v", got)
	}
	if err := e.adm.SetHidden(ctx, "hm-voice-cautious", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.adm.Resolved(ctx, "hm-voice-cautious", admin); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("hidden template = %v", err)
	}
	if _, err := e.adm.Resolved(ctx, "hm-read-only", audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); !errors.Is(err, mandate.ErrNoApprovers) {
		t.Errorf("nobody to approve = %v", err)
	}
	if _, err := e.adm.Resolved(ctx, "unknown", admin); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("unknown template = %v", err)
	}
}
