// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"net"
	"net/url"
	"strings"
)

// normalizeHTU reduces an http or https URI to scheme, host, port and path, as RFC 9449
// section 4.3 item 9 compares it: without query and fragment, with the syntax- and
// scheme-based normalization of RFC 3986 section 6.2 (lower-case scheme and host, no
// default port, "/" for an empty path). ok is false for anything else.
func normalizeHTU(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", false
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	authority := host
	if port != "" {
		authority = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + authority + path, true
}
