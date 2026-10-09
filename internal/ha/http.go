// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"crypto/x509"
	"net/http"
	"net/url"
	"time"
)

// httpTimeout bounds one request to Home Assistant's HTTP API.
const httpTimeout = 10 * time.Second

// HTTPClient returns a client for Home Assistant's HTTP API at origin (http:// or
// https://), e.g. to exchange the sign-in code of a human. The rules of the WebSocket
// connection apply: TLS 1.3, no redirects, no proxy, plaintext only as plaintext allows.
func HTTPClient(origin string, roots *x509.CertPool, plaintext Plaintext) (*http.Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Path != "" && u.Path != "/" {
		return nil, ErrInvalidURL
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return nil, ErrInvalidURL
	}
	if err := validateURL(u.String(), plaintext); err != nil {
		return nil, err
	}
	c := newHTTPClient(u.String(), roots, plaintext)
	c.Timeout = httpTimeout
	return c, nil
}
