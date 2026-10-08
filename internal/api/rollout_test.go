// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// saveTemplate changes the template "voice-assistant" through the API, as the template
// editor does, and returns its new digest.
func (h *harness) saveTemplate(edit func(map[string]any), confirm bool) string {
	h.t.Helper()
	var current wireTemplate
	h.ok(http.MethodGet, "/api/templates/voice-assistant", nil, &current)
	var d map[string]any
	_ = json.Unmarshal(current.Draft, &d)
	edit(d)
	body := map[string]any{"draft": d, "base_digest": current.Digest}
	if confirm {
		body["confirm_critical"] = true
	}
	var stored wireTemplate
	h.ok(http.MethodPut, "/api/templates/voice-assistant", body, &stored)
	return stored.Digest
}

func (h *harness) usage(name string) wireTemplateUsage {
	h.t.Helper()
	var u wireTemplateUsage
	h.ok(http.MethodGet, "/api/templates/"+name+"/usage", nil, &u)
	return u
}

func (h *harness) versions(id string) int {
	h.t.Helper()
	v, err := h.mandates.Versions(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return len(v)
}

func target(m wireTemplateUser) map[string]any {
	return map[string]any{"mandate_id": m.MandateID, "base_digest": m.Digest}
}

func outcomes(r wireRollout) map[string]string {
	out := map[string]string{}
	for _, o := range r.Results {
		out[o.MandateID] = o.Result
	}
	return out
}

func limitTo(n int) func(map[string]any) {
	return func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": n} }
}

// The mandates whose rules were last taken from a template: active ones of active agents,
// marked when edited since, newest template digest compared.
func TestTemplateUsage(t *testing.T) {
	h := newHarness(t)
	if u := h.usage("voice-assistant"); len(u.Mandates) != 0 || u.Name != "voice-assistant" || u.Digest != h.templateDigest("voice-assistant") {
		t.Fatalf("usage of an unused template = %+v", u)
	}
	a, b, c, d := h.admit("Anna"), h.admit("Bob"), h.admit("Carl"), h.admit("Dora")
	mb := h.mandateOf(b.ClientID)
	edited := currentDraft(t, h, mb.ID)
	edited["limits"] = map[string]any{"max_actions_per_hour": 9}
	h.ok(http.MethodPut, "/api/mandates/"+mb.ID, update("Bob", edited, mb.Digest, false), nil)
	h.ok(http.MethodPost, "/api/mandates/"+h.mandateOf(c.ClientID).ID+"/revoke", nil, nil)
	h.ok(http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": d.ClientID}, nil)
	h.putTemplate("strict", limitTo(5))
	e := h.admit("Emil")
	me := h.mandateOf(e.ClientID)
	h.ok(http.MethodPost, "/api/mandates/"+me.ID+"/apply-template", map[string]any{"template": "strict", "base_digest": me.Digest}, nil)

	u := h.usage("voice-assistant")
	if len(u.Mandates) != 2 {
		t.Fatalf("usage = %+v", u)
	}
	byAgent := map[string]wireTemplateUser{}
	for _, m := range u.Mandates {
		byAgent[m.AgentDisplayName] = m
	}
	ma := h.mandateOf(a.ClientID)
	if got := byAgent["Anna"]; got.MandateID != ma.ID || got.MandateName != "Anna" || got.ClientID != a.ClientID || got.Digest != ma.Digest ||
		got.EditedSince || !got.UpToDate || got.TemplateDigest != u.Digest || got.TakenAt == "" {
		t.Errorf("Anna = %+v", got)
	}
	if got := byAgent["Bob"]; !got.EditedSince || got.UpToDate {
		t.Errorf("Bob (edited since) = %+v", got)
	}
	// After the template changed, nobody is up to date.
	h.saveTemplate(limitTo(30), false)
	for _, m := range h.usage("voice-assistant").Mandates {
		if m.UpToDate {
			t.Errorf("up to date after a change of the template: %+v", m)
		}
	}
	if u := h.usage("strict"); len(u.Mandates) != 1 || u.Mandates[0].MandateID != me.ID {
		t.Errorf("usage of strict = %+v", u)
	}
	if u := h.usage("hm-read-only"); len(u.Mandates) != 0 {
		t.Errorf("usage of a base template = %+v", u)
	}
	for _, path := range []string{"/api/templates/none/usage", "/api/templates/Bad_Name/usage"} {
		if r := h.do(http.MethodGet, path, nil); r.errCode() != codeNotFound {
			t.Errorf("%s = %d %s", path, r.code, r.body)
		}
	}
	if r := h.do(http.MethodGet, "/api/templates/voice-assistant/usage", nil, as(guestID)); r.errCode() != codeForbidden {
		t.Errorf("usage for a non-administrator = %d", r.code)
	}
}

// Only the template: saving it changes no mandate.
func TestSavingATemplateChangesNoMandate(t *testing.T) {
	h := newHarness(t)
	a := h.admit("Anna")
	before := h.mandateOf(a.ClientID)
	h.saveTemplate(limitTo(7), false)
	if after := h.mandateOf(a.ClientID); after.Digest != before.Digest || h.versions(before.ID) != 1 {
		t.Errorf("mandate after saving its template = %+v", after)
	}
}

// Exactly the selected mandates take over the changed template: new version with its
// rules, approval settings and limits, name and validity kept, origin updated, one audit
// entry each with the human as actor. A mandate changed meanwhile is a conflict alone.
func TestApplyAChangedTemplateToItsMandates(t *testing.T) {
	h := newHarness(t)
	a, b, c, d := h.admit("Anna"), h.admit("Bob"), h.admit("Carl"), h.admit("Dora")
	digest := h.saveTemplate(limitTo(7), false)
	users, byID := map[string]wireTemplateUser{}, map[string]wireTemplateUser{}
	for _, m := range h.usage("voice-assistant").Mandates {
		users[m.AgentDisplayName], byID[m.MandateID] = m, m
	}
	// Carl's mandate changes after the human saw the list.
	mc := h.mandateOf(c.ClientID)
	edited := currentDraft(t, h, mc.ID)
	edited["limits"] = map[string]any{"max_actions_per_hour": 3}
	h.ok(http.MethodPut, "/api/mandates/"+mc.ID, update("Carl", edited, mc.Digest, false), nil)
	ma, mb := h.mandateOf(a.ClientID), h.mandateOf(b.ClientID)
	_, docA, _ := h.mandates.Current(t.Context(), ma.ID)
	updated := h.countEvents(audit.EventMandateUpdated)

	var res wireRollout
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", map[string]any{"template_digest": digest,
		"targets": []any{target(users["Anna"]), target(users["Bob"]), target(users["Carl"])}}, &res)
	want := map[string]string{ma.ID: rolloutUpdated, mb.ID: rolloutUpdated, mc.ID: rolloutConflict}
	if got := outcomes(res); len(got) != 3 || got[ma.ID] != want[ma.ID] || got[mb.ID] != want[mb.ID] || got[mc.ID] != want[mc.ID] {
		t.Fatalf("results = %+v", res.Results)
	}
	for _, o := range res.Results {
		if o.Result == rolloutUpdated && (o.Digest == nil || *o.Digest == byID[o.MandateID].Digest) {
			t.Errorf("updated without its new digest: %+v", o)
		}
	}
	if n := h.countEvents(audit.EventMandateUpdated) - updated; n != 2 {
		t.Errorf("mandate.updated entries = %d, want 2", n)
	}
	var actors []string
	rows, err := h.st.DB().Query(`SELECT json_extract(entry, '$.actor.id') FROM audit_log WHERE event = ? ORDER BY seq DESC LIMIT 2`, audit.EventMandateUpdated)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		actors = append(actors, id)
	}
	_ = rows.Close()
	if len(actors) != 2 || actors[0] != adminID || actors[1] != adminID {
		t.Errorf("actors = %v", actors)
	}
	after, docAfter, _ := h.mandates.Current(t.Context(), ma.ID)
	var before, now map[string]any
	_ = json.Unmarshal(docA, &before)
	_ = json.Unmarshal(docAfter, &now)
	if after.Name != "Anna" || after.MaxActionsPerHour != 7 || now["valid_from"] != before["valid_from"] || h.versions(ma.ID) != 2 {
		t.Errorf("Anna after = %+v %v", after, now)
	}
	v, _ := h.mandates.Versions(t.Context(), ma.ID)
	if o := v[len(v)-1].Origin; o.Template != "voice-assistant" || o.TemplateDigest != digest {
		t.Errorf("origin = %+v", o)
	}
	// Dora was not selected: unchanged.
	if h.versions(h.mandateOf(d.ClientID).ID) != 1 {
		t.Error("an unselected mandate changed")
	}
	// Carl again, now on the version he has: updated.
	carl := h.mandateOf(c.ClientID)
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", map[string]any{"template_digest": digest,
		"targets": []any{map[string]any{"mandate_id": carl.ID, "base_digest": carl.Digest}}}, &res)
	if got := outcomes(res); got[carl.ID] != rolloutUpdated {
		t.Errorf("retry = %+v", res.Results)
	}
	// Anna already has it: unchanged, no version.
	anna := h.mandateOf(a.ClientID)
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", map[string]any{"template_digest": digest,
		"targets": []any{map[string]any{"mandate_id": anna.ID, "base_digest": anna.Digest}}}, &res)
	if got := outcomes(res); got[anna.ID] != rolloutUnchanged || h.versions(anna.ID) != 2 {
		t.Errorf("unchanged = %+v, %d versions", res.Results, h.versions(anna.ID))
	}
}

// Revoked mandates and agents, unknown mandates: refused one by one, the rest goes on.
func TestApplyATemplateRefusesRevokedAndUnknownMandates(t *testing.T) {
	h := newHarness(t)
	a, b, c := h.admit("Anna"), h.admit("Bob"), h.admit("Carl")
	digest := h.saveTemplate(limitTo(7), false)
	ma, mb, mc := h.mandateOf(a.ClientID), h.mandateOf(b.ClientID), h.mandateOf(c.ClientID)
	h.ok(http.MethodPost, "/api/mandates/"+mb.ID+"/revoke", nil, nil)
	if err := h.agents.Revoke(t.Context(), c.ClientID, audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		t.Fatal(err)
	}
	var res wireRollout
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", map[string]any{"template_digest": digest, "targets": []any{
		map[string]any{"mandate_id": ma.ID, "base_digest": ma.Digest},
		map[string]any{"mandate_id": mb.ID, "base_digest": mb.Digest},
		map[string]any{"mandate_id": mc.ID, "base_digest": mc.Digest},
		map[string]any{"mandate_id": "m-nobody", "base_digest": ma.Digest},
	}}, &res)
	got := outcomes(res)
	if got[ma.ID] != rolloutUpdated || got[mb.ID] != rolloutRevoked || got[mc.ID] != rolloutRevoked || got["m-nobody"] != rolloutNotFound {
		t.Errorf("results = %+v", res.Results)
	}
	if res.Results[0].MandateID != ma.ID || res.Results[3].MandateID != "m-nobody" {
		t.Errorf("results not in the order of the targets: %+v", res.Results)
	}
	if a, _ := h.agents.Get(t.Context(), c.ClientID); a.Status != agent.StatusRevoked || h.versions(mc.ID) != 1 {
		t.Errorf("revoked agent's mandate changed")
	}
}

// One separate confirmation covers every selected mandate that would gain a rule allowing
// critical actions without approval; without it, no mandate is updated.
func TestApplyingACriticalTemplateNeedsOneConfirmation(t *testing.T) {
	h := newHarness(t)
	a, b := h.admit("Anna"), h.admit("Bob")
	digest := h.saveTemplate(func(d map[string]any) { d["rules"] = append(d["rules"].([]any), criticalRule()) }, true)
	ma, mb := h.mandateOf(a.ClientID), h.mandateOf(b.ClientID)
	body := map[string]any{"template_digest": digest, "targets": []any{
		map[string]any{"mandate_id": ma.ID, "base_digest": ma.Digest},
		map[string]any{"mandate_id": mb.ID, "base_digest": mb.Digest},
	}}
	updated := h.countEvents(audit.EventMandateUpdated)
	if r := h.do(http.MethodPost, "/api/templates/voice-assistant/apply", body); r.code != http.StatusUnprocessableEntity || r.errCode() != codeCriticalConfirm {
		t.Fatalf("without confirmation = %d %s", r.code, r.body)
	}
	if h.versions(ma.ID) != 1 || h.versions(mb.ID) != 1 || h.countEvents(audit.EventMandateUpdated) != updated {
		t.Fatal("a mandate was updated without the confirmation")
	}
	body["confirm_critical"] = true
	var res wireRollout
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", body, &res)
	if got := outcomes(res); got[ma.ID] != rolloutUpdated || got[mb.ID] != rolloutUpdated {
		t.Errorf("confirmed = %+v", res.Results)
	}
}

func TestApplyATemplateRefusesBadRequests(t *testing.T) {
	h := newHarness(t)
	a := h.admit("Anna")
	digest := h.saveTemplate(limitTo(7), false)
	m := h.mandateOf(a.ClientID)
	one := []any{map[string]any{"mandate_id": m.ID, "base_digest": m.Digest}}
	many := make([]any, maxRolloutTargets+1)
	for i := range many {
		many[i] = map[string]any{"mandate_id": "m-" + strings.Repeat("x", 4) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), "base_digest": m.Digest}
	}
	path := "/api/templates/voice-assistant/apply"
	for _, tc := range []struct {
		name  string
		path  string
		body  any
		code  string
		field string
	}{
		{"no targets", path, map[string]any{"template_digest": digest, "targets": []any{}}, codeInvalidInput, "/targets"},
		{"missing targets", path, map[string]any{"template_digest": digest}, codeInvalidInput, "/targets"},
		{"too many targets", path, map[string]any{"template_digest": digest, "targets": many}, codeInvalidInput, "/targets"},
		{"twice the same mandate", path, map[string]any{"template_digest": digest, "targets": append(one, one[0])}, codeInvalidInput, "/targets/1/mandate_id"},
		{"bad mandate id", path, map[string]any{"template_digest": digest, "targets": []any{map[string]any{"mandate_id": "a/b", "base_digest": m.Digest}}}, codeInvalidInput, "/targets/0/mandate_id"},
		{"bad base digest", path, map[string]any{"template_digest": digest, "targets": []any{map[string]any{"mandate_id": m.ID, "base_digest": "x"}}}, codeInvalidInput, "/targets/0/base_digest"},
		{"bad template digest", path, map[string]any{"template_digest": "md5:x", "targets": one}, codeInvalidInput, "/template_digest"},
		{"unknown field", path, map[string]any{"template_digest": digest, "targets": one, "all": true}, codeInvalidInput, "/all"},
		{"template changed since", path, map[string]any{"template_digest": "sha256:" + strings.Repeat("0", 64), "targets": one}, codeConflict, ""},
		{"unknown template", "/api/templates/none/apply", map[string]any{"template_digest": digest, "targets": one}, codeNotFound, ""},
		{"malformed template name", "/api/templates/Bad_Name/apply", map[string]any{"template_digest": digest, "targets": one}, codeNotFound, ""},
	} {
		if r := h.do(http.MethodPost, tc.path, tc.body); r.errCode() != tc.code || r.field() != tc.field {
			t.Errorf("%s = %d %s", tc.name, r.code, r.body)
		}
	}
	body := map[string]any{"template_digest": digest, "targets": one}
	for name, tc := range map[string]struct {
		opts []reqOpt
		code string
	}{
		"no CSRF token":      {[]reqOpt{header("X-HM-CSRF", "")}, codeCSRF},
		"other site":         {[]reqOpt{header("Sec-Fetch-Site", "cross-site")}, codeForbidden},
		"no administrator":   {[]reqOpt{as(guestID)}, codeForbidden},
		"not the supervisor": {[]reqOpt{from("192.168.1.9:4000")}, ""}, // an empty 403
	} {
		if r := h.do(http.MethodPost, path, body, tc.opts...); r.code != http.StatusForbidden && r.code != http.StatusUnauthorized || r.errCode() != tc.code {
			t.Errorf("%s = %d %s", name, r.code, r.body)
		}
	}
	if h.versions(m.ID) != 1 {
		t.Error("a refused request changed the mandate")
	}
	// A hidden base template is refused as when applying it to one mandate.
	h.ok(http.MethodPut, "/api/templates/hm-read-only/hidden", map[string]any{"hidden": true}, nil)
	_, base, _ := h.adm.TemplateDocument(t.Context(), "hm-read-only")
	if r := h.do(http.MethodPost, "/api/templates/hm-read-only/apply", map[string]any{"template_digest": base.Digest, "targets": one}); r.errCode() != codeNotFound {
		t.Errorf("hidden base template = %d %s", r.code, r.body)
	}
}

// A mandate that cannot be stored fails alone: the others are updated, and a mandate
// revoked between the check and the change is reported as revoked.
func TestApplyATemplateFailsOneMandateAlone(t *testing.T) {
	h := newHarness(t)
	a, b, c := h.admit("Anna"), h.admit("Bob"), h.admit("Carl")
	digest := h.saveTemplate(limitTo(7), false)
	ma, mb, mc := h.mandateOf(a.ClientID), h.mandateOf(b.ClientID), h.mandateOf(c.ClientID)
	// Storing Bob's version fails; storing Anna's revokes Carl's mandate meanwhile.
	for _, stmt := range []string{
		`CREATE TRIGGER fail_bob BEFORE INSERT ON mandate_versions WHEN NEW.mandate_id = '` + mb.ID + `' BEGIN SELECT RAISE(ABORT, 'disk full'); END`,
		`CREATE TRIGGER revoke_carl AFTER INSERT ON mandate_versions WHEN NEW.mandate_id = '` + ma.ID + `'
			BEGIN UPDATE mandates SET status = 'revoked' WHERE id = '` + mc.ID + `'; END`,
	} {
		if _, err := h.st.DB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var res wireRollout
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", map[string]any{"template_digest": digest, "targets": []any{
		map[string]any{"mandate_id": ma.ID, "base_digest": ma.Digest},
		map[string]any{"mandate_id": mb.ID, "base_digest": mb.Digest},
		map[string]any{"mandate_id": mc.ID, "base_digest": mc.Digest},
	}}, &res)
	got := outcomes(res)
	if got[ma.ID] != rolloutUpdated || got[mb.ID] != rolloutFailed || got[mc.ID] != rolloutRevoked {
		t.Errorf("results = %+v", res.Results)
	}
	if h.versions(ma.ID) != 2 || h.versions(mb.ID) != 1 || h.versions(mc.ID) != 1 {
		t.Errorf("versions: %d %d %d", h.versions(ma.ID), h.versions(mb.ID), h.versions(mc.ID))
	}
}

// A mandate that changed between the check and the change is a conflict.
func TestApplyATemplateReportsAChangeMeanwhile(t *testing.T) {
	h := newHarness(t)
	a, b := h.admit("Anna"), h.admit("Bob")
	digest := h.saveTemplate(limitTo(7), false)
	ma, mb := h.mandateOf(a.ClientID), h.mandateOf(b.ClientID)
	// Storing Anna's version also gives Bob's mandate another current version.
	other := strings.Replace(mb.Digest, mb.Digest[len(mb.Digest)-4:], "0000", 1)
	for _, stmt := range []string{
		`INSERT INTO mandate_versions (mandate_id, digest, document, created_at, created_by, origin) SELECT mandate_id, '` + other + `', document, created_at, created_by, 'edit' FROM mandate_versions WHERE mandate_id = '` + mb.ID + `'`,
		`CREATE TRIGGER change_bob AFTER INSERT ON mandate_versions WHEN NEW.mandate_id = '` + ma.ID + `'
			BEGIN UPDATE mandates SET current_digest = '` + other + `' WHERE id = '` + mb.ID + `'; END`,
	} {
		if _, err := h.st.DB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var res wireRollout
	h.ok(http.MethodPost, "/api/templates/voice-assistant/apply", map[string]any{"template_digest": digest, "targets": []any{
		map[string]any{"mandate_id": ma.ID, "base_digest": ma.Digest},
		map[string]any{"mandate_id": mb.ID, "base_digest": mb.Digest},
	}}, &res)
	if got := outcomes(res); got[ma.ID] != rolloutUpdated || got[mb.ID] != rolloutConflict {
		t.Errorf("results = %+v", res.Results)
	}
}
