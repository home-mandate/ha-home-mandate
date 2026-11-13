// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// A KeySource tells the gateway's current server key: the digest of the
// SubjectPublicKeyInfo of the certificate agents see. With recheck it looks again now
// (before an alarm, so that a renewal not yet taken over does not raise one).
type KeySource interface {
	Key(ctx context.Context, recheck bool) (Digest, error)
}

// ErrKey means the gateway's own key could not be read.
var ErrKey = errors.New("ctwatch: own key unknown")

// FileKey is the key of the certificate Home-Mandate serves itself (tlscert.Loader).
type FileKey struct {
	Loader interface {
		PublicKeySHA256(recheck bool) [sha256.Size]byte
	}
}

// Key returns the key of the pair in use.
func (f FileKey) Key(_ context.Context, recheck bool) (Digest, error) {
	return f.Loader.PublicKeySHA256(recheck), nil
}

// ProxyKey is the key the reverse proxy in front of Home-Mandate serves for the public
// name (HM_PROXY): a TLS handshake to the proxy's own address with the name as SNI. The
// address is the configured one, never the name resolved through DNS: whoever controls
// the name's DNS could otherwise answer the handshake and have their certificate taken
// for the gateway's own.
type ProxyKey struct {
	Addr       netip.AddrPort
	ServerName string
	Timeout    time.Duration
}

// Key performs the handshake. The certificate's trust does not matter, only its key and
// that it covers the name; the connection goes nowhere but to the configured address.
func (p ProxyKey) Key(ctx context.Context, _ bool) (Digest, error) {
	var leaf *x509.Certificate
	conf := &tls.Config{
		ServerName:         p.ServerName,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // only the key is read; VerifyConnection checks the name
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("%w: no certificate", ErrKey)
			}
			if err := cs.PeerCertificates[0].VerifyHostname(p.ServerName); err != nil {
				return fmt.Errorf("%w: %w", ErrKey, err)
			}
			leaf = cs.PeerCertificates[0]
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: p.Timeout}, Config: conf}).DialContext(ctx, "tcp", p.Addr.String())
	if err != nil {
		if errors.Is(err, ErrKey) {
			return Digest{}, err
		}
		return Digest{}, fmt.Errorf("%w: handshake with %s: %w", ErrKey, p.Addr, err)
	}
	_ = conn.Close()
	return sha256.Sum256(leaf.RawSubjectPublicKeyInfo), nil
}
