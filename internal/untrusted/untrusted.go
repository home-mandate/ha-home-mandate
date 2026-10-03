// SPDX-License-Identifier: AGPL-3.0-or-later

// Package untrusted cleans text that agents or Home Assistant entities chose, exactly as
// the UI does (web/src/lib/untrusted.ts and audit/filters.ts), and folds it for the
// search of the audit log. The UI cleans for display; the server cleans the same way
// where it compares, so that a search finds what the person sees. Shared test vectors
// live in web/src/lib/audit/search-vectors.json and are run by both test suites.
package untrusted

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// Max is the longest untrusted text in the UI (UNTRUSTED_MAX).
	Max = 500
	// SearchMax is the longest search text in characters after cleaning (SEARCH_MAX).
	SearchMax = 100
	// searchMaxBytes refuses huge input before any work; far above SearchMax anyway.
	searchMaxBytes = 2048
)

// breaks become one space before anything else (JavaScript: [\r\n\t\u0085\u2028\u2029]).
func isBreak(r rune) bool {
	switch r {
	case '\r', '\n', '\t', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

// isSpace is JavaScript's \s after controls and format characters are gone, plus the line
// and paragraph separators: the characters that are joined into one space.
func isSpace(r rune) bool {
	switch {
	case r == ' ', r == '\u00a0', r == '\u1680', r >= '\u2000' && r <= '\u200a', r == '\u2028', r == '\u2029',
		r == '\u202f', r == '\u205f', r == '\u3000', r == '\t', r == '\n', r == '\v', r == '\f', r == '\r', r == '\ufeff':
		return true
	}
	return false
}

// isHidden are controls (incl. C1) and format characters: bidi marks, overrides,
// isolates, zero-width characters, the soft hyphen, the BOM.
func isHidden(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf)
}

// isBlank are letters that render blank (Hangul fillers, braille blank) and variation
// selectors: they make a name look empty or different without being Cc or Cf.
func isBlank(r rune) bool {
	switch {
	case r == '\u115f', r == '\u1160', r == '\u3164', r == '\uffa0', r == '\u2800',
		r >= '\u180b' && r <= '\u180f', r >= '\ufe00' && r <= '\ufe0f', r >= 0xe0100 && r <= 0xe01ef:
		return true
	}
	return false
}

// Clean is cleanUntrusted: breaks become spaces, hidden and blank characters go, at most
// two combining marks stay on a character, whitespace runs become one space, and the
// text is cut to max characters with an ellipsis. Invalid UTF-8 becomes U+FFFD.
func Clean(text string, max int) string {
	var b strings.Builder
	marks, space := 0, false
	for _, r := range strings.ToValidUTF8(text, "�") {
		if isBreak(r) {
			r = ' '
		}
		if isHidden(r) || isBlank(r) {
			continue
		}
		if unicode.Is(unicode.M, r) {
			if marks++; marks > 2 {
				continue
			}
		} else {
			marks = 0
		}
		if isSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) <= max {
		return out
	}
	return string([]rune(out)[:max-1]) + "…"
}

// CleanSearch is cleanSearch: the search text with breaks as spaces, without hidden
// characters, whitespace runs joined and trimmed. It reports false for invalid UTF-8 and
// for text longer than SearchMax characters after cleaning.
func CleanSearch(text string) (string, bool) {
	if len(text) > searchMaxBytes || !utf8.ValidString(text) {
		return "", false
	}
	var b strings.Builder
	space := false
	for _, r := range text {
		if isBreak(r) {
			r = ' '
		}
		if isHidden(r) {
			continue
		}
		if isSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	return out, utf8.RuneCountInString(out) <= SearchMax
}

// Fold makes text comparable for the search, ignoring case and accents: letters are
// lowered, precomposed letters lose their marks (é and e+U+0301 both become e), all
// combining marks go, and ß, ẞ, ς, ſ, İ and ı fold to ss, ss, σ, s, i and i. Folding can
// only widen a match, never narrow it.
func Fold(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch r {
		case 'ß', 'ẞ':
			b.WriteString("ss")
			continue
		case 'ς':
			r = 'σ'
		case 'ſ':
			r = 's'
		case 'İ', 'ı':
			r = 'i'
		}
		if unicode.Is(unicode.M, r) {
			continue
		}
		if base, ok := baseLetters[r]; ok {
			b.WriteString(base)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
