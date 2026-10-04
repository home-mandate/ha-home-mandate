// SPDX-License-Identifier: AGPL-3.0-or-later

package untrusted

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

type vector struct {
	Input  string  `json:"input"`
	Clean  *string `json:"clean"`
	Folded string  `json:"folded"`
}

// The UI cleans the search text with cleanSearch before sending it; the server cleans and
// folds it again. Both suites run the same vectors (TESTING.md section 4, audit log).
func TestSharedSearchVectors(t *testing.T) {
	data, err := os.ReadFile("../../web/src/lib/audit/search-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Search []vector `json:"search"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Search) < 20 {
		t.Fatalf("only %d vectors", len(file.Search))
	}
	for _, v := range file.Search {
		clean, ok := CleanSearch(v.Input)
		if v.Clean == nil {
			if ok {
				t.Errorf("CleanSearch(%q) = %q, want refused", v.Input, clean)
			}
			continue
		}
		if !ok || clean != *v.Clean {
			t.Errorf("CleanSearch(%q) = %q, %v; want %q", v.Input, clean, ok, *v.Clean)
		}
		if got := Fold(clean); got != v.Folded {
			t.Errorf("Fold(%q) = %q, want %q", clean, got, v.Folded)
		}
	}
}

func TestCleanSearchRefusesInvalidUTF8AndHugeInput(t *testing.T) {
	for _, in := range []string{"\xff", "ab\xc3", strings.Repeat("a", searchMaxBytes+1)} {
		if got, ok := CleanSearch(in); ok {
			t.Errorf("CleanSearch(%q) = %q, want refused", in, got)
		}
	}
}

// Clean must give what cleanUntrusted in web/src/lib/untrusted.ts gives (its tests use the
// same inputs).
func TestClean(t *testing.T) {
	long := strings.Repeat("ab", 300)
	tests := []struct{ in, want string }{
		{"", ""},
		{"Voice Assistant", "Voice Assistant"},
		{"  Küche\n\tLicht  ", "Küche Licht"},
		{"\u202egnalnegrom", "gnalnegrom"},
		{"a\u2066b\u2069c\u200bd\u00ade\ufefff", "abcdef"},
		{"x\u2028y\u2029z\u0085w", "x y z w"},
		{"Z\u0301\u0302\u0303\u0304o", "Z\u0301\u0302o"},
		{"e\u0301", "e\u0301"},
		{"\u3164\u115f\u1160\uffa0\u2800", ""},
		{"a\ufe0fb\U000E0100c\u180bd", "abcd"},
		{"x\u200b\u0301\u0302\u0303y", "x\u0301\u0302y"},
		{"bad\xffbyte", "bad�byte"},
		{"a \u00a0 \u3000 b", "a b"},
		{long, long[:499] + "…"},
	}
	for _, tc := range tests {
		if got := Clean(tc.in, Max); got != tc.want {
			t.Errorf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := Clean(strings.Repeat("🚪", 10), 5); got != "🚪🚪🚪🚪…" || utf8.RuneCountInString(got) != 5 {
		t.Errorf("Clean cuts to %q", got)
	}
}

func TestFoldOnlyWidens(t *testing.T) {
	// Folding the haystack and the needle alike: every original match still matches.
	pairs := [][2]string{{"Haustür", "tür"}, {"Straße", "ß"}, {"ΣΟΦΟΣ", "σοφος"}, {"Ünïcödé", "ÜNÏ"}, {"İzmir", "İz"}}
	for _, p := range pairs {
		if !strings.Contains(Fold(p[0]), Fold(p[1])) {
			t.Errorf("Fold(%q) does not contain Fold(%q)", p[0], p[1])
		}
	}
	if Fold("Ab") == Fold("Ac") {
		t.Error("Fold merges different letters")
	}
}
