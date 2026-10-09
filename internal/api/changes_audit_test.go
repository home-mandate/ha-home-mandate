// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// changeEntries returns the entries of an event in the audit log, oldest first, as the
// UI gets them.
func (h *harness) changeEntries(event string) []map[string]any {
	h.t.Helper()
	page, r := h.audit("?event=" + event)
	if r.code != http.StatusOK {
		h.t.Fatalf("audit = %d %s", r.code, r.body)
	}
	out := make([]map[string]any, 0, len(page.Entries))
	for i := len(page.Entries) - 1; i >= 0; i-- {
		out = append(out, page.Entries[i])
	}
	return out
}

func actorID(e map[string]any) string {
	actor, _ := e["actor"].(map[string]any)
	id, _ := actor["id"].(string)
	return id
}

// Every template change in the UI is one template.changed entry with the signed-in human
// as actor; refused and unchanged saves record nothing.
func TestTemplateChangesInTheUIAreAudited(t *testing.T) {
	h := newHarness(t)
	var tmpl wireTemplate
	h.ok(http.MethodGet, "/api/templates/voice-assistant", nil, &tmpl)
	draft := json.RawMessage(tmpl.Draft)
	var stored wireTemplate
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": draft}, &stored, as(annaID))
	// Unchanged: nothing stored, nothing recorded.
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": draft, "base_digest": stored.Digest}, nil)
	// Refused: a stale base, a template the household cannot hide, a base template.
	for _, r := range []result{
		h.do(http.MethodPut, "/api/templates/garden", map[string]any{"draft": draft, "base_digest": tmpl.Digest}),
		h.do(http.MethodPut, "/api/templates/garden/hidden", map[string]any{"hidden": true}),
		h.do(http.MethodDelete, "/api/templates/hm-read-only", nil),
		h.do(http.MethodDelete, "/api/templates/missing", nil),
	} {
		if r.code < 400 {
			t.Errorf("accepted: %d %s", r.code, r.body)
		}
	}
	var edited map[string]any
	_ = json.Unmarshal(draft, &edited)
	edited["limits"] = map[string]any{"max_actions_per_hour": 7}
	var changed wireTemplate
	h.ok(http.MethodPut, "/api/templates/garden", map[string]any{"draft": edited, "base_digest": stored.Digest}, &changed)
	h.ok(http.MethodDelete, "/api/templates/garden", nil, nil, as(annaID))
	h.ok(http.MethodPut, "/api/templates/hm-read-only/hidden", map[string]any{"hidden": true}, nil)
	h.ok(http.MethodPut, "/api/templates/hm-read-only/hidden", map[string]any{"hidden": true}, nil) // already hidden
	h.ok(http.MethodPut, "/api/templates/hm-read-only/hidden", map[string]any{"hidden": false}, nil, as(annaID))

	entries := h.changeEntries("template.changed")
	want := []struct{ actor, change, name, digest, previous string }{
		{annaID, "stored", "garden", stored.Digest, ""},
		{adminID, "stored", "garden", changed.Digest, stored.Digest},
		{annaID, "removed", "garden", changed.Digest, ""},
		{adminID, "hidden", "hm-read-only", "", ""},
		{annaID, "shown", "hm-read-only", "", ""},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v", entries)
	}
	for i, w := range want {
		e := entries[i]
		member, _ := e["template"].(map[string]any)
		if actorID(e) != w.actor || member["change"] != w.change || member["name"] != w.name ||
			w.digest != "" && member["digest"] != w.digest || (w.previous == "") != (member["previous_digest"] == nil) ||
			w.previous != "" && member["previous_digest"] != w.previous {
			t.Errorf("entry %d = %v, want %+v", i, e, w)
		}
	}
	var v wireVerification
	h.ok(http.MethodPost, "/api/audit/verify", nil, &v)
	if !v.Valid || v.Checked != len(want) {
		t.Errorf("verification = %+v", v)
	}
}

// Adding and removing an approver in the UI is one approver.changed entry each with the
// signed-in human as actor; the UI gets the approver's name. Other channels for someone
// who is an approver already, and refused changes, record nothing.
func TestApproverChangesInTheUIAreAudited(t *testing.T) {
	h := newHarness(t)
	h.ok(http.MethodPut, "/api/approvers/"+annaID, putBody(nil, true, false, nil), nil)
	h.ok(http.MethodPut, "/api/approvers/"+annaID, putBody(nil, true, true, nil), nil, as(annaID))
	if r := h.do(http.MethodPut, "/api/approvers/"+annaID, map[string]any{"devices": nil, "ui": true, "base_version": "0123456789abcdef0123456789abcdef"}); r.errCode() != codeConflict {
		t.Errorf("stale change = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodDelete, "/api/approvers/"+adminID, nil); r.errCode() != codeNotFound {
		t.Errorf("removing nobody = %d %s", r.code, r.body)
	}
	h.ok(http.MethodDelete, "/api/approvers/"+annaID, nil, nil, as(annaID))

	entries := h.changeEntries("approver.changed")
	want := []struct{ actor, change string }{{adminID, "added"}, {annaID, "removed"}}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v", entries)
	}
	for i, w := range want {
		member, _ := entries[i]["approver"].(map[string]any)
		if actorID(entries[i]) != w.actor || member["change"] != w.change || member["id"] != annaID || member["name"] != "Anna" {
			t.Errorf("entry %d = %v, want %+v", i, entries[i], w)
		}
	}
	// Both events are filters of the administrative group.
	page, _ := h.audit("?group=admin")
	if page.Total != 2 {
		t.Errorf("admin entries = %d", page.Total)
	}
}
