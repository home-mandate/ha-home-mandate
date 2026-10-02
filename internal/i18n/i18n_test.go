// SPDX-License-Identifier: AGPL-3.0-or-later

package i18n

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

func catalogKeys(t *testing.T, lang Lang) []string {
	t.Helper()
	data, err := files.ReadFile("messages/" + string(lang) + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

// TestCatalogsMatchDeclaredKeys: every declared key exists in every language and no
// catalog has keys that are not declared (orphans).
func TestCatalogsMatchDeclaredKeys(t *testing.T) {
	declared := make([]string, len(Keys))
	for i, k := range Keys {
		declared[i] = string(k)
	}
	slices.Sort(declared)
	if dup := slices.Compact(slices.Clone(declared)); len(dup) != len(declared) {
		t.Fatal("Keys contains duplicates")
	}
	for _, lang := range Supported {
		if got := catalogKeys(t, lang); !slices.Equal(got, declared) {
			t.Errorf("%s keys = %v\nwant %v", lang, got, declared)
		}
	}
}

func TestPlaceholdersAreTheSameInAllLanguages(t *testing.T) {
	for _, key := range Keys {
		want := placeholders(catalogs[Default][key])
		for _, lang := range Supported {
			if got := placeholders(catalogs[lang][key]); !slices.Equal(got, want) {
				t.Errorf("%s %s placeholders = %v, want %v", lang, key, got, want)
			}
		}
	}
}

func TestMessagesAreNotEmptyAndHaveNoControlCharacters(t *testing.T) {
	for _, lang := range Supported {
		for _, key := range Keys {
			msg := catalogs[lang][key]
			if strings.TrimSpace(msg) == "" {
				t.Errorf("%s %s is empty", lang, key)
			}
			if strings.ContainsFunc(msg, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				t.Errorf("%s %s contains control characters", lang, key)
			}
		}
	}
}

func TestTSubstitutesPlaceholders(t *testing.T) {
	got := T(DE, ApprovalTitle, Args{"agent": "Sprachassistent"})
	if got != "Freigabe nötig: Sprachassistent" {
		t.Errorf("T = %q", got)
	}
	got = T(EN, ApprovalTitle, Args{"agent": "Voice"})
	if got != "Approval needed: Voice" {
		t.Errorf("T = %q", got)
	}
}

// An argument that looks like a placeholder is inserted literally, never expanded again.
func TestTDoesNotExpandArguments(t *testing.T) {
	got := T(EN, ApprovalMessage, Args{"agent": "{device}", "action": "unlock", "device": "Front door"})
	if !strings.Contains(got, "{device} wants") || strings.Count(got, "Front door") != 1 {
		t.Errorf("T = %q", got)
	}
}

func TestTLeavesMissingArgumentsVisible(t *testing.T) {
	if got := T(EN, ApprovalTitle, nil); got != "Approval needed: {agent}" {
		t.Errorf("T = %q", got)
	}
}

func TestTFallsBackForUnknownLanguageAndKey(t *testing.T) {
	if got := T("fr", ApprovalDeny, nil); got != "Deny" {
		t.Errorf("unknown language: %q", got)
	}
	if got := T(DE, Key("no_such_key"), nil); got != "no_such_key" {
		t.Errorf("unknown key: %q", got)
	}
}

func TestParse(t *testing.T) {
	tests := map[string]struct {
		lang Lang
		ok   bool
	}{
		"de": {DE, true}, "de-DE": {DE, true}, "de_AT": {DE, true}, "DE-ch": {DE, true},
		"en": {EN, true}, "en-US": {EN, true}, "EN": {EN, true},
		"fr": {EN, false}, "": {EN, false}, "deu": {EN, false}, "d": {EN, false}, "-de": {EN, false},
	}
	for tag, want := range tests {
		lang, ok := Parse(tag)
		if lang != want.lang || ok != want.ok {
			t.Errorf("Parse(%q) = %q, %v; want %q, %v", tag, lang, ok, want.lang, want.ok)
		}
	}
}

func TestPickTakesTheFirstSupported(t *testing.T) {
	if got := Pick("fr", "", "de-DE", "en"); got != DE {
		t.Errorf("Pick = %q", got)
	}
	if got := Pick("fr", "it"); got != Default {
		t.Errorf("Pick without match = %q", got)
	}
	if got := Pick(); got != Default {
		t.Errorf("Pick() = %q", got)
	}
}

func TestActionNamesCoverTheVocabulary(t *testing.T) {
	actions := []string{"read", "turn_on", "turn_off", "set", "set_temperature", "set_mode", "open", "close", "stop",
		"set_position", "lock", "unlock", "arm", "disarm", "snapshot", "play", "pause", "set_volume", "activate", "run"}
	for _, action := range actions {
		for _, lang := range Supported {
			want, ok := catalogs[lang][Key("action_"+action)]
			if got := ActionName(lang, action); !ok || got != want {
				t.Errorf("%s: display name of %s = %q, catalog entry present: %v", lang, action, got, ok)
			}
		}
	}
	if got := ActionName(DE, "unlock"); got != "entriegeln" {
		t.Errorf("ActionName(de, unlock) = %q", got)
	}
	if got := ActionName(DE, "teleport"); got != "teleport" {
		t.Errorf("unknown action = %q, want the identifier", got)
	}
}
