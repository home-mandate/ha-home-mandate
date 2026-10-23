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

// bellText is the bell's message (B2): where a request can be answered, never what it is
// about. Home Assistant shows it to every user, so it holds at most the link of the
// request's page, with the request's random ID (issues #48, #53). The app's UI is in the
// sidebar; a container's UI has an address of its own only in direct mode; without one,
// the bell names the UI without a link.
func bellText(lang i18n.Lang, app bool, link string) string {
	switch {
	case app && linkablePath(link):
		return i18n.T(lang, i18n.ApprovalBellMessageAppLink, i18n.Args{"url": link})
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

// linkablePath tells whether link is a path of Home Assistant's frontend, such as the
// app's panel, that can stand in a Markdown link as it is: absolute, not to another host,
// without query or fragment.
func linkablePath(link string) bool {
	if !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") || strings.ContainsAny(link, markdownUnsafe+"?#") ||
		strings.ContainsFunc(link, isSpaceOrControl) {
		return false
	}
	u, err := url.Parse(link)
	return err == nil && u.Scheme == "" && u.Host == "" && u.Path == link
}

func isSpaceOrControl(r rune) bool {
	return r <= ' ' || r == 0x7f || unicode.IsSpace(r) || unicode.IsControl(r)
}

// requestAddress is the address of the request id's page from the hook, "" without one.
func (s *Service) requestAddress(id string) string {
	if s.cfg.RequestAddress == nil {
		return ""
	}
	return s.cfg.RequestAddress(id)
}
