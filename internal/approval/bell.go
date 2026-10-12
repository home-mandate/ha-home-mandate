// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// markdownUnsafe are the characters that could end a Markdown link or start another
// construct inside it.
const markdownUnsafe = "()[]<>\\`\"'"

// bellText is the bell's message (B2): where a request can be answered, never anything
// about the request. Home Assistant shows it to every user, so it holds at most the UI's
// address (issue #48). The app's UI is in the sidebar; a container's UI has an address
// of its own only in direct mode; without one, the bell names the UI without a link.
func bellText(lang i18n.Lang, app bool, link string) string {
	switch {
	case app:
		return i18n.T(lang, i18n.ApprovalBellMessage, nil)
	case linkable(link):
		return i18n.T(lang, i18n.ApprovalBellMessageLink, i18n.Args{"url": link})
	default:
		return i18n.T(lang, i18n.ApprovalBellMessageUI, nil)
	}
}

// linkable tells whether link is an absolute http(s) URL without user info, query or
// fragment that can stand in a Markdown link as it is. The UI's address is the validated
// public origin plus the UI's path, so this always holds for it; anything else is left
// out rather than escaped.
func linkable(link string) bool {
	if link == "" || strings.ContainsAny(link, markdownUnsafe) || strings.ContainsFunc(link, isSpaceOrControl) {
		return false
	}
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Opaque == "" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(link, "#")
}

func isSpaceOrControl(r rune) bool {
	return r <= ' ' || r == 0x7f || unicode.IsSpace(r) || unicode.IsControl(r)
}

// uiAddress is the UI's address from the hook, "" without one.
func (s *Service) uiAddress() string {
	if s.cfg.UIAddress == nil {
		return ""
	}
	return s.cfg.UIAddress()
}
