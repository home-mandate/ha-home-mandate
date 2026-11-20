// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// configEntries returns the config members of the config.changed entries of l, in order.
func configEntries(t *testing.T, l *audit.Log) []string {
	t.Helper()
	lines, _, events := exported(t, l)
	var out []string
	for i, e := range events {
		if e != audit.EventConfigChanged {
			continue
		}
		line := string(lines[i])
		start := strings.Index(line, `"config":`)
		end := strings.Index(line[start:], "}")
		out = append(out, line[start+len(`"config":`):start+end+1])
	}
	return out
}

// SPEC-v0 section 9.2: every change of a setting is recorded with its old and new value. The
// first value Home-Mandate sees is where it starts from, not a change (the value it had
// before is not known); a setting that is removed is recorded without a value.
func TestSettingsAreRecordedWhenTheyChange(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	record := func(setting, value string) {
		t.Helper()
		if err := l.RecordSetting(ctx, setting, value); err != nil {
			t.Fatalf("RecordSetting(%s, %q): %v", setting, value, err)
		}
	}
	record(audit.SettingTimezone, "Europe/Berlin")
	record(audit.SettingTimezone, "Europe/Berlin")
	record(audit.SettingPublicOrigin, "")
	record(audit.SettingTimezone, "Europe/Vienna")
	record(audit.SettingPublicOrigin, "https://hm.example.org")
	record(audit.SettingPublicOrigin, "https://hm.example.org:8443")
	record(audit.SettingPublicOrigin, "")
	want := []string{
		`{"previous_value":"Europe/Berlin","setting":"timezone","value":"Europe/Vienna"}`,
		`{"setting":"public_origin","value":"https://hm.example.org"}`,
		`{"previous_value":"https://hm.example.org","setting":"public_origin","value":"https://hm.example.org:8443"}`,
		`{"previous_value":"https://hm.example.org:8443","setting":"public_origin"}`,
	}
	if got := configEntries(t, l); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("config.changed:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r, err := l.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("Verify = %+v, %v", r, err)
	}
}

// A value the log cannot record is refused, and the setting keeps the value it had, so
// that the change is recorded once the value can be.
func TestASettingThatCannotBeRecordedStaysAsItWas(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	if err := l.RecordSetting(ctx, audit.SettingPublicOrigin, "https://hm.example.org"); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordSetting(ctx, audit.SettingPublicOrigin, "https://192.0.2.1"); err == nil {
		t.Fatal("an IP address was recorded as the public origin")
	}
	if err := l.RecordSetting(ctx, audit.SettingPublicOrigin, "https://hm.example.net"); err != nil {
		t.Fatal(err)
	}
	if got := configEntries(t, l); len(got) != 1 || !strings.Contains(got[0], `"previous_value":"https://hm.example.org"`) {
		t.Errorf("config.changed: %v", got)
	}
}

// Trusted proxies are a set: each one added or removed is one entry, an added one with its
// value alone and a removed one with its previous value alone (SPEC-v0 section 9.1).
func TestTrustedProxiesAreRecordedOneByOne(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	set := func(proxies ...string) {
		t.Helper()
		if err := l.RecordSettings(ctx, audit.SettingTrustedProxy, proxies); err != nil {
			t.Fatalf("RecordSettings(%v): %v", proxies, err)
		}
	}
	set("192.0.2.10")
	set("192.0.2.10")
	set("2001:db8::1")
	set()
	set("10.0.0.0/8", "192.0.2.10")
	want := []string{
		`{"previous_value":"192.0.2.10","setting":"trusted_proxy"}`,
		`{"setting":"trusted_proxy","value":"2001:db8::1"}`,
		`{"previous_value":"2001:db8::1","setting":"trusted_proxy"}`,
		`{"setting":"trusted_proxy","value":"10.0.0.0/8"}`,
		`{"setting":"trusted_proxy","value":"192.0.2.10"}`,
	}
	if got := configEntries(t, l); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("config.changed:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// ct.status is recorded at every change of the state, the first one without a previous
// state (SPEC-v0 section 9.1).
func TestTheStateOfCTMonitoringIsRecordedAtEveryChange(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	for _, s := range []string{audit.CTLimited, audit.CTLimited, audit.CTOK, audit.CTUnavailable} {
		if err := l.RecordCTStatus(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	lines, _, events := exported(t, l)
	want := []string{`"ct":{"status":"limited"}`, `"ct":{"previous_status":"limited","status":"ok"}`, `"ct":{"previous_status":"ok","status":"unavailable"}`}
	if len(events) != len(want) {
		t.Fatalf("%d entries, want %d", len(events), len(want))
	}
	for i, w := range want {
		if !strings.Contains(string(lines[i]), w) {
			t.Errorf("entry %d = %s, want %s", i+1, lines[i], w)
		}
	}
}

// A setting stored in a form the log cannot read, or a database that is gone, is an error,
// never a silent change.
func TestRecordingSettingsFails(t *testing.T) {
	ctx := context.Background()
	l, db := newLog(t)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('audit_config_trusted_proxy', 'not a list', '2026-11-20T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordSettings(ctx, audit.SettingTrustedProxy, []string{"192.0.2.1"}); err == nil {
		t.Error("a stored value that is no list was taken")
	}
	if err := l.RecordSettings(ctx, audit.SettingTrustedProxy, []string{"192.0.2.1/32"}); err == nil {
		t.Error("a value the log cannot record was recorded")
	}
	// A stored proxy in a form the log cannot record cannot be recorded as removed either.
	if _, err := db.Exec(`UPDATE settings SET value = '["192.0.2.1/32"]' WHERE key = 'audit_config_trusted_proxy'`); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordSettings(ctx, audit.SettingTrustedProxy, nil); err == nil {
		t.Error("the removal of a value the log cannot record was recorded")
	}
	_ = db.Close()
	for name, call := range map[string]func() error{
		"setting":  func() error { return l.RecordSetting(ctx, audit.SettingTimezone, "UTC") },
		"settings": func() error { return l.RecordSettings(ctx, audit.SettingTrustedProxy, nil) },
		"ct":       func() error { return l.RecordCTStatus(ctx, audit.CTOK) },
		"export":   func() error { return l.ExportRecorded(ctx, io.Discard, audit.Actor{Kind: audit.ActorUser, ID: "u"}) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s without a database: no error", name)
		}
	}
}
