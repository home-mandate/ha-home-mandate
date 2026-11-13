// SPDX-License-Identifier: AGPL-3.0-or-later

package untrusted

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// markup are characters that format text or build links in Markdown or HTML. The
// underscore stays: it is part of identifiers such as hvac_mode.
const markup = "*~`[]()<>#|\\{}"

// ForNotification makes untrusted text safe for a notification or a message to a human
// (an agent's reason, the issuer of a certificate from a public log): control, format
// and separator characters and markup become spaces, "://" is broken so that no link
// can be clicked, whitespace is collapsed and the text is cut to limit runes (limit ≥ 1).
func ForNotification(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) || r == utf8.RuneError ||
			strings.ContainsRune(markup, r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(strings.ReplaceAll(b.String(), "://", " ")), " ")
	if utf8.RuneCountInString(out) > limit {
		out = string([]rune(out)[:limit-1]) + "…"
	}
	return out
}
