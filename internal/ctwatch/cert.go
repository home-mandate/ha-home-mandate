// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// oidPoison marks a precertificate (RFC 6962 section 3.1).
	oidPoison = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 3}
	// oidSCTList carries the SCTs embedded in a certificate (RFC 6962 section 3.3).
	oidSCTList = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
)

const (
	// MaxCertBytes bounds a certificate from a source; real ones are a few kilobytes.
	MaxCertBytes = 64 << 10
	// maxNames bounds the names kept of one certificate (CAs allow 100 or so).
	maxNames = 1000
	// maxSCTs bounds the SCTs read of one certificate.
	maxSCTs = 64
	// tagExtensions is the context tag of the extensions in a TBSCertificate.
	tagExtensions = 3
	// sctTimeEnd is where the time of a version 0 SCT ends: version, log ID, time.
	sctTimeEnd = 1 + 32 + 8
	// maxSCTMillis bounds an SCT's time to the year 9999, which time.Time and RFC 3339
	// hold; a later one is no time a log gave.
	maxSCTMillis = 253402300799999
)

// ErrCert means a source gave something that is not a certificate this package can read.
var ErrCert = errors.New("ctwatch: unreadable certificate")

// Parsed is what the gateway keeps of a certificate or precertificate from a log.
type Parsed struct {
	Cert
	Issuer string // the issuer's distinguished name, for the alarm
	Serial string // the serial number in hexadecimal, for the alarm
}

// ParseCert reads a certificate or precertificate in DER: its identity (the digest of the
// TBSCertificate without poison and SCT list, so that both have one), the digest of its
// public key, its names, its validity and the time of its earliest embedded SCT, which
// is when its precertificate was first logged (zero without SCTs).
func ParseCert(der []byte) (Parsed, error) {
	if len(der) == 0 || len(der) > MaxCertBytes {
		return Parsed{}, fmt.Errorf("%w: %d bytes", ErrCert, len(der))
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Parsed{}, fmt.Errorf("%w: %w", ErrCert, err)
	}
	digest, err := TBSDigest(cert.RawTBSCertificate)
	if err != nil {
		return Parsed{}, err
	}
	logged, err := earliestSCT(cert)
	if err != nil {
		return Parsed{}, err
	}
	names := certNames(cert)
	if len(names) == 0 {
		return Parsed{}, fmt.Errorf("%w: no DNS names", ErrCert)
	}
	return Parsed{
		Cert: Cert{Digest: digest, Names: names, NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC(),
			PublicKey: sha256.Sum256(cert.RawSubjectPublicKeyInfo), Logged: logged},
		Issuer: cert.Issuer.String(),
		Serial: fmt.Sprintf("%x", cert.SerialNumber),
	}, nil
}

// certNames are the DNS names of the SAN extension and the subject CN, lower case and
// without duplicates.
func certNames(cert *x509.Certificate) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append([]string{cert.Subject.CommonName}, cert.DNSNames...) {
		n = strings.ToLower(n)
		if n == "" || seen[n] || len(out) == maxNames {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// TBSDigest is the SHA-256 of a TBSCertificate with the poison and the SCT list removed
// (RFC 6962 section 3.2), the identity of a certificate and its precertificate. Every
// other element keeps its bytes; an extensions field left empty is dropped, as RFC 5280
// allows no empty one.
func TBSDigest(rawTBS []byte) (Digest, error) {
	var tbs asn1.RawValue
	if rest, err := asn1.Unmarshal(rawTBS, &tbs); err != nil || len(rest) > 0 ||
		tbs.Class != asn1.ClassUniversal || tbs.Tag != asn1.TagSequence || !tbs.IsCompound {
		return Digest{}, fmt.Errorf("%w: TBSCertificate", ErrCert)
	}
	var content []byte
	for rest := tbs.Bytes; len(rest) > 0; {
		var field asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &field); err != nil {
			return Digest{}, fmt.Errorf("%w: TBSCertificate field", ErrCert)
		}
		if field.Class != asn1.ClassContextSpecific || field.Tag != tagExtensions {
			content = append(content, field.FullBytes...)
			continue
		}
		kept, err := withoutCTExtensions(field)
		if err != nil {
			return Digest{}, err
		}
		content = append(content, kept...)
	}
	out, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: content})
	if err != nil {
		return Digest{}, fmt.Errorf("%w: %w", ErrCert, err)
	}
	return sha256.Sum256(out), nil
}

// withoutCTExtensions re-encodes the [3] extensions field without poison and SCT list;
// nil when nothing is left.
func withoutCTExtensions(field asn1.RawValue) ([]byte, error) {
	var list asn1.RawValue
	if rest, err := asn1.Unmarshal(field.Bytes, &list); err != nil || len(rest) > 0 ||
		list.Class != asn1.ClassUniversal || list.Tag != asn1.TagSequence {
		return nil, fmt.Errorf("%w: extensions", ErrCert)
	}
	var kept []byte
	for rest := list.Bytes; len(rest) > 0; {
		var ext asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &ext); err != nil {
			return nil, fmt.Errorf("%w: extension", ErrCert)
		}
		var id asn1.ObjectIdentifier
		if _, err := asn1.Unmarshal(ext.Bytes, &id); err != nil {
			return nil, fmt.Errorf("%w: extension ID", ErrCert)
		}
		if id.Equal(oidPoison) || id.Equal(oidSCTList) {
			continue
		}
		kept = append(kept, ext.FullBytes...)
	}
	if len(kept) == 0 {
		return nil, nil
	}
	inner, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: kept})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCert, err)
	}
	return asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tagExtensions, IsCompound: true, Bytes: inner})
}

// earliestSCT is the time of the earliest SCT embedded in the certificate (RFC 6962
// section 3.3), zero without any. An SCT list that cannot be read is an error: a
// certificate is never taken as logged at a time no source told.
func earliestSCT(cert *x509.Certificate) (time.Time, error) {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidSCTList) {
			continue
		}
		var list []byte
		if rest, err := asn1.Unmarshal(ext.Value, &list); err != nil || len(rest) > 0 {
			return time.Time{}, fmt.Errorf("%w: SCT list", ErrCert)
		}
		return sctListTime(list)
	}
	return time.Time{}, nil
}

// sctListTime reads a SignedCertificateTimestampList: a 16-bit length, then SCTs of a
// 16-bit length each, whose version 0 is followed by a 32-byte log ID and a 64-bit time
// in milliseconds. SCTs of another version tell no time this package can read.
func sctListTime(list []byte) (time.Time, error) {
	bad := fmt.Errorf("%w: SCT list", ErrCert)
	if len(list) < 2 || int(binary.BigEndian.Uint16(list)) != len(list)-2 {
		return time.Time{}, bad
	}
	var earliest uint64
	for n, rest := 0, list[2:]; len(rest) > 0; n++ {
		if len(rest) < 2 || n == maxSCTs {
			return time.Time{}, bad
		}
		size := int(binary.BigEndian.Uint16(rest))
		if size > len(rest)-2 {
			return time.Time{}, bad
		}
		sct := rest[2 : 2+size]
		rest = rest[2+size:]
		if len(sct) < sctTimeEnd || sct[0] != 0 {
			continue
		}
		ms := binary.BigEndian.Uint64(sct[33:sctTimeEnd])
		if ms == 0 || ms > maxSCTMillis {
			return time.Time{}, bad
		}
		if earliest == 0 || ms < earliest {
			earliest = ms
		}
	}
	if earliest == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(int64(earliest)).UTC(), nil
}
