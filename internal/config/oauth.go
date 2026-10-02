// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// appHAHTTP is Home Assistant's HTTP API on the internal network of the Supervisor; the
// Supervisor proxy (http://supervisor/core) forwards only /api and the WebSocket, not
// /auth. To be confirmed during app packaging (week 4).
const appHAHTTP = "http://homeassistant:8123"

// publicURL validates HM_PUBLIC_URL or the public_url option and returns its origin.
// Plaintext is only allowed for loopback (decision 1: no plaintext on the LAN).
func publicURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	u, err := origin(s)
	if err != nil {
		return "", fmt.Errorf("%w: public URL: %w", ErrInvalid, err)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return "", fmt.Errorf("%w: public URL: plaintext http only for localhost; use https", ErrInvalid)
	}
	return u.String(), nil
}

// browserURL validates HM_HA_BROWSER_URL or the ha_browser_url option.
func browserURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	u, err := origin(s)
	if err != nil {
		return "", fmt.Errorf("%w: Home Assistant browser URL: %w", ErrInvalid, err)
	}
	return u.String(), nil
}

// origin parses an absolute http(s) URL that names an origin only: no user info, path,
// query or fragment.
func origin(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, fmt.Errorf("scheme must be http or https")
	case u.Host == "" || u.Opaque != "":
		return nil, fmt.Errorf("host missing")
	case u.User != nil:
		return nil, fmt.Errorf("user info not allowed")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s, "#"):
		return nil, fmt.Errorf("query or fragment not allowed")
	case u.Path != "" && u.Path != "/":
		return nil, fmt.Errorf("path not allowed")
	}
	return &url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host)}, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// httpOrigin turns the WebSocket URL of Home Assistant into the origin of its HTTP API.
// An unparsable URL gives "", it is rejected by the Home Assistant client anyway.
func httpOrigin(wsURL string) string {
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := "https"
	if u.Scheme == "ws" {
		scheme = "http"
	}
	return (&url.URL{Scheme: scheme, Host: strings.ToLower(u.Host)}).String()
}
