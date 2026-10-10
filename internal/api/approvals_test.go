// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

func findOpen(list []wireApprovalRequest, id string) (wireApprovalRequest, bool) {
	for _, r := range list {
		if r.ID == id {
			return r, true
		}
	}
	return wireApprovalRequest{}, false
}

// The card shows the service data and the reason cleaned like the push (S1); every
// administrator sees open requests, can_answer only who may answer here now (F2).
func TestOpenApprovals(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: adminID, UI: true, UICritical: true})
	h.putApprover(approval.Approver{UserID: annaID, UI: true})
	id, _ := h.ask(unlockRequest(adminID, annaID))
	var got wireApprovals
	h.ok(http.MethodGet, "/api/approvals", nil, &got)
	req, ok := findOpen(got.Open, id)
	if !ok {
		t.Fatalf("open = %+v", got.Open)
	}
	if req.Agent.DisplayName != "Voice <b>" || req.EntityID != "lock.front_door" || req.DeviceName != "Haustür" || *req.Area != "hall" ||
		req.Action != "unlock" || !req.Critical || req.Reason == nil || strings.Contains(*req.Reason, "://") ||
		strings.ContainsAny(*req.Reason, "[]()") || len(req.Params) != 1 || req.Params[0] != (wireParam{"code", "1234"}) ||
		strings.Join(req.Recipients, ",") != "Markus" || !req.CanAnswer || req.ExpiresAt <= req.CreatedAt {
		t.Errorf("request = %+v", req)
	}
	// Anna has no UI for critical actions: not reached, she sees it but cannot answer here.
	h.ok(http.MethodGet, "/api/approvals", nil, &got, as(annaID))
	if req, _ := findOpen(got.Open, id); req.CanAnswer {
		t.Error("Anna may answer a critical request in the UI")
	}
	if len(got.History) != 0 {
		t.Errorf("history = %+v", got.History)
	}
}

// Combination table: who may answer in the UI (F2), as the server reports it and as it
// enforces it on the answer. The request stays open after every refusal.
func TestAnswerCombinations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ap       *approval.Approver
		user     string
		critical bool
		want     bool
	}{
		{"approver with UI", &approval.Approver{UserID: annaID, UI: true}, annaID, false, true},
		{"UI, critical without ui_critical", &approval.Approver{UserID: annaID, UI: true}, annaID, true, false},
		{"UI and ui_critical, critical", &approval.Approver{UserID: annaID, UI: true, UICritical: true}, annaID, true, true},
		{"device only", &approval.Approver{UserID: annaID, Devices: []approval.Device{{Service: "mobile_app_pixel_9", Critical: true}}}, annaID, false, false},
		{"not an approver of it", &approval.Approver{UserID: annaID, UI: true}, adminID, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.putApprover(*tc.ap)
			// A second approver on a phone, so that every request reaches someone.
			h.putApprover(approval.Approver{UserID: guestID, Devices: []approval.Device{{Service: "mobile_app_old_phone", Critical: true}}})
			if tc.user == adminID { // an administrator with the UI, but no approver of this request
				h.putApprover(approval.Approver{UserID: adminID, UI: true, UICritical: true})
			}
			req := lightRequest(annaID, guestID)
			if tc.critical {
				req = unlockRequest(annaID, guestID)
			}
			id, done := h.ask(req)
			var got wireApprovals
			h.ok(http.MethodGet, "/api/approvals", nil, &got, as(tc.user))
			if r, _ := findOpen(got.Open, id); r.CanAnswer != tc.want {
				t.Errorf("can_answer = %v, want %v", r.CanAnswer, tc.want)
			}
			r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": true}, as(tc.user))
			if !tc.want {
				if r.errCode() != codeNotFound {
					t.Errorf("answer = %d %s", r.code, r.body)
				}
				if _, open := findOpen(h.openFor(tc.user), id); !open {
					t.Error("refused answer ended the request")
				}
				h.approvals.CancelAll()
				<-done
				return
			}
			var entry wireHistoryEntry
			r.json(t, &entry)
			if r.code != http.StatusOK || entry.Outcome != "approved" || entry.Via != "ui" || entry.ByName == nil || *entry.ByName != "Anna" ||
				entry.Seq == 0 || entry.EntityID != req.EntityID {
				t.Errorf("answer = %d %+v", r.code, entry)
			}
			if res := <-done; res.Outcome != approval.OutcomeApproved || res.By != annaID || res.Via != approval.ViaUI {
				t.Errorf("result = %+v", res)
			}
		})
	}
}

func (h *harness) openFor(user string) []wireApprovalRequest {
	var got wireApprovals
	h.ok(http.MethodGet, "/api/approvals", nil, &got, as(user))
	return got.Open
}

// Negative catalog: unknown, guessed or malformed IDs and the nonce look alike (not_found).
func TestAnswerRefusals(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	id, done := h.ask(lightRequest(adminID))
	for _, bad := range []string{"0123456789abcdef0123456789abcdef", id[:31], id + "0", strings.ToUpper(id), "x"} {
		if r := h.do(http.MethodPost, "/api/approvals/"+bad+"/answer", map[string]any{"approve": false}); r.errCode() != codeNotFound {
			t.Errorf("answer %q = %d", bad, r.code)
		}
	}
	if r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{}); r.field() != "/approve" {
		t.Errorf("no decision = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": false}, header("X-HM-CSRF", "")); r.errCode() != codeCSRF {
		t.Errorf("without CSRF = %d", r.code)
	}
	// Rejecting answers with the closed request.
	var entry wireHistoryEntry
	h.ok(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": false}, &entry)
	if entry.Outcome != "rejected" || entry.Via != "ui" {
		t.Errorf("rejected = %+v", entry)
	}
	<-done
	// Answered: a second answer finds nothing.
	if r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": true}); r.errCode() != codeNotFound {
		t.Errorf("second answer = %d", r.code)
	}
	limited := false
	for range answerLimit {
		if r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": true}); r.errCode() == codeRateLimited {
			limited = r.header.Get("Retry-After") != ""
			break
		}
	}
	if !limited {
		t.Error("answers are not limited")
	}
}

// When the outcome is not written in time (the action is still running), the answer
// says "unavailable": it counted, the history will show the outcome.
func TestAnswerWithoutAuditEntryInTime(t *testing.T) {
	h := newHarness(t)
	old := answerWait
	answerWait = 50 * time.Millisecond
	defer func() { answerWait = old }()
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	h.log.OnCommit(nil) // nobody reports the entry
	id, done := h.ask(lightRequest(adminID))
	if r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": true}); r.errCode() != codeUnavailable {
		t.Errorf("answer = %d %s", r.code, r.body)
	}
	if res := <-done; res.Outcome != approval.OutcomeApproved {
		t.Errorf("the answer did not count: %+v", res)
	}
}

// The history: endings with an answer, cancelled ones with their cause (SPEC-v0 section
// 11.1 item 8), and the two of decision F1 that older logs hold without an approval,
// shown as cancelled with the cause they stand for.
func TestApprovalHistory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	at := testStart.Add(-time.Minute)
	ask := func(result audit.Result, appr *audit.Approval) {
		e := audit.Entry{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: "hm-client:voice-1", DisplayName: "Voice"},
			Request:    &audit.Request{Time: at.Add(-30 * time.Second), Resource: audit.Resource{EntityID: "lock.front_door"}, Action: "unlock"},
			Mandate:    &audit.Mandate{ID: "m-voice", Digest: "sha256:" + strings.Repeat("a", 64)},
			Evaluation: &audit.Evaluation{Decision: "ask", Reason: "rule", RuleID: ptr("r-locks")}, Result: &result, Approval: appr}
		if _, err := h.log.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	cancelled := func(cause string) *audit.Approval {
		return &audit.Approval{Outcome: audit.OutcomeCancelled, Cause: cause, At: at}
	}
	ask(audit.Result{Status: audit.StatusExecuted}, &audit.Approval{Outcome: "approved", By: adminID, Via: "push", At: at})
	ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, &audit.Approval{Outcome: "timeout", At: at})
	ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}, nil)
	ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication}, nil)
	ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, cancelled(audit.CauseInterrupted))
	ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}, cancelled(audit.CauseRevoked))
	ask(audit.Result{Status: audit.StatusFailed, Error: "outcome_unknown"}, &audit.Approval{Outcome: "approved", By: adminID, Via: "ui", At: at})
	var got wireApprovals
	h.ok(http.MethodGet, "/api/approvals", nil, &got)
	if len(got.History) != 7 {
		t.Fatalf("history = %+v", got.History)
	}
	want := []struct{ outcome, cause string }{{"approved", ""}, {"cancelled", "revoked"}, {"cancelled", "interrupted"},
		{"cancelled", "revoked"}, {"cancelled", "emergency_stop"}, {"timeout", ""}, {"approved", ""}}
	for i, e := range got.History {
		if e.Outcome != want[i].outcome || e.Cause != want[i].cause || e.DeviceName != "Haustür" || e.CreatedAt != "2026-10-03T09:58:30.000Z" {
			t.Errorf("history[%d] = %+v", i, e)
		}
	}
	if a := got.History[6]; *a.ByName != "Markus" || a.Via != "push" || a.AnsweredAt != "2026-10-03T09:59:00.000Z" {
		t.Errorf("approved = %+v", a)
	}
	if e := got.History[2]; e.ByName != nil || e.Via != "" || e.AnsweredAt != "2026-10-03T09:59:00.000Z" {
		t.Errorf("interrupted = %+v", e)
	}
	if got.History[4].ByName != nil || got.History[4].Via != "" {
		t.Errorf("emergency stop = %+v", got.History[4])
	}
	// The result is part of the history: an approval whose execution has an unknown outcome
	// says so.
	if e := got.History[0]; e.Error != "outcome_unknown" {
		t.Errorf("outcome unknown = %+v", e)
	}
	// A device that is gone from the catalog shows its entity ID.
	h.cat.devices = nil
	h.ok(http.MethodGet, "/api/approvals", nil, &got)
	if got.History[0].DeviceName != "lock.front_door" {
		t.Errorf("device name = %q", got.History[0].DeviceName)
	}
}

// Cause and error reach the UI only from the known sets; anything else (a log written by
// another version, or changed in the database) is shown as a generic value.
func TestHistoryShowsOnlyKnownCausesAndErrors(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, tc := range []struct {
		entry      string
		cause, err string
	}{
		{`{"request":{"time":"2026-10-03T09:58:30Z"},"approval":{"outcome":"cancelled","cause":"interrupted"},"result":{"status":"denied"}}`, "interrupted", ""},
		{`{"request":{"time":"2026-10-03T09:58:30Z"},"approval":{"outcome":"cancelled","cause":"<b>later</b>"},"result":{"status":"denied"}}`, "", ""},
		{`{"request":{"time":"2026-10-03T09:58:30Z"},"approval":{"outcome":"approved"},"result":{"status":"failed","error":"outcome_unknown"}}`, "", "outcome_unknown"},
		{`{"request":{"time":"2026-10-03T09:58:30Z"},"approval":{"outcome":"approved"},"result":{"status":"failed","error":"\u202e"}}`, "", "other"},
	} {
		got, err := h.srv.historyEntry(ctx, 1, testStart, json.RawMessage(tc.entry))
		if err != nil || got.Cause != tc.cause || got.Error != tc.err {
			t.Errorf("%s: %+v, %v", tc.entry, got, err)
		}
	}
}

func TestAuditCommittedIgnoresOtherEntries(t *testing.T) {
	h := newHarness(t)
	h.srv.AuditCommitted(1, audit.Entry{Event: audit.EventAgentRegistered})
	// A history entry that cannot be built (no request) is not announced.
	h.srv.AuditCommitted(2, audit.Entry{Event: audit.EventDecision, ApprovalID: "x"})
}
