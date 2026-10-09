// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// withPlaceholder lets the approvers placeholder stand for the approvers set up, as the
// gateway does.
func (h *harness) withPlaceholder() {
	h.adm.SetApprovers(func(ctx context.Context) ([]string, error) {
		list, err := h.approvers.List(ctx)
		ids := make([]string, len(list))
		for i, a := range list {
			ids[i] = a.UserID
		}
		return ids, err
	})
}

func (h *harness) putApproverDirect(ap approval.Approver) {
	h.t.Helper()
	if err := h.approvers.Put(context.Background(), ap, audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		h.t.Fatal(err)
	}
}

func TestTemplateApproversShowsWhoMayApprove(t *testing.T) {
	h := newHarness(t)
	h.withPlaceholder()
	// Anna has a phone that does not get critical requests.
	h.putApproverDirect(approval.Approver{UserID: annaID, Devices: []approval.Device{{Service: "mobile_app_pixel_9"}}})
	var got wireTemplateApprovers
	h.ok(http.MethodGet, "/api/templates/hm-voice-cautious/approvers", nil, &got)
	if len(got.People) != 2 {
		t.Fatalf("people = %+v", got.People)
	}
	markus, anna := got.People[0], got.People[1]
	// The admitting human first: without any channel.
	if markus.UserID != adminID || markus.Name == nil || *markus.Name != "Markus" || !markus.Self ||
		markus.Normal != approval.ReachNone || markus.Critical != approval.ReachNone {
		t.Errorf("markus = %+v", markus)
	}
	if anna.UserID != annaID || anna.Name == nil || *anna.Name != "Anna" || anna.Self ||
		anna.Normal != approval.ReachPush || anna.Critical != approval.ReachNone {
		t.Errorf("anna = %+v", anna)
	}
	// The template asks only for critical actions (locks), which nobody gets.
	if got.Normal != approval.CoverageNotNeeded || got.Critical != approval.CoverageNobody {
		t.Errorf("coverage = %s %s", got.Normal, got.Critical)
	}

	// With a phone for critical requests, nobody is warned.
	h.putApproverDirect(approval.Approver{UserID: annaID, Devices: []approval.Device{{Service: "mobile_app_pixel_9", Critical: true}}})
	h.ok(http.MethodGet, "/api/templates/hm-voice-cautious/approvers", nil, &got)
	if got.Critical != approval.CoverageReachable || got.People[1].Critical != approval.ReachPush {
		t.Errorf("critical = %s, anna = %+v", got.Critical, got.People[1])
	}
}

func TestTemplateApproversNamesAreText(t *testing.T) {
	h := newHarness(t)
	h.withPlaceholder()
	h.ha.set(func(f *fakeHA) { f.users[1].Name = `<img src=x onerror=alert(1)>Anna` })
	h.putApproverDirect(approval.Approver{UserID: annaID, UI: true})
	r := h.do(http.MethodGet, "/api/templates/hm-read-only/approvers", nil)
	var got wireTemplateApprovers
	r.json(t, &got)
	if len(got.People) != 2 || got.People[1].Name == nil || *got.People[1].Name != `<img src=x onerror=alert(1)>Anna` {
		t.Fatalf("people = %+v", got.People)
	}
	if got.People[1].Normal != approval.ReachUI {
		t.Errorf("anna = %+v", got.People[1])
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" && ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestTemplateApproversExplicitAndServiceUser(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// A template that names someone unknown to Home Assistant and Home-Mandate's own user.
	if err := h.adm.PutTemplate(ctx, "named", voiceTemplate(t, func(d map[string]any) {
		d["approval"] = map[string]any{"timeout": "PT2M", "approvers": []any{"someone-else", serviceID}}
		d["rules"] = []any{map[string]any{"id": "r-1", "resource": map[string]any{"category": "light"}, "actions": []any{"turn_on"},
			"decision": "ask"}}
	}), audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		t.Fatal(err)
	}
	h.putApproverDirect(approval.Approver{UserID: serviceID, Devices: []approval.Device{{Service: "mobile_app_old_phone", Critical: true}}})
	var got wireTemplateApprovers
	h.ok(http.MethodGet, "/api/templates/named/approvers", nil, &got)
	if len(got.People) != 2 || got.People[0].Name != nil || got.People[0].Normal != approval.ReachNone ||
		!got.People[1].Service || got.People[1].Normal != approval.ReachNone {
		t.Errorf("people = %+v", got.People)
	}
	if got.Normal != approval.CoverageNobody || got.Critical != approval.CoverageNotNeeded {
		t.Errorf("coverage = %s %s", got.Normal, got.Critical)
	}
}

func TestTemplateApproversWithoutHomeAssistantIsUnknown(t *testing.T) {
	h := newHarness(t)
	h.withPlaceholder()
	h.putApproverDirect(approval.Approver{UserID: annaID, Devices: []approval.Device{{Service: "mobile_app_pixel_9", Critical: true}}})
	// Signed in while Home Assistant answered; then it stops answering.
	h.ok(http.MethodGet, "/api/templates/hm-voice-cautious/approvers", nil, nil)
	h.ha.set(func(f *fakeHA) { f.usersErr = errHA })
	h.now.Add(usersTTL + time.Second)
	got, err := h.srv.ApproversPreview(context.Background(), "hm-voice-cautious", audit.Actor{Kind: audit.ActorUser, ID: adminID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Critical != approval.CoverageUnknown || len(got.People) != 2 || got.People[1].Critical != approval.ReachUnknown ||
		got.People[0].Critical != approval.ReachNone || got.People[1].Name != "" {
		t.Errorf("preview = %+v", got)
	}
}

func TestTemplateApproversRefuses(t *testing.T) {
	h := newHarness(t)
	for path, want := range map[string]string{
		"/api/templates/missing/approvers":    codeNotFound,
		"/api/templates/Bad%20Name/approvers": codeNotFound,
	} {
		if r := h.do(http.MethodGet, path, nil); r.errCode() != want {
			t.Errorf("%s = %d %s", path, r.code, r.body)
		}
	}
	if err := h.adm.SetHidden(context.Background(), "hm-read-only", true, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/api/templates/hm-read-only/approvers", nil); r.errCode() != codeNotFound {
		t.Errorf("hidden = %d", r.code)
	}
	// Not an administrator: refused like every API request.
	if r := h.do(http.MethodGet, "/api/templates/hm-voice-cautious/approvers", nil, as(guestID)); r.code != http.StatusForbidden {
		t.Errorf("guest = %d", r.code)
	}
	_ = h.st.Close()
	if r := h.do(http.MethodGet, "/api/templates/hm-voice-cautious/approvers", nil); r.errCode() != codeInternal {
		t.Errorf("database closed = %d %s", r.code, r.body)
	}
}
