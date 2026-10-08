// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

func TestApproverCandidates(t *testing.T) {
	h := newHarness(t)
	var list wireApproverList
	h.ok(http.MethodGet, "/api/approvers", nil, &list)
	people := map[string]wirePerson{}
	for _, p := range list.Candidates.People {
		people[p.UserID] = p
	}
	// Home-Mandate's own user, system users and persons without a user are no candidates.
	if len(people) != 3 || people[adminID].Name != "Markus M." || !people[adminID].IsAdmin || people[annaID].Name != "Anna" ||
		!people[annaID].IsAdmin || people[guestID].IsAdmin {
		t.Errorf("people = %+v", list.Candidates.People)
	}
	devices := map[string]wireCandidateDevice{}
	for _, d := range list.Candidates.Devices {
		devices[d.Service] = d
	}
	iphone, mac, pixel, ipad, old := devices["mobile_app_iphone_von_markus"], devices["mobile_app_macbook_pro"],
		devices["mobile_app_pixel_9"], devices["mobile_app_kuchen_ipad"], devices["mobile_app_old_phone"]
	if !iphone.SuggestCritical || *iphone.OwnerUserID != adminID || iphone.Name != "iPhone von Markus" {
		t.Errorf("iphone = %+v", iphone)
	}
	if mac.SuggestCritical || *mac.OwnerUserID != adminID || mac.Name != "Arbeits-Mac <b>" {
		t.Errorf("mac = %+v", mac)
	}
	if pixel.SuggestCritical || *pixel.OwnerUserID != annaID {
		t.Errorf("pixel = %+v", pixel)
	}
	if !ipad.SuggestCritical || ipad.OwnerUserID != nil {
		t.Errorf("ipad = %+v", ipad)
	}
	if old.SuggestCritical || old.OwnerUserID != nil || old.Name != "mobile_app_old_phone" {
		t.Errorf("unknown device = %+v", old)
	}
	if len(list.Approvers) != 0 {
		t.Errorf("approvers = %+v", list.Approvers)
	}
	h.ha.set(func(f *fakeHA) { f.err = errHA })
	if r := h.do(http.MethodGet, "/api/approvers", nil); r.errCode() != codeUnavailable {
		t.Errorf("Home Assistant down = %d", r.code)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"iPhone von Markus": "iphone_von_markus", "Küchen-iPad": "kuchen_ipad", "  Pixel 9  ": "pixel_9", "Straße 1": "strasse_1",
		"___": "", "Ä--Ö": "a_o",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range []struct {
		d    ha.Device
		want bool
	}{
		{ha.Device{Manufacturer: "Apple", Model: "iPhone15,2"}, true}, {ha.Device{Manufacturer: "Apple", Model: "iPad13,1"}, true},
		{ha.Device{Manufacturer: "Apple", Model: "Mac14,2"}, false}, {ha.Device{Manufacturer: "Google", Model: "iPhone"}, false},
		{ha.Device{}, false},
	} {
		if got := isIOS(tc.d); got != tc.want {
			t.Errorf("isIOS(%+v) = %v", tc.d, got)
		}
	}
}

func putBody(devices []map[string]any, ui, uiCritical bool, lang any) map[string]any {
	return map[string]any{"devices": devices, "ui": ui, "ui_critical": uiCritical, "language": lang}
}

func dev(service string, critical bool) map[string]any {
	return map[string]any{"service": service, "critical": critical}
}

func TestPutApprover(t *testing.T) {
	h := newHarness(t)
	var list wireApproverList
	h.ok(http.MethodPut, "/api/approvers/"+adminID, putBody([]map[string]any{dev("mobile_app_iphone_von_markus", true),
		dev("mobile_app_macbook_pro", false)}, true, false, "de"), &list)
	if len(list.Approvers) != 1 {
		t.Fatalf("approvers = %+v", list.Approvers)
	}
	a := list.Approvers[0]
	if a.Name != "Markus M." || len(a.Devices) != 2 || !a.UI || a.UICritical || *a.Language != "de" ||
		a.Reach != (wireReach{Normal: "push", Critical: "push"}) {
		t.Errorf("approver = %+v", a)
	}
	// UI only: requests reach the person only in the UI; critical ones not at all.
	h.ok(http.MethodPut, "/api/approvers/"+annaID, putBody(nil, true, false, nil), &list)
	if a := list.Approvers[0]; a.UserID != annaID || a.Reach != (wireReach{Normal: "ui", Critical: "none"}) || a.Language != nil {
		t.Errorf("anna = %+v", a)
	}
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.ApproversConfigured != 2 {
		t.Errorf("approvers configured = %d", sys.ApproversConfigured)
	}
	// Combination table of saving (F2), each naming only the field.
	six := []map[string]any{}
	for _, s := range []string{"mobile_app_iphone_von_markus", "mobile_app_kuchen_ipad", "mobile_app_macbook_pro", "mobile_app_old_phone", "mobile_app_pixel_9", "mobile_app_x"} {
		six = append(six, dev(s, false))
	}
	for _, tc := range []struct {
		name  string
		user  string
		body  map[string]any
		field string
	}{
		{"no channel", annaID, putBody(nil, false, false, nil), "/devices"},
		{"empty list, no UI", annaID, putBody([]map[string]any{}, false, false, nil), "/devices"},
		{"six devices", annaID, putBody(six, false, false, nil), "/devices"},
		{"duplicate", annaID, putBody([]map[string]any{dev("mobile_app_pixel_9", false), dev("mobile_app_pixel_9", true)}, false, false, nil), "/devices"},
		{"unknown device", annaID, putBody([]map[string]any{dev("mobile_app_evil", false)}, false, false, nil), "/devices"},
		{"bad service name", annaID, putBody([]map[string]any{dev("Mobile.App", false)}, false, false, nil), "/devices"},
		{"ui_critical without ui", annaID, putBody([]map[string]any{dev("mobile_app_pixel_9", false)}, false, true, nil), "/ui_critical"},
		{"UI for no administrator", guestID, putBody(nil, true, false, nil), "/ui"},
		{"Home-Mandate's own user", serviceID, putBody([]map[string]any{dev("mobile_app_pixel_9", false)}, false, false, nil), "/user_id"},
		{"no person", "ffffffffffffffffffffffffffffffff", putBody([]map[string]any{dev("mobile_app_pixel_9", false)}, false, false, nil), "/user_id"},
		{"malformed user", "a.b", putBody(nil, true, false, nil), "/user_id"},
		{"language", annaID, putBody(nil, true, false, "fr"), "/language"},
	} {
		if r := h.do(http.MethodPut, "/api/approvers/"+tc.user, tc.body); r.errCode() != codeInvalidInput || r.field() != tc.field {
			t.Errorf("%s = %d %s", tc.name, r.code, r.body)
		}
	}
	// The administrator check fails: fail closed.
	h.ha.set(func(f *fakeHA) { f.usersErr = errHA })
	h.now.Add(usersTTL)
	if r := h.do(http.MethodPut, "/api/approvers/"+annaID, putBody(nil, true, false, nil), as(annaID)); r.code != http.StatusServiceUnavailable {
		t.Errorf("check failed = %d %s", r.code, r.body)
	}
}

// Removing an approver takes them out of open requests: their answer then counts as
// anyone else's.
func TestDeleteApprover(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: annaID, UI: true})
	h.putApprover(approval.Approver{UserID: adminID, UI: true})
	id, done := h.ask(lightRequest(annaID, adminID))
	h.ok(http.MethodDelete, "/api/approvers/"+annaID, nil, nil)
	if r := h.do(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": true}, as(annaID)); r.errCode() != codeNotFound {
		t.Errorf("answer of a removed approver = %d", r.code)
	}
	h.ok(http.MethodPost, "/api/approvals/"+id+"/answer", map[string]any{"approve": false}, nil)
	<-done
	if r := h.do(http.MethodDelete, "/api/approvers/"+annaID, nil); r.errCode() != codeNotFound {
		t.Errorf("second delete = %d", r.code)
	}
	if r := h.do(http.MethodDelete, "/api/approvers/a.b", nil); r.errCode() != codeNotFound {
		t.Errorf("malformed = %d", r.code)
	}
}

// The test notification: only to the stored devices, neutral, no buttons, limited.
func TestTestApprover(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: adminID, Language: "de", Devices: []approval.Device{{Service: "mobile_app_iphone_von_markus", Critical: true},
		{Service: "mobile_app_macbook_pro"}}})
	h.putApprover(approval.Approver{UserID: annaID, UI: true})
	h.ok(http.MethodPost, "/api/approvers/"+adminID+"/test", nil, nil)
	sent := h.ha.sent()
	if len(sent) != 2 || !strings.HasPrefix(sent[0], "mobile_app_iphone_von_markus|Home-Mandate: Test|") || strings.HasSuffix(sent[0], "A") ||
		!strings.HasPrefix(sent[1], "mobile_app_macbook_pro|") {
		t.Errorf("sent = %q", sent)
	}
	if r := h.do(http.MethodPost, "/api/approvers/"+adminID+"/test", nil); r.errCode() != codeRateLimited || r.header.Get("Retry-After") != "30" {
		t.Errorf("second test = %d %v", r.code, r.header)
	}
	// UI only: nothing to send, nothing failed.
	h.ok(http.MethodPost, "/api/approvers/"+annaID+"/test", nil, nil)
	h.now.Add(testPeriodApprover)
	h.ha.set(func(f *fakeHA) {
		f.notifyErr = map[string]error{"mobile_app_iphone_von_markus": errHA, "mobile_app_macbook_pro": errHA}
	})
	if r := h.do(http.MethodPost, "/api/approvers/"+adminID+"/test", nil); r.errCode() != codeUnavailable {
		t.Errorf("nothing delivered = %d", r.code)
	}
	if r := h.do(http.MethodPost, "/api/approvers/"+guestID+"/test", nil); r.errCode() != codeNotFound {
		t.Errorf("no approver = %d", r.code)
	}
	if r := h.do(http.MethodPost, "/api/approvers/x.y/test", nil); r.errCode() != codeNotFound {
		t.Errorf("malformed = %d", r.code)
	}
	// Overall limit.
	h.ha.set(func(f *fakeHA) { f.notifyErr = nil })
	limited := false
	for i := range testLimitOverall + 2 {
		h.now.Add(testPeriodApprover)
		if i%5 == 0 {
			h.now.Add(-testPeriodApprover + time.Second)
		}
		if r := h.do(http.MethodPost, "/api/approvers/"+adminID+"/test", nil); r.errCode() == codeRateLimited {
			limited = true
		}
	}
	if !limited {
		t.Error("test notifications not limited")
	}
}

func TestUnavailableOr(t *testing.T) {
	for _, err := range []error{errUsersUnavailable, ha.ErrDisconnected} {
		var e *apiError
		if !errors.As(unavailableOr(err), &e) || e.code != codeUnavailable {
			t.Errorf("unavailableOr(%v) = %v", err, unavailableOr(err))
		}
	}
	if err := unavailableOr(errHA); err != errHA {
		t.Errorf("other error = %v", err)
	}
}

// The first change wins (decision of 2026-10-04): a change based on an older version of
// the approvers is a conflict and stores nothing; the answer carries the new version.
func TestApproverConflicts(t *testing.T) {
	h := newHarness(t)
	var list wireApproverList
	h.ok(http.MethodGet, "/api/approvers", nil, &list)
	old := list.Version
	if len(old) != 32 {
		t.Fatalf("version = %q", old)
	}
	// Markus saves first.
	body := putBody([]map[string]any{dev("mobile_app_iphone_von_markus", true)}, false, false, nil)
	body["base_version"] = old
	h.ok(http.MethodPut, "/api/approvers/"+adminID, body, &list)
	if list.Version == old {
		t.Fatal("version unchanged after a change")
	}
	// Anna's change started from the same, now old version: conflict, nothing stored.
	late := putBody(nil, true, false, nil)
	late["base_version"] = old
	if r := h.do(http.MethodPut, "/api/approvers/"+annaID, late, as(annaID)); r.errCode() != codeConflict || r.code != http.StatusConflict {
		t.Errorf("late change = %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodDelete, "/api/approvers/"+adminID+"?base_version="+old, nil); r.errCode() != codeConflict {
		t.Errorf("late removal = %d %s", r.code, r.body)
	}
	if got, _ := h.approvers.List(context.Background()); len(got) != 1 || got[0].UserID != adminID {
		t.Errorf("approvers = %+v", got)
	}
	// With the new version both work.
	late["base_version"] = list.Version
	h.ok(http.MethodPut, "/api/approvers/"+annaID, late, &list, as(annaID))
	h.ok(http.MethodDelete, "/api/approvers/"+annaID+"?base_version="+list.Version, nil, nil)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/approvers/" + annaID, map[string]any{"devices": []any{}, "ui": true, "ui_critical": false, "language": nil, "base_version": "x"}},
		{http.MethodPut, "/api/approvers/" + annaID, map[string]any{"devices": []any{}, "ui": true, "ui_critical": false, "language": nil, "base_version": ""}},
		{http.MethodDelete, "/api/approvers/" + adminID + "?base_version=x", nil},
		{http.MethodDelete, "/api/approvers/" + adminID + "?base_version=" + list.Version + "&base_version=" + list.Version, nil},
		{http.MethodDelete, "/api/approvers/" + adminID + "?other=1", nil},
	} {
		if r := h.do(tc.method, tc.path, tc.body); r.errCode() != codeInvalidInput || r.field() != "/base_version" {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, r.code, r.body)
		}
	}
}
