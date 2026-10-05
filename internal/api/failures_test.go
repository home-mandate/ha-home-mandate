// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/approval"
)

func TestNewFillsDefaults(t *testing.T) {
	s := New(Config{})
	if s.cfg.Logger == nil || s.cfg.Now == nil || s.cfg.Status().HAConnected || s.cfg.Status().Units != nil {
		t.Error("defaults missing")
	}
	if present, until := s.cfg.TLS(); present || !until.IsZero() {
		t.Error("TLS default")
	}
	if admin, err := s.IsAdmin(context.Background(), adminID); admin || err == nil {
		t.Errorf("without Home Assistant: %v, %v", admin, err)
	}
	if (&apiError{code: codeConflict, field: "/x"}).Error() != "api: conflict /x" {
		t.Error("error text")
	}
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"x": make(chan int)})
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal") {
		t.Errorf("unencodable = %d %s", rec.Code, rec.Body)
	}
}

// Every endpoint reports a failing database as internal, without details, and changes
// nothing it could not finish.
func TestEndpointsReportDatabaseErrors(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	same := currentDraft(t, h, m.ID)
	_ = h.st.Close()
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/session", nil},
		{http.MethodPut, "/api/session/language", map[string]any{"language": "de"}},
		{http.MethodPut, "/api/session/language", map[string]any{"language": nil}},
		{http.MethodGet, "/api/system", nil},
		{http.MethodGet, "/api/agents", nil},
		{http.MethodPost, "/api/agents/revoke", map[string]any{"client_id": voice.ClientID}},
		{http.MethodGet, "/api/mandates", nil},
		{http.MethodGet, "/api/mandates/" + m.ID, nil},
		{http.MethodGet, "/api/mandates/" + m.ID + "/versions/1", nil},
		{http.MethodPut, "/api/mandates/" + m.ID, update("X", same, m.Digest, false)},
		{http.MethodPost, "/api/mandates/" + m.ID + "/apply-template", map[string]any{"template": "voice-assistant", "base_digest": m.Digest}},
		{http.MethodPost, "/api/mandates/" + m.ID + "/revoke", nil},
		{http.MethodPost, "/api/mandates", map[string]any{"client_id": voice.ClientID, "template": "voice-assistant"}},
		{http.MethodGet, "/api/templates", nil},
		{http.MethodGet, "/api/templates/voice-assistant", nil},
		{http.MethodPut, "/api/templates/garden", map[string]any{"draft": draft(t)}},
		{http.MethodDelete, "/api/templates/voice-assistant", nil},
		{http.MethodGet, "/api/settings", nil},
		{http.MethodPut, "/api/settings", map[string]any{"approval_timeout": "PT1M", "max_actions_per_hour": 1, "bell": false}},
		{http.MethodGet, "/api/approvals", nil},
		{http.MethodGet, "/api/audit", nil},
		{http.MethodPost, "/api/audit/verify", nil},
		{http.MethodGet, "/api/approvers", nil},
		{http.MethodPut, "/api/approvers/" + annaID, putBody(nil, true, false, nil)},
		{http.MethodDelete, "/api/approvers/" + adminID, nil},
		{http.MethodPost, "/api/approvers/" + adminID + "/test", nil},
		{http.MethodPut, "/api/emergency-stop", map[string]any{"active": true}},
	} {
		r := h.do(tc.method, tc.path, tc.body)
		if r.code != http.StatusInternalServerError || r.errCode() != codeInternal {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, r.code, r.body)
		}
	}
	if err := h.srv.LoadSettings(context.Background()); err == nil {
		t.Error("LoadSettings succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.srv.RunTail(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	h.srv.ApprovalOpened(approval.Open{ID: "x"}) // approvers unreadable: announced without an answer in the UI
}

// What fails only after a part succeeded, made to fail with triggers.
func TestPartialFailures(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	exec := func(stmt string) {
		t.Helper()
		if _, err := h.st.DB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	// The versions of a mandate cannot be read: list, detail and audit entries with it.
	exec(`ALTER TABLE mandate_versions RENAME TO mandate_versions_gone`)
	for _, path := range []string{"/api/mandates", "/api/mandates/" + m.ID} {
		if r := h.do(http.MethodGet, path, nil); r.code != http.StatusInternalServerError {
			t.Errorf("%s = %d", path, r.code)
		}
	}
	if p, r := h.audit(""); r.code != http.StatusOK || len(p.Entries) == 0 {
		t.Errorf("audit without versions = %d", r.code) // the version number is left out, not the entry
	}
	exec(`ALTER TABLE mandate_versions_gone RENAME TO mandate_versions`)
	// Templates whose document cannot be read.
	exec(`UPDATE mandate_templates SET document = 'not json'`)
	for _, path := range []string{"/api/templates", "/api/templates/voice-assistant"} {
		if r := h.do(http.MethodGet, path, nil); r.code != http.StatusInternalServerError {
			t.Errorf("%s = %d", path, r.code)
		}
	}
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/apply-template", map[string]any{"template": "voice-assistant", "base_digest": m.Digest}); r.code != http.StatusInternalServerError {
		t.Errorf("apply a broken template = %d %s", r.code, r.body)
	}
	// A stored version that is no JSON object.
	exec(`UPDATE mandate_versions SET document = '[]'`)
	if r := h.do(http.MethodGet, "/api/mandates/"+m.ID, nil); r.code != http.StatusInternalServerError {
		t.Errorf("broken version = %d", r.code)
	}
	// The approvers cannot be read: the open requests cannot be shown.
	exec(`ALTER TABLE approver_devices RENAME TO approver_devices_gone`)
	for _, path := range []string{"/api/approvals", "/api/system"} {
		if r := h.do(http.MethodGet, path, nil); r.code != http.StatusInternalServerError {
			t.Errorf("%s = %d", path, r.code)
		}
	}
	exec(`ALTER TABLE approver_devices_gone RENAME TO approver_devices`)
	h.ok(http.MethodGet, "/api/approvals", nil, nil)
}

// A mandate without a name (imported on the command line) shows its ID.
func TestMandateWithoutName(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	if _, err := h.st.DB().Exec(`UPDATE mandates SET name = ''`); err != nil {
		t.Fatal(err)
	}
	var list []wireMandateSummary
	h.ok(http.MethodGet, "/api/mandates", nil, &list)
	if list[0].Name != m.ID {
		t.Errorf("name = %q", list[0].Name)
	}
}

func TestReviewHardening(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	// A template without limits cannot be applied (it would leave a mandate without them).
	broken := voiceTemplate(t, func(d map[string]any) { delete(d, "limits") })
	if _, err := h.st.DB().Exec(`INSERT INTO mandate_templates (name, document, created_at, created_by) VALUES ('nolimits', ?, 't', 'x')`, string(broken)); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/apply-template", map[string]any{"template": "nolimits", "base_digest": m.Digest}); r.errCode() != codeInvalidMandate || r.field() != "/template" {
		t.Errorf("template without limits = %d %s", r.code, r.body)
	}
	// Too slow: unavailable, not internal.
	slow := h.srv.wrap(noBody, func(*request) (any, error) { return nil, context.DeadlineExceeded })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/slow", nil)
	req.Header.Set("X-Remote-User-Id", adminID)
	slow.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("deadline = %d %s", rec.Code, rec.Body)
	}
	// Foreign requests are logged at most once a minute.
	for range 3 {
		_ = h.do(http.MethodGet, "/", nil, from("10.0.0.1:1"))
	}
	if ok, _ := h.srv.limits.allow("log:foreign", 1, time.Minute); ok {
		t.Error("foreign requests not rate limited in the log")
	}
	// An event too large for the stream closes the connection (the UI reloads).
	c, _ := h.srv.hub.add(adminID, "")
	h.srv.publish(event{Type: "approval.closed", ID: strings.Repeat("x", maxEventBytes)})
	select {
	case <-c.overflow:
	default:
		t.Error("oversized event did not close the connection")
	}
	// After a change is done, the agent is answered even if its extras cannot be read.
	if _, err := h.st.DB().Exec(`ALTER TABLE mandates RENAME TO mandates_gone`); err != nil {
		t.Fatal(err)
	}
	a, _ := h.agents.Get(context.Background(), voice.ClientID)
	if w := h.srv.presentAgent(context.Background(), a); w.ClientID != voice.ClientID || w.Mandate != nil || w.RedirectURIs == nil {
		t.Errorf("fallback = %+v", w)
	}
}

// The tail starts at the end of the log once it can be read; it never replays the log.
func TestTailStartsAtTheEnd(t *testing.T) {
	h := newHarness(t)
	h.admit("Voice")
	old := tailEvery
	tailEvery = 5 * time.Millisecond
	defer func() { tailEvery = old }()
	c, _ := h.srv.hub.add(adminID, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.srv.RunTail(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case data := <-c.send:
		t.Errorf("replayed: %s", data)
	default:
	}
	h.admit("Other")
	select {
	case data := <-c.send:
		if !strings.Contains(string(data), "audit.appended") {
			t.Errorf("first event = %s", data)
		}
	case <-time.After(5 * time.Second):
		t.Error("new entries not announced")
	}
	cancel()
	<-done
}
