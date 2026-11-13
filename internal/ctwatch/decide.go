// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ctwatch watches Certificate Transparency for the gateway's public name (issue
// #66) and decides, by RELAY-v0 section 6.7, which certificates raise an alarm. The
// decision is a pure function of the certificate, the gateway's server keys, the
// acknowledgements and two points in time; everything else (sources, storage,
// notifications) feeds it.
package ctwatch

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Digest is a SHA-256 digest: of a TBSCertificate with the poison and the SCT list
// removed (a certificate's identity), or of a SubjectPublicKeyInfo (a key).
type Digest [sha256.Size]byte

// String is the canonical base64url form without padding, as in RELAY-v0.
func (d Digest) String() string { return base64.RawURLEncoding.EncodeToString(d[:]) }

// ErrDigest means a text is not a digest in canonical base64url.
var ErrDigest = errors.New("ctwatch: not a SHA-256 digest in canonical base64url")

// ParseDigest reads the canonical base64url form; any other spelling of the same bytes
// is refused, so that one digest has one text.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != len(d) {
		return d, ErrDigest
	}
	copy(d[:], b)
	return d, nil
}

// Result is what a certificate means for the household.
type Result int

const (
	// None: the certificate is the gateway's own, acknowledged, for another name, or
	// expired before monitoring began.
	None Result = iota
	// Alarm: someone else holds a certificate for the name that is valid now or will be.
	Alarm
	// PastExposure: such a certificate was valid at some time since monitoring began and
	// has expired by now.
	PastExposure
)

func (r Result) String() string {
	switch r {
	case Alarm:
		return "alarm"
	case PastExposure:
		return "past-exposure"
	default:
		return "none"
	}
}

// Cert is what the decision needs of a certificate from a log.
type Cert struct {
	Digest    Digest
	Names     []string // DNS names of the SAN extension and the subject CN
	NotBefore time.Time
	NotAfter  time.Time
	PublicKey Digest // SHA-256 of the SubjectPublicKeyInfo
	// Logged is the time of its first log entry; zero when no source told it, which
	// counts as after the replacement of any earlier key (an alarm rather than silence).
	Logged time.Time
}

// EarlierKey is a server key the gateway replaced at Replaced.
type EarlierKey struct {
	PublicKey Digest
	Replaced  time.Time
}

// Keys are the gateway's server keys: the current one, if known, and the earlier ones.
type Keys struct {
	Current    Digest
	HasCurrent bool
	Earlier    []EarlierKey
}

// Situation is everything the decision depends on besides the certificate.
type Situation struct {
	PublicNames []string
	// Since takes the place of the pairing of RELAY-v0: the first start of monitoring
	// for the name.
	Since        time.Time
	Now          time.Time
	Keys         Keys
	Acknowledged map[Digest]bool
}

// Decide applies RELAY-v0 section 6.7 to one certificate.
func Decide(s Situation, c Cert) Result {
	if !s.counts(c) || s.own(c) || s.Acknowledged[c.Digest] {
		return None
	}
	if !c.NotAfter.Before(s.Now) {
		return Alarm // valid now, or not valid yet
	}
	if c.NotAfter.Before(s.Since) {
		return None // expired before monitoring began
	}
	return PastExposure
}

// counts tells whether one of the certificate's names covers a public name.
func (s Situation) counts(c Cert) bool {
	for _, public := range s.PublicNames {
		for _, name := range c.Names {
			if Covers(name, public) {
				return true
			}
		}
	}
	return false
}

// own tells whether the gateway obtained the certificate: by its current key, or by an
// earlier one if the certificate was logged before that key was replaced.
func (s Situation) own(c Cert) bool {
	if s.Keys.HasCurrent && c.PublicKey == s.Keys.Current {
		return true
	}
	for _, k := range s.Keys.Earlier {
		if c.PublicKey == k.PublicKey && !c.Logged.IsZero() && c.Logged.Before(k.Replaced) {
			return true
		}
	}
	return false
}

// Covers tells whether a certificate's name covers a public name: equal without regard
// to case, or a wildcard of exactly one label in the leftmost position. A wildcard
// covers neither the domain it stands under nor a name more than one label below.
func Covers(certName, publicName string) bool {
	certName, publicName = strings.ToLower(certName), strings.ToLower(publicName)
	if certName == publicName {
		return true
	}
	parent, ok := strings.CutPrefix(certName, "*.")
	if !ok || parent == "" {
		return false
	}
	label, rest, found := strings.Cut(publicName, ".")
	return found && label != "" && rest == parent
}
