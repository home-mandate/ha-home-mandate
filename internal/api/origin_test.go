// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

func (h *harness) countEvents(event string) int {
	h.t.Helper()
	var n int
	if err := h.st.DB().QueryRow(`SELECT count(*) FROM audit_log WHERE event = ?`, event).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) putTemplate(name string, edit func(map[string]any)) {
	h.t.Helper()
	if err := h.adm.PutTemplate(context.Background(), name, voiceTemplate(h.t, edit), audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) templateDigest(name string) string {
	h.t.Helper()
	_, t, err := h.adm.TemplateDocument(context.Background(), name)
	if err != nil {
		h.t.Fatal(err)
	}
	return t.Digest
}

// A new mandate is named after its agent and says which template its rules came from.
func TestNewMandatesAreNamedAfterTheirAgent(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Claude Desktop")
	m := h.mandateOf(voice.ClientID)
	var d wireMandateDetail
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &d)
	if d.Summary.Name != "Claude Desktop" {
		t.Errorf("name after admission = %q", d.Summary.Name)
	}
	digest := h.templateDigest("voice-assistant")
	if v := d.Versions[0]; v.Origin != "template" || v.Template == nil || *v.Template != "voice-assistant" ||
		v.TemplateDigest == nil || *v.TemplateDigest != digest {
		t.Errorf("origin of the first version = %+v", v)
	}
	if rf := d.Summary.RulesFrom; rf == nil || rf.Template != "voice-assistant" || rf.TemplateDigest != digest || rf.EditedSince || rf.At == "" {
		t.Errorf("rules from = %+v", rf)
	}
	// A new mandate for the agent after the first was revoked: named after the agent too.
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/revoke", nil, nil)
	h.ok(http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "voice-assistant"}, &d)
	if d.Summary.Name != "Claude Desktop" || d.Summary.RulesFrom == nil {
		t.Errorf("new mandate = %+v", d.Summary)
	}
	h.ok(http.MethodPost, "/api/mandates/"+d.Summary.ID+"/revoke", nil, nil)
	h.ok(http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "voice-assistant", "name": "   "}, &d)
	if d.Summary.Name != "Claude Desktop" {
		t.Errorf("new mandate with a blank name = %q", d.Summary.Name)
	}
}

// Applying a template stores where the rules came from; applying it again, or a
// template with the same content, stores nothing and says so. Edits show as edits.
func TestApplyingATemplateKeepsItsOrigin(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	path := "/api/mandates/" + m.ID + "/apply-template"
	h.putTemplate("strict", func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 5} })
	updated := h.countEvents(audit.EventMandateUpdated)

	// The template the mandate came from, applied again: unchanged.
	var res wireApplyResult
	h.ok(http.MethodPost, path, map[string]any{"template": "voice-assistant", "base_digest": m.Digest}, &res)
	if res.Result != applyUnchanged || len(res.Versions) != 1 || res.Summary.Digest != m.Digest {
		t.Errorf("same template again = %s, %d versions", res.Result, len(res.Versions))
	}
	h.ok(http.MethodPost, path, map[string]any{"template": "strict", "base_digest": m.Digest}, &res)
	if res.Result != applyUpdated || len(res.Versions) != 2 || res.Summary.MaxActionsPerHour != 5 {
		t.Errorf("strict = %s %+v", res.Result, res.Summary)
	}
	strictDigest := h.templateDigest("strict")
	if v := res.Versions[0]; v.Origin != "template" || *v.Template != "strict" || *v.TemplateDigest != strictDigest {
		t.Errorf("origin = %+v", v)
	}
	// Twice: exactly one version, and the answer says nothing changed.
	base := res.Summary.Digest
	h.ok(http.MethodPost, path, map[string]any{"template": "strict", "base_digest": base}, &res)
	if res.Result != applyUnchanged || len(res.Versions) != 2 || res.Summary.Digest != base {
		t.Errorf("strict twice = %s, %d versions", res.Result, len(res.Versions))
	}
	if n := h.countEvents(audit.EventMandateUpdated) - updated; n != 1 {
		t.Errorf("mandate.updated entries = %d, want 1", n)
	}
	// Another administrator applying it changes only the metadata: still unchanged.
	h.ok(http.MethodPost, path, map[string]any{"template": "strict", "base_digest": base}, &res, as(annaID))
	if res.Result != applyUnchanged {
		t.Errorf("strict by another admin = %s", res.Result)
	}
	// An edit shows as one, and the mandate was edited since the template.
	edited := currentDraft(t, h, m.ID)
	edited["limits"] = map[string]any{"max_actions_per_hour": 6}
	var d wireMandateDetail
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", edited, base, false), &d)
	if v := d.Versions[0]; v.Origin != "edit" || v.Template != nil || v.TemplateDigest != nil {
		t.Errorf("origin of an edit = %+v", v)
	}
	if rf := d.Summary.RulesFrom; rf == nil || rf.Template != "strict" || !rf.EditedSince {
		t.Errorf("rules from after an edit = %+v", rf)
	}
	var list []wireMandateSummary
	h.ok(http.MethodGet, "/api/mandates", nil, &list)
	if len(list) != 1 || list[0].RulesFrom == nil || !list[0].RulesFrom.EditedSince {
		t.Errorf("list = %+v", list)
	}
	var agents []wireAgent
	h.ok(http.MethodGet, "/api/agents", nil, &agents)
	if len(agents) != 1 || agents[0].Mandate == nil || agents[0].Mandate.RulesFrom == nil || agents[0].Mandate.RulesFrom.Template != "strict" {
		t.Errorf("agents = %+v", agents)
	}
}

// Applying a template can rename the mandate in the same request; a rename alone stores
// no version.
func TestApplyingATemplateCanRename(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	path := "/api/mandates/" + m.ID + "/apply-template"
	h.putTemplate("strict", func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 5} })
	for _, name := range []any{"", "\u202e", strings.Repeat("n", 81), 7} {
		if r := h.do(http.MethodPost, path, map[string]any{"template": "strict", "base_digest": m.Digest, "name": name}); r.errCode() != codeInvalidInput || r.field() != "/name" {
			t.Errorf("name %v = %d %s", name, r.code, r.body)
		}
	}
	var res wireApplyResult
	h.ok(http.MethodPost, path, map[string]any{"template": "voice-assistant", "base_digest": m.Digest, "name": " Wohnzimmer "}, &res)
	if res.Result != applyUnchanged || res.Summary.Name != "Wohnzimmer" || len(res.Versions) != 1 {
		t.Errorf("rename with an unchanged template = %s %+v", res.Result, res.Summary)
	}
	h.ok(http.MethodPost, path, map[string]any{"template": "strict", "base_digest": m.Digest, "name": "Streng"}, &res)
	if res.Result != applyUpdated || res.Summary.Name != "Streng" || len(res.Versions) != 2 {
		t.Errorf("rename with a change = %s %+v", res.Result, res.Summary)
	}
	// A refused change keeps the name.
	if r := h.do(http.MethodPost, path, map[string]any{"template": "voice-assistant", "base_digest": m.Digest, "name": "Lost"}); r.errCode() != codeConflict {
		t.Errorf("old base = %d %s", r.code, r.body)
	}
	if got, _ := h.mandates.Get(t.Context(), m.ID); got.Name != "Streng" {
		t.Errorf("name after a refused change = %q", got.Name)
	}
}

// Versions stored before origins were kept show none.
func TestVersionsOfBeforeHaveNoOrigin(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	if _, err := h.st.DB().Exec(`UPDATE mandate_versions SET origin = 'unknown', template_name = '', template_digest = ''`); err != nil {
		t.Fatal(err)
	}
	var d wireMandateDetail
	h.ok(http.MethodGet, "/api/mandates/"+m.ID, nil, &d)
	if v := d.Versions[0]; v.Origin != "unknown" || v.Template != nil || d.Summary.RulesFrom != nil {
		t.Errorf("old version = %+v, rules from %+v", v, d.Summary.RulesFrom)
	}
}
