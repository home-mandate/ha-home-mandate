// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

func criticalRule() map[string]any {
	return map[string]any{"id": "r-unlock", "resource": map[string]any{"category": "lock"}, "actions": []any{"unlock"},
		"decision": "allow", "allow_critical": true}
}

func TestMandateListAndDetail(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	var list []wireMandateSummary
	h.ok(http.MethodGet, "/api/mandates", nil, &list)
	if len(list) != 1 || list[0].ID != m.ID || list[0].Name != "voice-assistant" || list[0].ClientID != voice.ClientID ||
		list[0].AgentDisplayName != "Voice" || list[0].Status != "active" || list[0].RuleCount == 0 || list[0].ValidFrom == "" ||
		list[0].Expires != nil || list[0].MaxActionsPerHour != 60 || list[0].Digest != m.Digest {
		t.Errorf("list = %+v", list)
	}
	var d wireMandateDetail
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &d)
	if d.Summary.ID != m.ID || len(d.Versions) != 1 || d.Versions[0].Number != 1 || *d.Versions[0].CreatedByName != "Markus" ||
		!strings.Contains(string(d.Document), `"principal":"`+household) {
		t.Errorf("detail = %+v", d)
	}
	var doc map[string]any
	h.ok(http.MethodGet, "/api/mandates/"+m.ID+"/versions/1", nil, &doc)
	if doc["id"] != m.ID {
		t.Errorf("version 1 = %v", doc)
	}
	for _, path := range []string{"/api/mandates/m-none", "/api/mandates/x", "/api/mandates/" + m.ID + "/versions/2",
		"/api/mandates/" + m.ID + "/versions/0", "/api/mandates/" + m.ID + "/versions/-1", "/api/mandates/" + m.ID + "/versions/1e3",
		"/api/mandates/%2e%2e/versions/1", "/api/mandates/a%2Fb"} {
		if r := h.do(http.MethodGet, path, nil); r.code != http.StatusNotFound {
			t.Errorf("%s = %d", path, r.code)
		}
	}
}

// currentDraft is the editable part of a mandate's current version.
func currentDraft(t *testing.T, h *harness, id string) map[string]any {
	t.Helper()
	var d wireMandateDetail
	h.ok(http.MethodGet, "/api/mandates/"+id, nil, &d)
	var doc map[string]any
	_ = json.Unmarshal(d.Document, &doc)
	out := map[string]any{}
	for _, k := range draftKeys {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}
	return out
}

func update(name string, d map[string]any, base string, confirm bool) map[string]any {
	u := map[string]any{"name": name, "draft": d, "base_digest": base}
	if confirm {
		u["confirm_critical"] = true
	}
	return u
}

// Negative catalog, mandate: the server enforces Store.Update whatever the UI checked.
func TestPutMandate(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	path := "/api/mandates/" + m.ID

	// A rename alone stores no version, but needs the current version as its base.
	var d wireMandateDetail
	same := currentDraft(t, h, m.ID)
	if r := h.do(http.MethodPut, path, update("Alt", same, "sha256:"+strings.Repeat("0", 64), false)); r.errCode() != codeConflict {
		t.Errorf("rename on an old base = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPut, path, update("  Sprachassistent ", same, m.Digest, false), &d)
	if d.Summary.Name != "Sprachassistent" || len(d.Versions) != 1 || d.Summary.Digest != m.Digest {
		t.Errorf("rename = %+v", d.Summary)
	}
	// An edit stores a version; the identity comes from the server.
	edited := currentDraft(t, h, m.ID)
	edited["limits"] = map[string]any{"max_actions_per_hour": 7}
	h.ok(http.MethodPut, path, update("Sprachassistent", edited, m.Digest, false), &d)
	if len(d.Versions) != 2 || d.Summary.MaxActionsPerHour != 7 || d.Versions[0].Number != 2 || d.Versions[0].CreatedBy != adminID ||
		!strings.Contains(string(d.Document), `"created_by":"`+adminID) || !strings.Contains(string(d.Document), voice.ClientID) {
		t.Errorf("edit = %+v %s", d.Summary, d.Document)
	}
	newBase := d.Summary.Digest
	// Based on the old version: conflict, nothing stored.
	if r := h.do(http.MethodPut, path, update("X", draft(t), m.Digest, false)); r.errCode() != codeConflict {
		t.Errorf("old base = %d %s", r.code, r.body)
	}
	// allow_critical without the separate confirmation: refused; with it: stored.
	critical := draft(t)
	critical["rules"] = append(critical["rules"].([]any), criticalRule())
	if r := h.do(http.MethodPut, path, update("X", critical, newBase, false)); r.code != http.StatusUnprocessableEntity || r.errCode() != codeCriticalConfirm {
		t.Errorf("critical without confirmation = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPut, path, update("X", critical, newBase, true), &d)
	if len(d.Versions) != 3 {
		t.Errorf("critical with confirmation: %d versions", len(d.Versions))
	}
	base := d.Summary.Digest
	// Invalid drafts: the pointer leads to the field.
	for _, tc := range []struct {
		name  string
		edit  func(map[string]any)
		code  string
		field string
	}{
		{"action of another category", func(d map[string]any) {
			d["rules"] = []any{map[string]any{"id": "r1", "resource": map[string]any{"category": "light"}, "actions": []any{"unlock"}, "decision": "allow"}}
		}, codeInvalidMandate, "/draft/rules/0"},
		{"schema", func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": -1} }, codeInvalidMandate, "/draft/limits/max_actions_per_hour"},
		{"wrong decision", func(d map[string]any) {
			rules := d["rules"].([]any)
			rules[1].(map[string]any)["decision"] = "maybe"
		}, codeInvalidMandate, "/draft/rules/1/decision"},
		{"expires before valid_from", func(d map[string]any) { d["expires"] = "2020-01-01T00:00:00Z" }, codeInvalidMandate, "/draft/expires"},
		{"unknown draft field", func(d map[string]any) { d["default"] = "allow" }, codeInvalidInput, "/draft/default"},
		{"principal smuggled in", func(d map[string]any) { d["principal"] = "household:evil" }, codeInvalidInput, "/draft/principal"},
	} {
		dd := draft(t)
		tc.edit(dd)
		r := h.do(http.MethodPut, path, update("X", dd, base, true))
		if r.errCode() != tc.code || r.field() != tc.field {
			t.Errorf("%s = %d %s", tc.name, r.code, r.body)
		}
	}
	for _, tc := range []struct {
		body  any
		field string
	}{
		{update("", draft(t), base, false), "/name"},
		{update("\u202e", draft(t), base, false), "/name"},
		{update(strings.Repeat("n", 81), draft(t), base, false), "/name"},
		{update("X", draft(t), "sha256:old", false), "/base_digest"},
		{map[string]any{"name": "X", "base_digest": base}, "/draft"},
		{map[string]any{"name": "X", "base_digest": base, "draft": []any{}}, "/draft"},
		{map[string]any{"name": "X", "base_digest": base, "draft": nil}, "/draft"},
	} {
		if r := h.do(http.MethodPut, path, tc.body); r.errCode() != codeInvalidInput || r.field() != tc.field {
			t.Errorf("%v = %d %s", tc.body, r.code, r.body)
		}
	}
	if r := h.do(http.MethodPut, "/api/mandates/m-none", update("X", draft(t), base, false)); r.errCode() != codeNotFound {
		t.Errorf("unknown mandate = %d", r.code)
	}
	if r := h.do(http.MethodPut, "/api/mandates/!", update("X", draft(t), base, false)); r.errCode() != codeNotFound {
		t.Errorf("malformed id = %d", r.code)
	}
	// A revoked mandate cannot be edited.
	h.ok(http.MethodPost, path+"/revoke", nil, nil)
	if r := h.do(http.MethodPut, path, update("X", draft(t), base, false)); r.errCode() != codeConflict {
		t.Errorf("revoked = %d %s", r.code, r.body)
	}
}

func TestApplyTemplate(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	path := "/api/mandates/" + m.ID + "/apply-template"
	strict := voiceTemplate(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 5} })
	if err := h.adm.PutTemplate(context.Background(), "strict", strict, audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		t.Fatal(err)
	}
	if err := h.adm.PutTemplate(context.Background(), "doors", voiceTemplate(t, func(d map[string]any) {
		d["rules"] = []any{criticalRule()}
	}), audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		t.Fatal(err)
	}
	var d wireMandateDetail
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Mein Name", currentDraft(t, h, m.ID), m.Digest, false), &d)
	h.ok(http.MethodPost, path, map[string]any{"template": "strict", "base_digest": d.Summary.Digest}, &d)
	if d.Summary.MaxActionsPerHour != 5 || d.Summary.Name != "Mein Name" || len(d.Versions) != 2 {
		t.Errorf("applied = %+v", d.Summary)
	}
	base := d.Summary.Digest
	if r := h.do(http.MethodPost, path, map[string]any{"template": "doors", "base_digest": base}); r.errCode() != codeCriticalConfirm {
		t.Errorf("critical template without confirmation = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPost, path, map[string]any{"template": "doors", "base_digest": base, "confirm_critical": true}, &d)
	for _, tc := range []struct {
		body        map[string]any
		code, field string
	}{
		{map[string]any{"template": "none", "base_digest": d.Summary.Digest}, codeInvalidInput, "/template"},
		{map[string]any{"template": "strict", "base_digest": "x"}, codeInvalidInput, "/base_digest"},
		{map[string]any{"template": "strict", "base_digest": base}, codeConflict, ""},
	} {
		if r := h.do(http.MethodPost, path, tc.body); r.errCode() != tc.code || r.field() != tc.field {
			t.Errorf("%v = %d %s", tc.body, r.code, r.body)
		}
	}
	if r := h.do(http.MethodPost, "/api/mandates/m-none/apply-template", map[string]any{"template": "strict", "base_digest": base}); r.errCode() != codeNotFound {
		t.Errorf("unknown mandate = %d", r.code)
	}
}

func TestCreateMandate(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	if err := h.adm.PutTemplate(context.Background(), "doors", voiceTemplate(t, func(d map[string]any) {
		d["rules"] = []any{criticalRule()}
	}), audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"client_id": voice.ClientID, "template": "voice-assistant"}
	if r := h.do(http.MethodPost, "/api/mandates", body); r.errCode() != codeConflict {
		t.Errorf("agent with an active mandate = %d %s", r.code, r.body)
	}
	first := h.mandateOf(voice.ClientID)
	h.ok(http.MethodPost, "/api/mandates/"+first.ID+"/revoke", nil, nil)
	if r := h.do(http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "doors"}); r.errCode() != codeCriticalConfirm {
		t.Errorf("critical template = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "none"}); r.field() != "/template" {
		t.Errorf("unknown template = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "voice-assistant", "name": "\u202e"}); r.field() != "/name" {
		t.Errorf("bad name = %d %s", r.code, r.body)
	}
	var d wireMandateDetail
	h.ok(http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "voice-assistant", "name": "Neu"}, &d)
	if d.Summary.ID == first.ID || d.Summary.Name != "Neu" || d.Summary.Status != "active" {
		t.Errorf("new mandate = %+v", d.Summary)
	}
	var agents []wireAgent
	h.ok(http.MethodGet, "/api/agents", nil, &agents)
	if agents[0].Mandate.ID != d.Summary.ID {
		t.Errorf("agent's mandate = %+v", agents[0].Mandate)
	}
	if r := h.do(http.MethodPost, "/api/mandates", map[string]any{"client_id": "hm-client:none", "template": "voice-assistant"}); r.errCode() != codeNotFound {
		t.Errorf("unknown agent = %d", r.code)
	}
	h.ok(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": voice.ClientID}, nil)
	if r := h.do(http.MethodPost, "/api/mandates", body); r.errCode() != codeConflict {
		t.Errorf("revoked agent = %d", r.code)
	}
}

// Revoking a mandate ends the open requests of its agent: they could not be carried out.
func TestRevokeMandate(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	req := lightRequest(adminID)
	req.ClientID = voice.ClientID
	_, done := h.ask(req)
	var s wireMandateSummary
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/revoke", nil, &s)
	if s.Status != "revoked" {
		t.Errorf("summary = %+v", s)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("open request not ended")
	}
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/revoke", nil, &s) // again: no-op
	if r := h.do(http.MethodPost, "/api/mandates/m-none/revoke", nil); r.errCode() != codeNotFound {
		t.Errorf("unknown = %d", r.code)
	}
}

func TestBaseTemplatesThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	var tmpl wireTemplate
	h.ok(http.MethodGet, "/api/templates/hm-voice-cautious", nil, &tmpl)
	if !tmpl.Builtin || tmpl.Draft == nil || !strings.Contains(string(tmpl.Draft), "$approvers") {
		t.Errorf("base template = %+v %s", tmpl.wireTemplateInfo, tmpl.Draft)
	}
	d := draft(t)
	for _, tc := range []struct{ method, path, code string }{
		{http.MethodPut, "/api/templates/hm-voice-cautious", codeBuiltinTemplate},
		{http.MethodDelete, "/api/templates/hm-read-only", codeBuiltinTemplate},
		{http.MethodPut, "/api/templates/hm-mine", codeInvalidInput},
	} {
		var body any
		if tc.method == http.MethodPut {
			body = map[string]any{"draft": d, "base_digest": tmpl.Digest}
		}
		if r := h.do(tc.method, tc.path, body); r.errCode() != tc.code {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, r.code, r.body)
		}
	}
	// Saved under a new name it is the household's own; the placeholder stays a placeholder.
	var copied wireTemplate
	h.ok(http.MethodPut, "/api/templates/my-voice", map[string]any{"draft": json.RawMessage(tmpl.Draft)}, &copied)
	if copied.Builtin || !strings.Contains(string(copied.Draft), "$approvers") {
		t.Errorf("copy = %+v %s", copied.wireTemplateInfo, copied.Draft)
	}

	// Hiding: only base templates; hidden ones stay in the list, marked.
	h.ok(http.MethodPut, "/api/templates/hm-voice-cautious/hidden", map[string]any{"hidden": true}, nil)
	var list []wireTemplateInfo
	h.ok(http.MethodGet, "/api/templates", nil, &list)
	if !list[2].Hidden {
		t.Errorf("not hidden: %+v", list[2])
	}
	for _, tc := range []struct {
		path string
		body any
		code string
	}{
		{"/api/templates/my-voice/hidden", map[string]any{"hidden": true}, codeNotFound},
		{"/api/templates/hm-read-only/hidden", map[string]any{}, codeInvalidInput},
		{"/api/templates/hm-read-only/hidden", map[string]any{"hidden": "yes"}, codeInvalidInput},
	} {
		if r := h.do(http.MethodPut, tc.path, tc.body); r.errCode() != tc.code {
			t.Errorf("%s %v = %d %s", tc.path, tc.body, r.code, r.body)
		}
	}
	h.ok(http.MethodPut, "/api/templates/hm-voice-cautious/hidden", map[string]any{"hidden": false}, nil)
}

func TestTemplates(t *testing.T) {
	h := newHarness(t)
	var list []wireTemplateInfo
	h.ok(http.MethodGet, "/api/templates", nil, &list)
	if len(list) != 4 || list[3].Name != "voice-assistant" || list[3].RuleCount == 0 || *list[3].CreatedByName != "Markus" ||
		list[3].Builtin || list[3].CreatedAt == nil || !digestPattern.MatchString(list[3].Digest) {
		t.Errorf("templates = %+v", list)
	}
	for i, name := range []string{"hm-read-only", "hm-light-climate", "hm-voice-cautious"} {
		b := list[i]
		if b.Name != name || !b.Builtin || b.CreatedAt != nil || b.CreatedByName != nil || b.Title["de"] == "" || b.Description["en"] == "" {
			t.Errorf("base template %d = %+v", i, b)
		}
	}
	var tmpl wireTemplate
	h.ok(http.MethodGet, "/api/templates/voice-assistant", nil, &tmpl)
	var dr map[string]any
	_ = json.Unmarshal(tmpl.Draft, &dr)
	if tmpl.Name != "voice-assistant" || dr["rules"] == nil || dr["valid_from"] == nil || dr["expires"] != nil || dr["id"] != nil {
		t.Errorf("template = %s", tmpl.Draft)
	}
	d := draft(t)
	d["expires"] = "2099-01-01T00:00:00Z"
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": d}, &tmpl)
	_ = json.Unmarshal(tmpl.Draft, &dr)
	if tmpl.Name != "garden" || dr["expires"] != nil {
		t.Errorf("stored = %s", tmpl.Draft)
	}
	crit := draft(t)
	crit["rules"] = []any{criticalRule()}
	if r := h.do(http.MethodPut, "/api/templates/garden", map[string]any{"draft": crit, "base_digest": tmpl.Digest}); r.errCode() != codeCriticalConfirm {
		t.Errorf("critical template = %d %s", r.code, r.body)
	}
	// Without the version the edit started from, or with an outdated one: nothing is overwritten.
	for _, base := range []any{nil, "sha256:" + strings.Repeat("0", 64)} {
		if r := h.do(http.MethodPut, "/api/templates/garden", map[string]any{"draft": crit, "base_digest": base, "confirm_critical": true}); r.errCode() != codeConflict {
			t.Errorf("base %v = %d %s", base, r.code, r.body)
		}
	}
	if r := h.do(http.MethodPut, "/api/templates/garden", map[string]any{"draft": crit, "base_digest": "x"}); r.field() != "/base_digest" {
		t.Errorf("malformed base digest = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": crit, "base_digest": tmpl.Digest, "confirm_critical": true}, &tmpl)
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": crit, "base_digest": tmpl.Digest}, &tmpl) // unchanged: no new confirmation
	bad := draft(t)
	bad["limits"] = map[string]any{"max_actions_per_hour": "many"}
	for _, tc := range []struct {
		path  string
		body  any
		code  string
		field string
	}{
		{"/api/templates/Garden", map[string]any{"draft": d}, codeInvalidInput, "/name"},
		{"/api/templates/-x", map[string]any{"draft": d}, codeInvalidInput, "/name"},
		{"/api/templates/garden", map[string]any{"draft": "x"}, codeInvalidInput, "/draft"},
		{"/api/templates/garden", map[string]any{"draft": map[string]any{"id": "m-evil"}}, codeInvalidInput, "/draft/id"},
		{"/api/templates/garden", map[string]any{"draft": bad, "base_digest": tmpl.Digest}, codeInvalidMandate, "/draft/limits/max_actions_per_hour"},
	} {
		if r := h.do(http.MethodPut, tc.path, tc.body); r.errCode() != tc.code || r.field() != tc.field {
			t.Errorf("%s %v = %d %s", tc.path, tc.body, r.code, r.body)
		}
	}
	h.ok(http.MethodGet, "/api/templates", nil, &list)
	if len(list) != 5 {
		t.Errorf("templates = %+v", list)
	}
	h.ok(http.MethodDelete, "/api/templates/garden", nil, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodDelete, "/api/templates/garden"}, {http.MethodGet, "/api/templates/garden"},
		{http.MethodGet, "/api/templates/Bad_Name"}, {http.MethodDelete, "/api/templates/..."},
	} {
		if r := h.do(tc.method, tc.path, nil); r.errCode() != codeNotFound {
			t.Errorf("%s %s = %d", tc.method, tc.path, r.code)
		}
	}
}

// A template stored by the command line may lack valid_from; its draft then starts at
// the template's creation.
func TestTemplateWithoutValidFrom(t *testing.T) {
	h := newHarness(t)
	doc := voiceTemplate(t, func(d map[string]any) { delete(d, "valid_from") })
	if _, err := h.st.DB().Exec(`INSERT INTO mandate_templates (name, document, created_at, created_by) VALUES ('cli', ?, '2026-10-01T08:00:00Z', 'local-admin')`, string(doc)); err != nil {
		t.Fatal(err)
	}
	var tmpl wireTemplate
	h.ok(http.MethodGet, "/api/templates/cli", nil, &tmpl)
	if !strings.Contains(string(tmpl.Draft), `"valid_from":"2026-10-01T08:00:00Z"`) {
		t.Errorf("draft = %s", tmpl.Draft)
	}
}

func TestInvalidField(t *testing.T) {
	rules := json.RawMessage(`{"rules":[{"id":"a"},{"id":"b"}]}`)
	leaf := &jsonschema.ValidationError{InstanceLocation: []string{"rules", "1", "actions"}}
	nested := &jsonschema.ValidationError{Causes: []*jsonschema.ValidationError{leaf}}
	outside := &jsonschema.ValidationError{InstanceLocation: []string{"agent", "client_id"}}
	odd := &jsonschema.ValidationError{InstanceLocation: []string{"rules", "a/b~c"}}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: %w", mandate.ErrInvalid, nested), "/draft/rules/1/actions"},
		{fmt.Errorf("%w: %w", mandate.ErrInvalid, outside), "/draft"},
		{fmt.Errorf("%w: %w", mandate.ErrInvalid, odd), "/draft/rules/a~1b~0c"},
		{fmt.Errorf("%w: rule %q: action", mandate.ErrInvalid, "b"), "/draft/rules/1"},
		{fmt.Errorf("%w: rule %q: action", mandate.ErrInvalid, "zzz"), "/draft"},
		{fmt.Errorf("%w: expires must be after valid_from", mandate.ErrInvalid), "/draft/expires"},
		{errors.New("other"), "/draft"},
	} {
		if got := invalidField(tc.err, "/draft", rules); got != tc.want {
			t.Errorf("invalidField(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestSettings(t *testing.T) {
	h := newHarness(t)
	var d wireDefaults
	h.ok(http.MethodGet, "/api/settings", nil, &d)
	if d != (wireDefaults{ApprovalTimeout: "PT2M", MaxActionsPerHour: 60}) || h.srv.BellEnabled() {
		t.Errorf("defaults = %+v", d)
	}
	h.ok(http.MethodPut, "/api/settings", map[string]any{"approval_timeout": "PT1M30S", "max_actions_per_hour": 120, "bell": true}, &d)
	if d != (wireDefaults{ApprovalTimeout: "PT1M30S", MaxActionsPerHour: 120, Bell: true}) || !h.srv.BellEnabled() {
		t.Errorf("stored = %+v", d)
	}
	// Survives a restart.
	h.build()
	if err := h.srv.LoadSettings(context.Background()); err != nil || !h.srv.BellEnabled() {
		t.Errorf("after restart: bell %v, %v", h.srv.BellEnabled(), err)
	}
	for _, tc := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"approval_timeout": "PT9S", "max_actions_per_hour": 1, "bell": false}, "/approval_timeout"},
		{map[string]any{"approval_timeout": "PT61M", "max_actions_per_hour": 1, "bell": false}, "/approval_timeout"},
		{map[string]any{"approval_timeout": "2m", "max_actions_per_hour": 1, "bell": false}, "/approval_timeout"},
		{map[string]any{"approval_timeout": "PT", "max_actions_per_hour": 1, "bell": false}, "/approval_timeout"},
		{map[string]any{"max_actions_per_hour": 1, "bell": false}, "/approval_timeout"},
		{map[string]any{"approval_timeout": "PT1M", "max_actions_per_hour": 0, "bell": false}, "/max_actions_per_hour"},
		{map[string]any{"approval_timeout": "PT1M", "max_actions_per_hour": 1001, "bell": false}, "/max_actions_per_hour"},
		{map[string]any{"approval_timeout": "PT1M", "bell": false}, "/max_actions_per_hour"},
		{map[string]any{"approval_timeout": "PT1M", "max_actions_per_hour": 1}, "/bell"},
		{map[string]any{"approval_timeout": "PT1M", "max_actions_per_hour": 1.5, "bell": true}, "/max_actions_per_hour"},
	} {
		if r := h.do(http.MethodPut, "/api/settings", tc.body); r.errCode() != codeInvalidInput || r.field() != tc.field {
			t.Errorf("%v = %d %s", tc.body, r.code, r.body)
		}
	}
	// Broken stored values fall back to the defaults.
	_ = h.st.SetSetting(context.Background(), settingTimeout, "forever")
	_ = h.st.SetSetting(context.Background(), settingRateLimit, "-3")
	h.ok(http.MethodGet, "/api/settings", nil, &d)
	if d.ApprovalTimeout != "PT2M" || d.MaxActionsPerHour != 60 {
		t.Errorf("broken stored = %+v", d)
	}
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{{"PT2M", 2 * time.Minute, true}, {"PT45S", 45 * time.Second, true}, {"PT1M5S", 65 * time.Second, true}, {"PT", 0, false},
		{"P1D", 0, false}, {"PT1H", 0, false}, {"pt2m", 0, false}} {
		if got, ok := parseTimeout(tc.in); got != tc.want || ok != tc.ok {
			t.Errorf("parseTimeout(%q) = %v, %v", tc.in, got, ok)
		}
	}
}

// Switching the emergency stop on ends every open request at once (decision F1).
func TestEmergencyStop(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	_, done := h.ask(lightRequest(adminID))
	var stop wireStop
	h.ok(http.MethodPut, "/api/emergency-stop", map[string]any{"active": true}, &stop)
	if !stop.Active || *stop.ByName != "Markus" || stop.Since == nil {
		t.Errorf("stop = %+v", stop)
	}
	select {
	case res := <-done:
		if res.Outcome != approval.OutcomeCancelled {
			t.Errorf("request ended with %s", res.Outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open request not ended by the emergency stop")
	}
	h.ok(http.MethodPut, "/api/emergency-stop", map[string]any{"active": true}, &stop) // again: unchanged
	h.ok(http.MethodPut, "/api/emergency-stop", map[string]any{"active": false}, &stop)
	if stop.Active || stop.Since != nil || stop.ByName != nil {
		t.Errorf("released = %+v", stop)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"active": "yes"}} {
		if r := h.do(http.MethodPut, "/api/emergency-stop", body); r.errCode() != codeInvalidInput {
			t.Errorf("%v = %d", body, r.code)
		}
	}
}

// A base template applied to a mandate puts the human who applies it in place of the
// approvers placeholder; a hidden one is refused as at admission.
func TestApplyABaseTemplate(t *testing.T) {
	h := newHarness(t)
	a := h.admit("Garten")
	m := h.mandateOf(a.ClientID)
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/apply-template", map[string]any{"template": "hm-voice-cautious", "base_digest": m.Digest}, nil)
	_, doc, err := h.mandates.Current(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "$approvers") || !strings.Contains(string(doc), adminID) {
		t.Errorf("mandate after applying a base template = %s", doc)
	}
	h.ok(http.MethodPut, "/api/templates/hm-read-only/hidden", map[string]any{"hidden": true}, nil)
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/apply-template", map[string]any{"template": "hm-read-only", "base_digest": h.mandateOf(a.ClientID).Digest}); r.field() != "/template" {
		t.Errorf("hidden template = %d %s", r.code, r.body)
	}
}
