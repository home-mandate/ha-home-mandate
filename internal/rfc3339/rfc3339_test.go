// SPDX-License-Identifier: AGPL-3.0-or-later

package rfc3339

import (
	"testing"
	"time"
)

func TestParseAccepts(t *testing.T) {
	want := time.Date(2026, 10, 12, 4, 30, 0, 500_000_000, time.UTC)
	for _, s := range []string{"2026-10-12T04:30:00.5Z", "2026-10-12T06:30:00.5+02:00", "2026-10-11T04:31:00.5-23:59"} {
		if got, err := Parse(s); err != nil || !got.Equal(want) {
			t.Errorf("Parse(%s) = %v, %v", s, got, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{
		"", "not-a-time", "2026-10-12", "2026-10-12T04:30:00", "2026-10-12T4:30:00Z", "2026-10-12T04:30:00,5Z",
		"2026-10-12T04:30:00+24:00", "2026-10-12T04:30:00-24:00", "2026-10-12T04:30:00+23:60", "2026-10-12T04:30:00+00:60",
		"2026-02-30T12:00:00Z", "2016-12-31T23:59:60Z", "2026-10-12T04:30:00Z\n",
	} {
		if got, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", s, got)
		}
	}
}
