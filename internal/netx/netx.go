// SPDX-License-Identifier: AGPL-3.0-or-later

// Package netx is the hardened HTTP client for requests to the internet: Client ID
// Metadata Documents (internal/oauth) and Certificate Transparency (internal/ctwatch).
// It connects only to public addresses, checked on the address actually dialled after
// DNS resolution (SSRF, decision W6), uses no proxy from the environment, follows no
// redirect and requires TLS 1.2 or later. Where both are available, the dialer tries
// IPv6 first (Go's address ordering, RFC 6724, with Happy Eyeballs): sources such as
// Cert Spotter limit requests per IPv4 address, which households behind CGNAT share.
package netx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrNotPublic means a connection was refused because its address is not public.
var ErrNotPublic = errors.New("netx: address not public")

// maxHeaderBytes bounds the response headers of every request.
const maxHeaderBytes = 16 << 10

// nonPublic lists special-purpose ranges beyond what netip classifies. 192.88.99.0/24,
// the deprecated 6to4 relay anycast, stays allowed: the E2E suite uses it as a public
// stand-in.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"), // IPv4-compatible, deprecated
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"), // Teredo
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"), // site-local, deprecated
}

// PublicAddr tells whether ip is a public unicast address.
func PublicAddr(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// CheckAddress checks a dialled address (host:port) with allow; nil allow means
// PublicAddr. IPv4-mapped IPv6 addresses count as their IPv4 address.
func CheckAddress(address string, allow func(netip.Addr) bool) error {
	if allow == nil {
		allow = PublicAddr
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotPublic, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !allow(ip.Unmap()) {
		return fmt.Errorf("%w: %s", ErrNotPublic, host)
	}
	return nil
}

// Config configures a client.
type Config struct {
	// Timeout bounds a whole request, the connection and the TLS handshake each.
	Timeout time.Duration
	// Roots are the trusted CAs; nil means the system roots, as Go finds them.
	Roots *x509.CertPool
	// Control checks each dialled address; nil means CheckAddress with PublicAddr.
	Control func(network, address string, c syscall.RawConn) error
}

// NewClient returns the hardened client.
func NewClient(c Config) *http.Client {
	control := c.Control
	if control == nil {
		control = func(_, address string, _ syscall.RawConn) error { return CheckAddress(address, nil) }
	}
	dialer := &net.Dialer{Timeout: c.Timeout, Control: control}
	return &http.Client{
		Timeout: c.Timeout,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.Roots},
			TLSHandshakeTimeout:    c.Timeout,
			ResponseHeaderTimeout:  c.Timeout,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: maxHeaderBytes,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
