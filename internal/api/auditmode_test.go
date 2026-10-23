// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// Switching audit mode on in the editor keeps the new version waiting; it takes effect
// only after a human confirmed exactly its digest, and the approvers of the mandate are
// told both times (SPEC-v0 section 11.6 item 4).
func TestAuditModeIsProposedConfirmedAndTold(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: annaID, Language: "de", Devices: []approval.Device{{Service: "mobile_app_pixel_9"}}})
	// An approver the mandate does not name is not told.
	h.putApprover(approval.Approver{UserID: guestID, Devices: []approval.Device{{Service: "mobile_app_old_phone"}}})
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	d := draft(t)
	d["approval"] = map[string]any{"timeout": "PT2M", "approvers": []any{annaID}}
	var detail wireMandateDetail
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, m.Digest, true), &detail)
	base := detail.Summary.Digest

	trial := draft(t)
	trial["approval"] = d["approval"]
	trial["enforce"] = false
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", trial, base, true), &detail)
	if detail.Summary.Digest != base || detail.Summary.AuditMode || !detail.Summary.AuditModePending || detail.Pending == nil ||
		!strings.Contains(string(detail.Pending.Document), `"enforce":false`) || detail.Pending.ProposedBy != adminID {
		t.Fatalf("after proposing: %+v pending %+v", detail.Summary, detail.Pending)
	}
	if sent := h.ha.sent(); len(sent) != 1 || !strings.HasPrefix(sent[0], "mobile_app_pixel_9|Home-Mandate: Probebetrieb vorgeschlagen|") ||
		!strings.Contains(sent[0], detail.Pending.Digest[:19]) {
		t.Errorf("notifications after proposing = %v", sent)
	}
	var list []wireMandateSummary
	h.ok(http.MethodGet, "/api/mandates", nil, &list)
	if len(list) != 1 || !list[0].AuditModePending || list[0].AuditMode {
		t.Errorf("list = %+v", list)
	}

	// Only the digest that was shown counts.
	pending := detail.Pending.Digest
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/confirm", map[string]any{"digest": "sha256:" + strings.Repeat("0", 64)}); r.errCode() != codeConflict {
		t.Errorf("confirm another digest = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/confirm", map[string]any{"digest": "x"}); r.errCode() != codeInvalidInput || r.field() != "/digest" {
		t.Errorf("confirm without a digest = %d %s", r.code, r.body)
	}
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/confirm", map[string]any{"digest": pending}, &detail)
	if detail.Summary.Digest != pending || !detail.Summary.AuditMode || detail.Summary.AuditModePending || detail.Pending != nil {
		t.Fatalf("after confirming: %+v", detail.Summary)
	}
	if sent := h.ha.sent(); len(sent) != 2 || !strings.HasPrefix(sent[1], "mobile_app_pixel_9|Home-Mandate: Probebetrieb aktiv|") {
		t.Errorf("notifications after confirming = %v", sent)
	}

	// Switching it off takes effect at once.
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", d, pending, true), &detail)
	if detail.Summary.AuditMode || detail.Pending != nil {
		t.Errorf("after switching off: %+v", detail.Summary)
	}
}

func TestAProposalCanBeDiscarded(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	trial := draft(t)
	trial["enforce"] = false
	var detail wireMandateDetail
	h.ok(http.MethodPut, "/api/mandates/"+m.ID, update("Voice", trial, m.Digest, true), &detail)
	if detail.Pending == nil {
		t.Fatal("nothing waits")
	}
	h.ok(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/discard", nil, &detail)
	if detail.Pending != nil || detail.Summary.Digest != m.Digest {
		t.Errorf("after discarding: %+v", detail)
	}
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/discard", nil); r.errCode() != codeNotFound {
		t.Errorf("discard twice = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/confirm", map[string]any{"digest": m.Digest}); r.errCode() != codeNotFound {
		t.Errorf("confirm without a proposal = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/mandates/no-such/audit-mode/discard", nil); r.errCode() != codeNotFound {
		t.Errorf("discard of an unknown mandate = %d %s", r.code, r.body)
	}
	for path, body := range map[string]any{"/api/mandates/x/audit-mode/confirm": map[string]any{"digest": m.Digest}, "/api/mandates/x/audit-mode/discard": nil} {
		if r := h.do(http.MethodPost, path, body); r.errCode() != codeNotFound {
			t.Errorf("%s with an impossible ID = %d %s", path, r.code, r.body)
		}
	}
	if r := h.do(http.MethodPost, "/api/mandates/"+m.ID+"/audit-mode/confirm", map[string]any{"digest": 7}); r.code != http.StatusBadRequest {
		t.Errorf("confirm with a number = %d %s", r.code, r.body)
	}
}

func TestApproversOfADocument(t *testing.T) {
	doc := []byte(`{"approval":{"approvers":["b","a"]},"rules":[{"approval":{"approvers":["c","a"]}},{"id":"x"}]}`)
	if got := approversOf(doc); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("approversOf = %v", got)
	}
	if got := approversOf([]byte(`[]`)); got != nil {
		t.Errorf("approversOf(no object) = %v", got)
	}
	if got := shortDigest("sha256:" + strings.Repeat("ab", 32)); got != "sha256:abababababab" {
		t.Errorf("shortDigest = %q", got)
	}
	if got := shortDigest("sha256:ab"); got != "sha256:ab" {
		t.Errorf("shortDigest of a short text = %q", got)
	}
}

// Audit mode can only be switched on in the editor of an existing mandate; elsewhere the
// answer says so at the field.
func TestAuditModeElsewhereIsNamedAtTheField(t *testing.T) {
	err := mandateError(mandate.ErrAuditModeNeedsConfirmation, "/draft", nil)
	var ae *apiError
	if !errors.As(err, &ae) || ae.code != codeAuditModeNotHere || ae.field != "/draft/enforce" {
		t.Errorf("mandateError = %v", err)
	}
}

// The notices go out in the household's language, English for one we do not have; a
// proposal that does not exist, or approvers that cannot be read, tell nobody.
func TestAuditModeNoticeEdges(t *testing.T) {
	h := newHarness(t)
	for lang, want := range map[string]i18n.Lang{"de": i18n.DE, "en": i18n.EN, "fr": i18n.EN, "": i18n.EN} {
		h.status.Language = lang
		if got := h.srv.language(); got != want {
			t.Errorf("language %q = %q, want %q", lang, got, want)
		}
	}
	h.srv.proposed(context.Background(), "no-such", adminID)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	h.srv.proposed(context.Background(), m.ID, adminID) // nothing waits
	h.srv.cfg.Approvers = nil
	info, _ := h.srv.cfg.Mandates.Get(context.Background(), m.ID)
	h.srv.tellApprovers(context.Background(), info, []byte(`{}`), i18n.AuditModeActiveTitle, i18n.AuditModeActiveMessage, adminID)
	if sent := h.ha.sent(); len(sent) != 0 {
		t.Errorf("notifications = %v", sent)
	}
}
