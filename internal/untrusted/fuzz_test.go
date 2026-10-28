// SPDX-License-Identifier: AGPL-3.0-or-later

package untrusted

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzClean: agents and Home Assistant entities choose names and reasons freely. Clean
// returns valid UTF-8 of at most max characters without hidden or blank characters,
// breaks or runs of spaces, and cleaning its result again changes nothing; CleanSearch
// and Fold do not panic and keep valid UTF-8.
func FuzzClean(f *testing.F) {
	for _, seed := range []string{
		"Voice assistant", "Haustür\u202e", "  a\r\n\tb  ", "e\u0301\u0301\u0301\u0301", "\u3164\u3164", "\xff\xfe",
		"\u200b\ufeffAdmin", "ẞtraße İı", strings.Repeat("x", 600), "\u2028\u2029\u0085", "a\u00a0\u3000b",
	} {
		f.Add(seed, uint16(Max))
	}
	f.Fuzz(func(t *testing.T, text string, limit uint16) {
		max := 1 + int(limit)%(2*Max)
		out := Clean(text, max)
		if !utf8.ValidString(out) || utf8.RuneCountInString(out) > max {
			t.Fatalf("Clean(%q, %d) = %q", text, max, out)
		}
		if strings.ContainsFunc(out, func(r rune) bool { return isHidden(r) || isBlank(r) || isBreak(r) || isSpace(r) && r != ' ' }) ||
			strings.Contains(out, "  ") || strings.HasPrefix(out, " ") {
			t.Fatalf("Clean(%q) = %q keeps what it must remove", text, out)
		}
		if !strings.HasSuffix(out, "…") && Clean(out, max) != out {
			t.Fatalf("Clean is not idempotent on %q: %q", out, Clean(out, max))
		}
		if s, ok := CleanSearch(text); ok && (!utf8.ValidString(s) || utf8.RuneCountInString(s) > SearchMax) {
			t.Fatalf("CleanSearch(%q) = %q", text, s)
		}
		if folded := Fold(out); !utf8.ValidString(folded) {
			t.Fatalf("Fold(%q) = %q", out, folded)
		}
	})
}
