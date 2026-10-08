// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/store"
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
	if err := a.Put(ctx, Approver{UserID: u2, Devices: phones("mobile_app_anna")}); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, Approver{UserID: u1, Devices: phones("mobile_app_old", "mobile_app_mac"), UI: true, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	// Replacing keeps one entry with the new values, devices included.
	if err := a.Put(ctx, Approver{UserID: u1, Devices: []Device{{Service: "mobile_app_markus", Critical: true}, {Service: "mobile_app_mac"}},
		UI: true, UICritical: true, Language: "de"}); err != nil {
		t.Fatal(err)
	}
	list, err := a.List(ctx)
	if err != nil || len(list) != 2 || list[0].UserID != u1 || !slices.Equal(list[0].Devices, []Device{{Service: "mobile_app_mac"}, {Service: "mobile_app_markus", Critical: true}}) ||
		!list[0].UI || !list[0].UICritical || list[0].Language != "de" || list[0].CreatedAt.IsZero() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !slices.Equal(list[1].Devices, phones("mobile_app_anna")) || list[1].UI || list[1].UICritical {
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
	devices := func(n int) []Device {
		out := make([]Device, n)
		for i := range out {
			out[i] = Device{Service: "mobile_app_" + string(rune('a'+i)), Critical: i%2 == 0}
		}
		return out
	}
	cases := []struct {
		name string
		ap   Approver
		ok   bool
	}{
		{"one device", Approver{Devices: devices(1)}, true},
		{"only a device without critical actions", Approver{Devices: []Device{{Service: "mobile_app_mac"}}}, true},
		{"five devices", Approver{Devices: devices(5)}, true},
		{"UI only", Approver{UI: true}, true},
		{"UI only, also critical", Approver{UI: true, UICritical: true}, true},
		{"device and UI", Approver{Devices: devices(1), UI: true}, true},
		{"device and UI, also critical", Approver{Devices: devices(2), UI: true, UICritical: true}, true},

		{"no channel at all", Approver{}, false},
		{"critical in the UI without UI", Approver{UICritical: true}, false},
		{"critical in the UI without UI, with device", Approver{Devices: devices(1), UICritical: true}, false},
		{"six devices", Approver{Devices: devices(6)}, false},
		{"duplicate device", Approver{Devices: phones("mobile_app_a", "mobile_app_a")}, false},
		{"duplicate device, differing switch", Approver{Devices: []Device{{Service: "mobile_app_a", Critical: true}, {Service: "mobile_app_a"}}}, false},
		{"empty device name", Approver{Devices: phones("")}, false},
		{"device with notify prefix", Approver{Devices: phones("notify.mobile_app_x")}, false},
		{"device upper case", Approver{Devices: phones("Mobile")}, false},
		{"device too long", Approver{Devices: phones(strings.Repeat("a", 65))}, false},
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
		"empty user":         {Devices: phones("mobile_app_x")},
		"user with space":    {UserID: "a b", Devices: phones("mobile_app_x")},
		"long user":          {UserID: strings.Repeat("a", 65), Devices: phones("mobile_app_x")},
		"unknown language":   {UserID: u1, Devices: phones("mobile_app_x"), Language: "fr"},
		"language with tail": {UserID: u1, Devices: phones("mobile_app_x"), Language: "de-DE"},
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
		{Approver{Devices: phones("mobile_app_a")}, false, true},
		{Approver{Devices: phones("mobile_app_a")}, true, true},
		{Approver{UI: true}, true, true},
		{Approver{UI: true, UICritical: true}, true, true},
		{Approver{UI: true}, false, false},
		{Approver{Devices: phones("mobile_app_a"), UI: true, UICritical: true}, false, false},
	}
	for _, c := range cases {
		err := CheckUI(c.ap, c.admin)
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrUINeedsAdmin)) {
			t.Errorf("CheckUI(%+v, admin=%v) = %v", c.ap, c.admin, err)
		}
	}
}

// Devices of the combination tables: P a phone (critical actions too, the default for
// phones), M the Mac app (no critical actions, its default: no unlocking).
var (
	devP = Device{Service: "mobile_app_phone", Critical: true}
	devM = Device{Service: "mobile_app_mac"}
)

// configs are the eleven valid channel configurations: devices none, P, M or P+M × UI
// off, on or on for critical actions too (none + off has no channel and is invalid).
var configs = map[string]Approver{
	"P":           {Devices: []Device{devP}},
	"M":           {Devices: []Device{devM}},
	"PM":          {Devices: []Device{devP, devM}},
	"-, UI":       {UI: true},
	"-, UI+crit":  {UI: true, UICritical: true},
	"P, UI":       {Devices: []Device{devP}, UI: true},
	"P, UI+crit":  {Devices: []Device{devP}, UI: true, UICritical: true},
	"M, UI":       {Devices: []Device{devM}, UI: true},
	"M, UI+crit":  {Devices: []Device{devM}, UI: true, UICritical: true},
	"PM, UI":      {Devices: []Device{devP, devM}, UI: true},
	"PM, UI+crit": {Devices: []Device{devP, devM}, UI: true, UICritical: true},
}

// notified names the devices of a channel set: "-", "P", "M" or "PM".
func notified(services []string) string {
	out := ""
	for _, s := range services {
		switch s {
		case devP.Service:
			out += "P"
		case devM.Service:
			out += "M"
		default:
			out += "?"
		}
	}
	if out == "" {
		return "-"
	}
	return out
}

// Combination table "channels" (F2): every valid configuration × administrator now ×
// critical action. Ordinary requests go to every device, critical ones only to devices
// with critical actions; the UI only for an administrator, a critical request only with
// ui_critical.
func TestChannelsCombinations(t *testing.T) {
	cases := []struct {
		config          string
		admin, critical bool
		devices         string
		ui, reachable   bool
	}{
		{"P", false, false, "P", false, true},
		{"P", false, true, "P", false, true},
		{"P", true, false, "P", false, true},
		{"P", true, true, "P", false, true},

		{"M", false, false, "M", false, true},
		{"M", false, true, "-", false, false},
		{"M", true, false, "M", false, true},
		{"M", true, true, "-", false, false},

		{"PM", false, false, "PM", false, true},
		{"PM", false, true, "P", false, true},
		{"PM", true, false, "PM", false, true},
		{"PM", true, true, "P", false, true},

		{"-, UI", false, false, "-", false, false},
		{"-, UI", false, true, "-", false, false},
		{"-, UI", true, false, "-", true, true},
		{"-, UI", true, true, "-", false, false},

		{"-, UI+crit", false, false, "-", false, false},
		{"-, UI+crit", false, true, "-", false, false},
		{"-, UI+crit", true, false, "-", true, true},
		{"-, UI+crit", true, true, "-", true, true},

		{"P, UI", false, false, "P", false, true},
		{"P, UI", false, true, "P", false, true},
		{"P, UI", true, false, "P", true, true},
		{"P, UI", true, true, "P", false, true},

		{"P, UI+crit", false, false, "P", false, true},
		{"P, UI+crit", false, true, "P", false, true},
		{"P, UI+crit", true, false, "P", true, true},
		{"P, UI+crit", true, true, "P", true, true},

		{"M, UI", false, false, "M", false, true},
		{"M, UI", false, true, "-", false, false},
		{"M, UI", true, false, "M", true, true},
		{"M, UI", true, true, "-", false, false},

		{"M, UI+crit", false, false, "M", false, true},
		{"M, UI+crit", false, true, "-", false, false},
		{"M, UI+crit", true, false, "M", true, true},
		{"M, UI+crit", true, true, "-", true, true},

		{"PM, UI", false, false, "PM", false, true},
		{"PM, UI", false, true, "P", false, true},
		{"PM, UI", true, false, "PM", true, true},
		{"PM, UI", true, true, "P", false, true},

		{"PM, UI+crit", false, false, "PM", false, true},
		{"PM, UI+crit", false, true, "P", false, true},
		{"PM, UI+crit", true, false, "PM", true, true},
		{"PM, UI+crit", true, true, "P", true, true},
	}
	if len(cases) != len(configs)*4 {
		t.Fatalf("%d cases, want every combination (%d)", len(cases), len(configs)*4)
	}
	for _, c := range cases {
		ap, ok := configs[c.config]
		if !ok {
			t.Fatalf("unknown config %q", c.config)
		}
		ap.UserID = u1
		if err := ap.validate(); err != nil {
			t.Fatalf("%s is no valid configuration: %v", c.config, err)
		}
		got := ap.Channels(c.critical, c.admin)
		if notified(got.Devices) != c.devices || got.UI != c.ui || got.Reachable() != c.reachable {
			t.Errorf("%s, admin=%v, critical=%v: devices %s, ui=%v, reachable=%v; want %s, %v, %v", c.config, c.admin, c.critical,
				notified(got.Devices), got.UI, got.Reachable(), c.devices, c.ui, c.reachable)
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
		{"P", false, true, true},
		{"P", true, true, true},
		{"M", false, true, false},
		{"M", true, true, false},
		{"PM", false, true, true},
		{"PM", true, true, true},
		{"-, UI", false, false, false},
		{"-, UI", true, true, false},
		{"-, UI+crit", false, false, false},
		{"-, UI+crit", true, true, true},
		{"P, UI", false, true, true},
		{"P, UI", true, true, true},
		{"P, UI+crit", false, true, true},
		{"P, UI+crit", true, true, true},
		{"M, UI", false, true, false},
		{"M, UI", true, true, false},
		{"M, UI+crit", false, true, false},
		{"M, UI+crit", true, true, true},
		{"PM, UI", false, true, true},
		{"PM, UI", true, true, true},
		{"PM, UI+crit", false, true, true},
		{"PM, UI+crit", true, true, true},
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

// Combination table "reach by channel" (decision S9): the settings show per kind of
// request whether it reaches the person by push, only in the UI, or not at all.
func TestReachByCombinations(t *testing.T) {
	cases := []struct {
		config           string
		admin            bool
		normal, critical string
	}{
		{"P", false, "push", "push"},
		{"P", true, "push", "push"},
		{"M", false, "push", "none"},
		{"M", true, "push", "none"},
		{"PM", false, "push", "push"},
		{"PM", true, "push", "push"},
		{"-, UI", false, "none", "none"},
		{"-, UI", true, "ui", "none"},
		{"-, UI+crit", false, "none", "none"},
		{"-, UI+crit", true, "ui", "ui"},
		{"P, UI", false, "push", "push"},
		{"P, UI", true, "push", "push"},
		{"P, UI+crit", false, "push", "push"},
		{"P, UI+crit", true, "push", "push"},
		{"M, UI", false, "push", "none"},
		{"M, UI", true, "push", "none"},
		{"M, UI+crit", false, "push", "none"},
		{"M, UI+crit", true, "push", "ui"},
		{"PM, UI", false, "push", "push"},
		{"PM, UI", true, "push", "push"},
		{"PM, UI+crit", false, "push", "push"},
		{"PM, UI+crit", true, "push", "push"},
	}
	if len(cases) != len(configs)*2 {
		t.Fatalf("%d cases, want %d", len(cases), len(configs)*2)
	}
	for _, c := range cases {
		normal, critical := configs[c.config].ReachBy(c.admin)
		if normal != c.normal || critical != c.critical {
			t.Errorf("%s, admin=%v: %s/%s, want %s/%s", c.config, c.admin, normal, critical, c.normal, c.critical)
		}
		// Consistent with Reach: "none" exactly when unreachable.
		r := configs[c.config].Reach(c.admin)
		if r.Normal != (normal != ReachNone) || r.Critical != (critical != ReachNone) {
			t.Errorf("%s, admin=%v: ReachBy and Reach disagree", c.config, c.admin)
		}
	}
}

func TestApproversReportDatabaseErrors(t *testing.T) {
	a, closeDB := newApprovers(t)
	_ = closeDB()
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u1, Devices: phones("mobile_app_x")}); err == nil {
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

// The first change wins: a change based on an older version is refused and stores
// nothing; every change, also of a detail, makes a new version.
func TestApproverVersions(t *testing.T) {
	a, _ := newApprovers(t)
	ctx := context.Background()
	v0, err := a.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	anna := Approver{UserID: "anna", Devices: phones("mobile_app_anna")}
	if err := a.PutIf(ctx, anna, v0); err != nil {
		t.Fatal(err)
	}
	v1, _ := a.Version(ctx)
	if v1 == v0 || len(v1) != 32 {
		t.Fatalf("versions %q → %q", v0, v1)
	}
	// Someone else still on v0: refused, nothing stored.
	if err := a.PutIf(ctx, Approver{UserID: "bob", UI: true}, v0); !errors.Is(err, ErrApproversChanged) {
		t.Errorf("PutIf on an old version = %v", err)
	}
	if err := a.RemoveIf(ctx, "anna", v0); !errors.Is(err, ErrApproversChanged) {
		t.Errorf("RemoveIf on an old version = %v", err)
	}
	if list, _ := a.List(ctx); len(list) != 1 || list[0].UserID != "anna" {
		t.Errorf("list = %+v", list)
	}
	// Details count: only the critical switch of a device changes.
	anna.Devices[0].Critical = false
	if err := a.PutIf(ctx, anna, v1); err != nil {
		t.Fatal(err)
	}
	v2, _ := a.Version(ctx)
	if v2 == v1 {
		t.Error("a changed device switch kept the version")
	}
	// The same approvers have the same version.
	if again, _ := a.Version(ctx); again != v2 {
		t.Error("version not stable")
	}
	if err := a.RemoveIf(ctx, "nobody", v2); !errors.Is(err, ErrApproverNotFound) {
		t.Errorf("RemoveIf(unknown) = %v", err)
	}
	if err := a.RemoveIf(ctx, "anna", v2); err != nil {
		t.Fatal(err)
	}
	if v3, _ := a.Version(ctx); v3 != v0 {
		t.Errorf("empty again: %q, want %q", v3, v0)
	}
}

// Two changes based on the same version at the same time: exactly one is stored.
func TestApproverVersionRace(t *testing.T) {
	a, _ := newApprovers(t)
	ctx := context.Background()
	v, _ := a.Version(ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			errs <- a.PutIf(ctx, Approver{UserID: fmt.Sprintf("user%d", i), UI: true}, v)
		})
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrApproversChanged):
			t.Errorf("unexpected %v", err)
		}
	}
	if list, _ := a.List(ctx); ok != 1 || len(list) != 1 {
		t.Errorf("%d stored, list %d", ok, len(list))
	}
}

func TestApproverVersionsReportDatabaseErrors(t *testing.T) {
	a, closeDB := newApprovers(t)
	_ = closeDB()
	ctx := context.Background()
	if _, err := a.Version(ctx); err == nil {
		t.Error("Version succeeded")
	}
	if err := a.PutIf(ctx, Approver{UserID: "anna", UI: true}, "x"); err == nil {
		t.Error("PutIf succeeded")
	}
	if err := a.RemoveIf(ctx, "anna", "x"); err == nil {
		t.Error("RemoveIf succeeded")
	}
}
