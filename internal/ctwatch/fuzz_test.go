// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// FuzzParseCert feeds arbitrary bytes, seeded with real certificates and precertificates
// and with synthetic ones carrying SCT lists. Whatever comes from a source, parsing ends
// without a panic, either with ErrCert or with a certificate that is consistent: one
// digest for one TBSCertificate, names present, and a time of logging inside the range
// RFC 3339 can write.
func FuzzParseCert(f *testing.F) {
	if data, err := os.ReadFile(certSpotterFile); err == nil {
		var issuances []struct {
			DER string `json:"cert_der"`
		}
		if json.Unmarshal(data, &issuances) == nil {
			for _, is := range issuances {
				if der, err := base64.StdEncoding.DecodeString(is.DER); err == nil {
					f.Add(der)
				}
			}
		}
	}
	early := uint64(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli())
	f.Add(testCert(f, []string{"a.example"}, poison))
	f.Add(testCert(f, []string{"a.example"}, pkix.Extension{Id: oidSCTList, Value: sctListValue(f, sctBytes(early, 0), sctBytes(early+1, 1))}))
	f.Fuzz(func(t *testing.T, der []byte) {
		p, err := ParseCert(der)
		if err != nil {
			if !errors.Is(err, ErrCert) {
				t.Fatalf("error outside ErrCert: %v", err)
			}
			return
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("parsed what x509 refuses: %v", err)
		}
		if again, err := TBSDigest(cert.RawTBSCertificate); err != nil || again != p.Digest {
			t.Fatalf("digest not stable: %v", err)
		}
		if p.PublicKey != sha256.Sum256(cert.RawSubjectPublicKeyInfo) || len(p.Names) == 0 {
			t.Fatal("key digest or names wrong")
		}
		if !p.Logged.IsZero() && p.Logged.Year() > 9999 {
			t.Fatalf("logged %v", p.Logged)
		}
	})
}
