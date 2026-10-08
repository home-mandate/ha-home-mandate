// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"slices"
	"testing"
)

const (
	ownUser = "9f8e7d6c5b4a39281706f5e4d3c2b1a0" // Home-Mandate's own Home Assistant user
	unset   = "0a1b2c3d4e5f60718293a4b5c6d7e8f9" // no approver set up
)

func admins(ids ...string) func(context.Context, string) (bool, error) {
	return func(_ context.Context, user string) (bool, error) { return slices.Contains(ids, user), nil }
}

func TestReachOfPeople(t *testing.T) {
	a, _ := newApprovers(t)
	ctx := context.Background()
	for _, ap := range []Approver{
		{UserID: u1, Devices: []Device{{Service: "mobile_app_iphone", Critical: true}}},
		{UserID: u2, Devices: []Device{{Service: "mobile_app_pixel"}}},
		{UserID: u3, UI: true},
		{UserID: ownUser, Devices: phones("mobile_app_service")},
	} {
		if err := a.Put(ctx, ap); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.ReachOf(ctx, Expected{People: []string{u1, u2, u3, unset, ownUser}}, ownUser, admins(u3))
	if err != nil {
		t.Fatal(err)
	}
	want := []PersonReach{
		{UserID: u1, Normal: ReachPush, Critical: ReachPush},
		{UserID: u2, Normal: ReachPush, Critical: ReachNone},
		{UserID: u3, Normal: ReachUI, Critical: ReachNone},
		{UserID: unset, Normal: ReachNone, Critical: ReachNone},
		// Home-Mandate's own user is never asked, whatever is set up for it.
		{UserID: ownUser, Normal: ReachNone, Critical: ReachNone, Service: true},
	}
	if !slices.Equal(got.People, want) {
		t.Errorf("people = %+v\nwant %+v", got.People, want)
	}
	if got.Normal != CoverageNotNeeded || got.Critical != CoverageNotNeeded {
		t.Errorf("coverage = %s %s, want not needed", got.Normal, got.Critical)
	}
}

func TestReachOfCoverage(t *testing.T) {
	a, _ := newApprovers(t)
	ctx := context.Background()
	for _, ap := range []Approver{
		{UserID: u1, Devices: []Device{{Service: "mobile_app_iphone", Critical: true}}},
		{UserID: u2, Devices: []Device{{Service: "mobile_app_pixel"}}},
		{UserID: u3, UI: true}, // only while an administrator
		{UserID: ownUser, Devices: []Device{{Service: "mobile_app_service", Critical: true}}},
	} {
		if err := a.Put(ctx, ap); err != nil {
			t.Fatal(err)
		}
	}
	failing := func(context.Context, string) (bool, error) { return false, errors.New("home assistant unavailable") }
	cases := []struct {
		name             string
		normal, critical [][]string
		isAdmin          func(context.Context, string) (bool, error)
		wantN, wantC     string
	}{
		{"one reachable person is enough", [][]string{{unset, u2}}, [][]string{{u2, u1}}, admins(), CoverageReachable, CoverageReachable},
		{"nobody with a channel", [][]string{{unset}}, nil, admins(), CoverageNobody, CoverageNotNeeded},
		{"an empty list is nobody", [][]string{{}}, [][]string{{}}, admins(), CoverageNobody, CoverageNobody},
		{"only an ordinary phone for critical requests", [][]string{{u2}}, [][]string{{u2}}, admins(), CoverageReachable, CoverageNobody},
		{"one list without anyone is enough to warn", [][]string{{u1}, {unset}}, nil, admins(), CoverageNobody, CoverageNotNeeded},
		{"the ownUser user never counts", [][]string{{ownUser}}, [][]string{{ownUser}}, admins(ownUser), CoverageNobody, CoverageNobody},
		{"the UI only for an administrator", [][]string{{u3}}, nil, admins(), CoverageNobody, CoverageNotNeeded},
		{"the UI of an administrator", [][]string{{u3}}, nil, admins(u3), CoverageReachable, CoverageNotNeeded},
		{"Home Assistant not asked: unknown, never reachable", [][]string{{u1}}, [][]string{{u1}}, failing, CoverageUnknown, CoverageUnknown},
		{"unknown, but nobody else either", [][]string{{u1, unset}}, nil, failing, CoverageUnknown, CoverageNotNeeded},
		{"nobody in one list wins over unknown in another", [][]string{{u1}, {unset}}, nil, failing, CoverageNobody, CoverageNotNeeded},
		{"without an administrator check", [][]string{{u1}}, nil, nil, CoverageUnknown, CoverageNotNeeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := a.ReachOf(ctx, Expected{Normal: c.normal, Critical: c.critical}, ownUser, c.isAdmin)
			if err != nil {
				t.Fatal(err)
			}
			if got.Normal != c.wantN || got.Critical != c.wantC {
				t.Errorf("coverage = %s %s, want %s %s", got.Normal, got.Critical, c.wantN, c.wantC)
			}
		})
	}
}

func TestReachOfUnknown(t *testing.T) {
	a, _ := newApprovers(t)
	ctx := context.Background()
	if err := a.Put(ctx, Approver{UserID: u1, Devices: phones("mobile_app_pixel")}); err != nil {
		t.Fatal(err)
	}
	failing := func(context.Context, string) (bool, error) { return false, errors.New("home assistant unavailable") }
	got, err := a.ReachOf(ctx, Expected{People: []string{u1, unset}}, "", failing)
	if err != nil {
		t.Fatal(err)
	}
	want := []PersonReach{{UserID: u1, Normal: ReachUnknown, Critical: ReachUnknown}, {UserID: unset, Normal: ReachNone, Critical: ReachNone}}
	if !slices.Equal(got.People, want) {
		t.Errorf("people = %+v", got.People)
	}
}

func TestReachOfReportsDatabaseErrors(t *testing.T) {
	a, closeDB := newApprovers(t)
	_ = closeDB()
	if _, err := a.ReachOf(context.Background(), Expected{People: []string{u1}}, "", admins()); err == nil {
		t.Error("no error")
	}
}
