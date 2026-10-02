// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
	if err := a.Put(ctx, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, Approver{UserID: u1, Devices: []string{"mobile_app_old", "mobile_app_mac"}, UI: true, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	// Replacing keeps one entry with the new values, devices included.
	if err := a.Put(ctx, Approver{UserID: u1, Devices: []string{"mobile_app_markus", "mobile_app_mac"}, UI: true, UICritical: true,
		Language: "de"}); err != nil {
		t.Fatal(err)
	}
	list, err := a.List(ctx)
	if err != nil || len(list) != 2 || list[0].UserID != u1 || !slices.Equal(list[0].Devices, []string{"mobile_app_mac", "mobile_app_markus"}) ||
		!list[0].UI || !list[0].UICritical || list[0].Language != "de" || list[0].CreatedAt.IsZero() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !slices.Equal(list[1].Devices, []string{"mobile_app_anna"}) || list[1].UI || list[1].UICritical {
		t.Errorf("second = %+v", list[1])
	}
	// A person may answer in the UI only, without any device.
	if err := a.Put(ctx, Approver{UserID: u2, UI: true}); err != nil {
		t.Fatal(err)
	}
	if list, _ := a.List(ctx); len(list[1].Devices) != 0 || !list[1].UI {
		t.Errorf("UI only = %+v", list[1])
	}
	if err := a.Remove(ctx, u1); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx, u1); !errors.Is(err, ErrApproverNotFound) {
		t.Errorf("second remove = %v", err)
	}
	// Removing deletes the devices with the person.
	if err := a.Put(ctx, Approver{UserID: u1, UI: true}); err != nil {
		t.Fatal(err)
	}
	if list, _ := a.List(ctx); len(list[0].Devices) != 0 {
		t.Errorf("devices survived removal: %+v", list[0])
	}
}

// Combination table "saving" (F2): at least one channel, at most five devices, no
// duplicates, ui_critical only together with ui.
func TestPutApproverChannels(t *testing.T) {
	devices := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "mobile_app_" + string(rune('a'+i))
		}
		return out
	}
	cases := []struct {
		name string
		ap   Approver
		ok   bool
	}{
		{"one device", Approver{Devices: devices(1)}, true},
		{"five devices", Approver{Devices: devices(5)}, true},
		{"UI only", Approver{UI: true}, true},
		{"UI only, also critical", Approver{UI: true, UICritical: true}, true},
		{"device and UI", Approver{Devices: devices(1), UI: true}, true},
		{"device and UI, also critical", Approver{Devices: devices(2), UI: true, UICritical: true}, true},

		{"no channel at all", Approver{}, false},
		{"critical in the UI without UI", Approver{UICritical: true}, false},
		{"critical in the UI without UI, with device", Approver{Devices: devices(1), UICritical: true}, false},
		{"six devices", Approver{Devices: devices(6)}, false},
		{"duplicate device", Approver{Devices: []string{"mobile_app_a", "mobile_app_a"}}, false},
		{"empty device name", Approver{Devices: []string{""}}, false},
		{"device with notify prefix", Approver{Devices: []string{"notify.mobile_app_x"}}, false},
		{"device upper case", Approver{Devices: []string{"Mobile"}}, false},
		{"device too long", Approver{Devices: []string{strings.Repeat("a", 65)}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newApprovers(t)
			c.ap.UserID = u1
			err := a.Put(context.Background(), c.ap)
			if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrInvalidApprover)) {
				t.Fatalf("Put = %v, want ok=%v", err, c.ok)
			}
			list, _ := a.List(context.Background())
			if (len(list) == 1) != c.ok {
				t.Errorf("stored = %+v", list)
			}
		})
	}
}

func TestPutApproverRejects(t *testing.T) {
	a, _ := newApprovers(t)
	for name, ap := range map[string]Approver{
		"empty user":         {Devices: []string{"mobile_app_x"}},
		"user with space":    {UserID: "a b", Devices: []string{"mobile_app_x"}},
		"long user":          {UserID: strings.Repeat("a", 65), Devices: []string{"mobile_app_x"}},
		"unknown language":   {UserID: u1, Devices: []string{"mobile_app_x"}, Language: "fr"},
		"language with tail": {UserID: u1, Devices: []string{"mobile_app_x"}, Language: "de-DE"},
	} {
		if err := a.Put(context.Background(), ap); !errors.Is(err, ErrInvalidApprover) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The UI channel is for administrators only (the UI is admin-only); the API checks this
// when saving, the service again for every request and answer.
func TestCheckUI(t *testing.T) {
	cases := []struct {
		ap    Approver
		admin bool
		ok    bool
	}{
		{Approver{Devices: []string{"mobile_app_a"}}, false, true},
		{Approver{Devices: []string{"mobile_app_a"}}, true, true},
		{Approver{UI: true}, true, true},
		{Approver{UI: true, UICritical: true}, true, true},
		{Approver{UI: true}, false, false},
		{Approver{Devices: []string{"mobile_app_a"}, UI: true, UICritical: true}, false, false},
	}
	for _, c := range cases {
		err := CheckUI(c.ap, c.admin)
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrUINeedsAdmin)) {
			t.Errorf("CheckUI(%+v, admin=%v) = %v", c.ap, c.admin, err)
		}
	}
}

// configs are the eight valid channel configurations: 0, 1 or 2 devices × UI ×
// critical actions in the UI.
var configs = map[string]Approver{
	"UI":             {UI: true},
	"UI+crit":        {UI: true, UICritical: true},
	"1 dev":          {Devices: []string{"mobile_app_a"}},
	"1 dev, UI":      {Devices: []string{"mobile_app_a"}, UI: true},
	"1 dev, UI+crit": {Devices: []string{"mobile_app_a"}, UI: true, UICritical: true},
	"2 dev":          {Devices: []string{"mobile_app_a", "mobile_app_b"}},
	"2 dev, UI":      {Devices: []string{"mobile_app_a", "mobile_app_b"}, UI: true},
	"2 dev, UI+crit": {Devices: []string{"mobile_app_a", "mobile_app_b"}, UI: true, UICritical: true},
}

// Combination table "channels" (F2): every valid configuration × administrator now ×
// critical action. Devices always get every request; the UI only an administrator,
// and a critical one only with ui_critical.
func TestChannelsCombinations(t *testing.T) {
	cases := []struct {
		config          string
		admin, critical bool
		devices         int
		ui, reachable   bool
	}{
		{"UI", false, false, 0, false, false},
		{"UI", false, true, 0, false, false},
		{"UI", true, false, 0, true, true},
		{"UI", true, true, 0, false, false},

		{"UI+crit", false, false, 0, false, false},
		{"UI+crit", false, true, 0, false, false},
		{"UI+crit", true, false, 0, true, true},
		{"UI+crit", true, true, 0, true, true},

		{"1 dev", false, false, 1, false, true},
		{"1 dev", false, true, 1, false, true},
		{"1 dev", true, false, 1, false, true},
		{"1 dev", true, true, 1, false, true},

		{"1 dev, UI", false, false, 1, false, true},
		{"1 dev, UI", false, true, 1, false, true},
		{"1 dev, UI", true, false, 1, true, true},
		{"1 dev, UI", true, true, 1, false, true},

		{"1 dev, UI+crit", false, false, 1, false, true},
		{"1 dev, UI+crit", false, true, 1, false, true},
		{"1 dev, UI+crit", true, false, 1, true, true},
		{"1 dev, UI+crit", true, true, 1, true, true},

		{"2 dev", false, false, 2, false, true},
		{"2 dev", false, true, 2, false, true},
		{"2 dev", true, false, 2, false, true},
		{"2 dev", true, true, 2, false, true},

		{"2 dev, UI", false, false, 2, false, true},
		{"2 dev, UI", false, true, 2, false, true},
		{"2 dev, UI", true, false, 2, true, true},
		{"2 dev, UI", true, true, 2, false, true},

		{"2 dev, UI+crit", false, false, 2, false, true},
		{"2 dev, UI+crit", false, true, 2, false, true},
		{"2 dev, UI+crit", true, false, 2, true, true},
		{"2 dev, UI+crit", true, true, 2, true, true},
	}
	if len(cases) != len(configs)*4 {
		t.Fatalf("%d cases, want every combination (%d)", len(cases), len(configs)*4)
	}
	for _, c := range cases {
		ap, ok := configs[c.config]
		if !ok {
			t.Fatalf("unknown config %q", c.config)
		}
		got := ap.Channels(c.critical, c.admin)
		if len(got.Devices) != c.devices || !slices.Equal(got.Devices, ap.Devices) || got.UI != c.ui || got.Reachable() != c.reachable {
			t.Errorf("%s, admin=%v, critical=%v: %+v reachable=%v, want %d devices, ui=%v, reachable=%v",
				c.config, c.admin, c.critical, got, got.Reachable(), c.devices, c.ui, c.reachable)
		}
	}
}

// Combination table "reach" for the settings: can a person be reached for ordinary and
// for critical requests?
func TestReachCombinations(t *testing.T) {
	cases := []struct {
		config           string
		admin            bool
		normal, critical bool
	}{
		{"UI", false, false, false},
		{"UI", true, true, false},
		{"UI+crit", false, false, false},
		{"UI+crit", true, true, true},
		{"1 dev", false, true, true},
		{"1 dev", true, true, true},
		{"1 dev, UI", false, true, true},
		{"1 dev, UI", true, true, true},
		{"1 dev, UI+crit", false, true, true},
		{"1 dev, UI+crit", true, true, true},
		{"2 dev", false, true, true},
		{"2 dev", true, true, true},
		{"2 dev, UI", false, true, true},
		{"2 dev, UI", true, true, true},
		{"2 dev, UI+crit", false, true, true},
		{"2 dev, UI+crit", true, true, true},
	}
	if len(cases) != len(configs)*2 {
		t.Fatalf("%d cases, want %d", len(cases), len(configs)*2)
	}
	for _, c := range cases {
		if got := configs[c.config].Reach(c.admin); got != (Reach{Normal: c.normal, Critical: c.critical}) {
			t.Errorf("%s, admin=%v: %+v, want normal=%v critical=%v", c.config, c.admin, got, c.normal, c.critical)
		}
	}
}

func TestApproversReportDatabaseErrors(t *testing.T) {
	a, closeDB := newApprovers(t)
	_ = closeDB()
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u1, Devices: []string{"mobile_app_x"}}); err == nil {
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
