// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/store"
)

func newApprovers(t *testing.T) (*Approvers, func() error) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewApprovers(st.DB()), st.DB().Close
}

func TestApprovers(t *testing.T) {
	a, _ := newApprovers(t)
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u2, NotifyService: "mobile_app_anna"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, Approver{UserID: u1, NotifyService: "mobile_app_old", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	// Replacing keeps one entry with the new values.
	if err := a.Put(ctx, Approver{UserID: u1, NotifyService: "mobile_app_markus", Language: "de"}); err != nil {
		t.Fatal(err)
	}
	list, err := a.List(ctx)
	if err != nil || len(list) != 2 || list[0].UserID != u1 || list[0].NotifyService != "mobile_app_markus" ||
		list[0].Language != "de" || list[0].CreatedAt.IsZero() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := a.Remove(ctx, u1); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx, u1); !errors.Is(err, ErrApproverNotFound) {
		t.Errorf("second remove = %v", err)
	}
}

func TestPutApproverRejects(t *testing.T) {
	a, _ := newApprovers(t)
	for name, ap := range map[string]Approver{
		"empty user":         {NotifyService: "mobile_app_x"},
		"user with space":    {UserID: "a b", NotifyService: "mobile_app_x"},
		"long user":          {UserID: strings.Repeat("a", 65), NotifyService: "mobile_app_x"},
		"service with dot":   {UserID: u1, NotifyService: "notify.mobile_app_x"},
		"service upper":      {UserID: u1, NotifyService: "Mobile"},
		"no service":         {UserID: u1},
		"unknown language":   {UserID: u1, NotifyService: "mobile_app_x", Language: "fr"},
		"language with tail": {UserID: u1, NotifyService: "mobile_app_x", Language: "de-DE"},
	} {
		if err := a.Put(context.Background(), ap); !errors.Is(err, ErrInvalidApprover) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestApproversReportDatabaseErrors(t *testing.T) {
	a, closeDB := newApprovers(t)
	_ = closeDB()
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u1, NotifyService: "mobile_app_x"}); err == nil {
		t.Error("Put succeeded")
	}
	if _, err := a.List(ctx); err == nil {
		t.Error("List succeeded")
	}
	if err := a.Remove(ctx, u1); err == nil || errors.Is(err, ErrApproverNotFound) {
		t.Errorf("Remove = %v", err)
	}
	// Ask cannot know the approvers and fails instead of waiting.
	svc := New(Config{Approvers: a, Notifier: newFakeNotifier(), MaxTimeout: time.Minute})
	if _, err := svc.Ask(ctx, request()); err == nil || errors.Is(err, ErrNoApprover) {
		t.Errorf("Ask = %v", err)
	}
}

// Without a household language the default applies; a warning that cannot be delivered
// is logged, nothing else.
func TestDefaultsAndUndeliverableWarnings(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.Language = nil
	if got := e.svc.language(Approver{}); got != "en" {
		t.Errorf("language = %q", got)
	}
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.notifier.next(t)
	e.notifier.setFail("mobile_app_markus", true)
	e.notifier.setFail("mobile_app_anna", true)
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, "not a user id!"))
	if a := wait(t, ch); a.res.Outcome != OutcomeInvalidResponse || a.res.By != "unknown" {
		t.Errorf("result = %+v", a.res)
	}
}
