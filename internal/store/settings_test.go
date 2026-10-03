// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"context"
	"regexp"
	"testing"
)

func TestSettingRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if _, ok, err := s.Setting(ctx, "k"); err != nil || ok {
		t.Fatalf("Setting(missing) = ok %v, err %v", ok, err)
	}
	if err := s.SetSetting(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Setting(ctx, "k"); err != nil || !ok || v != "v2" {
		t.Errorf("Setting = %q, %v, %v; want v2", v, ok, err)
	}
	for range 2 { // deleting a missing key is no error
		if err := s.DeleteSetting(ctx, "k"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := s.Setting(ctx, "k"); err != nil || ok {
		t.Errorf("after delete: ok %v, err %v", ok, err)
	}
}

func TestHouseholdIsCreatedOnceAndMatchesTheSpec(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()

	first, err := s.Household(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// SPEC-v0 schema: ^(household|person):[A-Za-z0-9_-]{1,64}$
	if !regexp.MustCompile(`^household:hm-[0-9a-f]{12}$`).MatchString(first) {
		t.Errorf("household = %q", first)
	}
	again, err := s.Household(ctx)
	if err != nil || again != first {
		t.Errorf("second Household = %q, %v; want %q", again, err, first)
	}
	s.Close()
	reopened := reopen(t, path)
	if after, err := reopened.Household(ctx); err != nil || after != first {
		t.Errorf("after reopen = %q, %v; want %q", after, err, first)
	}
}

func TestSettingsReportDatabaseErrors(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	s.Close()

	if _, _, err := s.Setting(ctx, "k"); err == nil {
		t.Error("Setting on a closed store succeeded")
	}
	if err := s.SetSetting(ctx, "k", "v"); err == nil {
		t.Error("SetSetting on a closed store succeeded")
	}
	if _, err := s.Household(ctx); err == nil {
		t.Error("Household on a closed store succeeded")
	}
	if err := s.DeleteSetting(ctx, "k"); err == nil {
		t.Error("DeleteSetting on a closed store succeeded")
	}
	if err := s.SetSettings(ctx, map[string]string{"k": "v"}); err == nil {
		t.Error("SetSettings on a closed store succeeded")
	}
}

func TestSetSettingsIsAllOrNothing(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if err := s.SetSettings(ctx, map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER no_c BEFORE INSERT ON settings WHEN NEW.key = 'c' BEGIN SELECT RAISE(ABORT, 'x'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSettings(ctx, map[string]string{"a": "9", "c": "3"}); err == nil {
		t.Fatal("SetSettings succeeded")
	}
	if v, _, _ := s.Setting(ctx, "a"); v != "1" {
		t.Errorf("a = %q after a failed write", v)
	}
}
