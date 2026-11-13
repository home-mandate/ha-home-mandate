// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"slices"
	"testing"
	"time"
)

// testdata/certspotter-example.com.json holds issuances for example.com as the Cert
// Spotter API gave them on 2026-11-13 (public data), each with the digests Cert Spotter
// computed. testdata/crtsh-example.com-*.pem are a precertificate (…7802) and its
// certificate (…4041) from crt.sh: the last issuance of that file.
const (
	certSpotterFile = "testdata/certspotter-example.com.json"
	crtshCert       = "testdata/crtsh-example.com-29581274041.pem"
	crtshPrecert    = "testdata/crtsh-example.com-29581277802.pem"
	crtshTBS        = "6ecee9752dd17d6ab36befdda1a90849a5c9227e6a7806621ac8ef42cd621d9c"
)

// TestParseCertAgreesWithCertSpotter: the TBS digest (poison and SCT list removed) and
// the key digest are what an independent implementation computes for real certificates.
func TestParseCertAgreesWithCertSpotter(t *testing.T) {
	data, err := os.ReadFile(certSpotterFile)
	if err != nil {
		t.Fatal(err)
	}
	var issuances []struct {
		ID        string   `json:"id"`
		TBS       string   `json:"tbs_sha256"`
		PublicKey string   `json:"pubkey_sha256"`
		DER       string   `json:"cert_der"`
		Names     []string `json:"dns_names"`
	}
	if err := json.Unmarshal(data, &issuances); err != nil || len(issuances) == 0 {
		t.Fatalf("%d issuances: %v", len(issuances), err)
	}
	for _, is := range issuances {
		der, err := base64.StdEncoding.DecodeString(is.DER)
		if err != nil {
			t.Fatal(err)
		}
		p, err := ParseCert(der)
		if err != nil {
			t.Fatalf("%s: %v", is.ID, err)
		}
		if got := hex.EncodeToString(p.Digest[:]); got != is.TBS {
			t.Errorf("%s: TBS digest %s, Cert Spotter %s", is.ID, got, is.TBS)
		}
		if got := hex.EncodeToString(p.PublicKey[:]); got != is.PublicKey {
			t.Errorf("%s: key digest %s, Cert Spotter %s", is.ID, got, is.PublicKey)
		}
		for _, n := range is.Names {
			if !slices.Contains(p.Names, n) {
				t.Errorf("%s: name %s missing in %v", is.ID, n, p.Names)
			}
		}
		if p.Logged.IsZero() || p.Logged.Before(p.NotBefore.Add(-48*time.Hour)) {
			t.Errorf("%s: logged %v, not before %v", is.ID, p.Logged, p.NotBefore)
		}
	}
}

func readPEM(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s: no PEM", path)
	}
	return block.Bytes
}

// TestPrecertificateAndCertificateAreOne: RELAY-v0 section 6.7 identifies both by one
// digest, so that an acknowledgement covers both.
func TestPrecertificateAndCertificateAreOne(t *testing.T) {
	cert, err := ParseCert(readPEM(t, crtshCert))
	if err != nil {
		t.Fatal(err)
	}
	pre, err := ParseCert(readPEM(t, crtshPrecert))
	if err != nil {
		t.Fatal(err)
	}
	if cert.Digest != pre.Digest || hex.EncodeToString(cert.Digest[:]) != crtshTBS {
		t.Fatalf("certificate %x, precertificate %x, Cert Spotter %s", cert.Digest, pre.Digest, crtshTBS)
	}
	if cert.Logged.IsZero() || !pre.Logged.IsZero() {
		t.Errorf("logged: certificate %v (has SCTs), precertificate %v (has none)", cert.Logged, pre.Logged)
	}
	if cert.PublicKey != pre.PublicKey || cert.Serial != pre.Serial || cert.Issuer == "" {
		t.Errorf("key, serial or issuer differ: %+v / %+v", cert, pre)
	}
}

// testCert makes a self-signed certificate with the extra extensions.
func testCert(t testing.TB, names []string, extra ...pkix.Extension) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "Test"}, DNSNames: names,
		NotBefore: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC),
		ExtraExtensions: extra}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func mustMarshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func seqOf(t testing.TB, b []byte) []byte {
	return mustMarshal(t, asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: b})
}

func extensionsField(t testing.TB, b []byte) []byte {
	return mustMarshal(t, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tagExtensions, IsCompound: true, Bytes: b})
}

// sctBytes is one version-0 SCT at ms (log ID and the rest zero); version v when given.
func sctBytes(ms uint64, v byte) []byte {
	s := make([]byte, sctTimeEnd+6)
	s[0] = v
	binary.BigEndian.PutUint64(s[33:sctTimeEnd], ms)
	return s
}

// sctListValue encodes the SCTs as the value of the SCT list extension.
func sctListValue(t testing.TB, scts ...[]byte) []byte {
	var body []byte
	for _, s := range scts {
		body = append(binary.BigEndian.AppendUint16(body, uint16(len(s))), s...)
	}
	return mustMarshal(t, append(binary.BigEndian.AppendUint16(nil, uint16(len(body))), body...))
}

var poison = pkix.Extension{Id: oidPoison, Critical: true, Value: asn1.NullBytes}

func TestDigestIgnoresOnlyTheCTExtensions(t *testing.T) {
	plain, err := x509.ParseCertificate(testCert(t, []string{"a.example"}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := TBSDigest(plain.RawTBSCertificate)
	if err != nil {
		t.Fatal(err)
	}
	if got != sha256.Sum256(plain.RawTBSCertificate) {
		t.Fatal("a TBSCertificate without CT extensions must keep its bytes")
	}
	// A field whose only extension is the poison is dropped as a whole.
	serial := mustMarshal(t, 7)
	onlyPoison := seqOf(t, append(serial, extensionsField(t, seqOf(t, mustMarshal(t, poison)))...))
	if got, err := TBSDigest(onlyPoison); err != nil || got != sha256.Sum256(seqOf(t, serial)) {
		t.Fatalf("only poison: %x, %v", got, err)
	}
	// Other extensions stay: two certificates differing in one are two.
	a, err := ParseCert(testCert(t, []string{"a.example"}, poison))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseCert(testCert(t, []string{"a.example"}, poison, pkix.Extension{Id: asn1.ObjectIdentifier{1, 2, 3}, Value: asn1.NullBytes}))
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == b.Digest {
		t.Fatal("an extension other than poison and SCT list must count")
	}
}

func TestEarliestSCT(t *testing.T) {
	early := uint64(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).UnixMilli())
	late := early + uint64(time.Hour.Milliseconds())
	cases := []struct {
		name string
		scts [][]byte
		want uint64
	}{
		{"earliest of several", [][]byte{sctBytes(late, 0), sctBytes(early, 0)}, early},
		{"another version tells no time", [][]byte{sctBytes(early, 1), sctBytes(late, 0)}, late},
		{"only another version", [][]byte{sctBytes(early, 1)}, 0},
		{"too short to tell a time", [][]byte{{0, 1, 2}, sctBytes(late, 0)}, late},
		{"empty list", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParseCert(testCert(t, []string{"a.example"}, pkix.Extension{Id: oidSCTList, Value: sctListValue(t, c.scts...)}))
			if err != nil {
				t.Fatal(err)
			}
			want := time.Time{}
			if c.want != 0 {
				want = time.UnixMilli(int64(c.want)).UTC()
			}
			if !p.Logged.Equal(want) {
				t.Fatalf("logged %v, want %v", p.Logged, want)
			}
		})
	}
}

func TestMalformedSCTListsAreRefused(t *testing.T) {
	good := uint64(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli())
	tooMany := make([][]byte, maxSCTs+1)
	for i := range tooMany {
		tooMany[i] = sctBytes(good, 0)
	}
	cases := map[string][]byte{
		"not an octet string":  mustMarshal(t, 5),
		"trailing data":        append(sctListValue(t, sctBytes(good, 0)), 0),
		"short":                mustMarshal(t, []byte{0}),
		"wrong total length":   mustMarshal(t, []byte{0, 9, 0, 1, 0}),
		"SCT longer than list": mustMarshal(t, []byte{0, 2, 0, 9}),
		"dangling length byte": mustMarshal(t, []byte{0, 1, 0}),
		"too many":             sctListValue(t, tooMany...),
		"time zero":            sctListValue(t, sctBytes(0, 0)),
		"time after year 9999": sctListValue(t, sctBytes(maxSCTMillis+1, 0)),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCert(testCert(t, []string{"a.example"}, pkix.Extension{Id: oidSCTList, Value: value}))
			if !errors.Is(err, ErrCert) {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestParseCertRefuses(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nameless := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	noNames, err := x509.CreateCertificate(rand.Reader, nameless, nameless, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"no names": noNames,
		"empty":    nil,
		"too long": make([]byte, MaxCertBytes+1),
		"garbage":  []byte("not a certificate"),
	}
	for name, der := range cases {
		if _, err := ParseCert(der); !errors.Is(err, ErrCert) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCertNamesAreLowerCaseAndUnique(t *testing.T) {
	p, err := ParseCert(testCert(t, []string{"A.Example", "a.example", "*.Example"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"test", "a.example", "*.example"}; !slices.Equal(p.Names, want) {
		t.Fatalf("names %v, want %v", p.Names, want)
	}
}

func TestTBSDigestRefusesMalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"not DER":               {0x30, 0x80},
		"not a sequence":        mustMarshal(t, 5),
		"trailing data":         append(seqOf(t, nil), 0),
		"broken field":          seqOf(t, []byte{0x02, 0x05, 0x01}),
		"extensions not a list": seqOf(t, extensionsField(t, mustMarshal(t, 5))),
		"broken extension":      seqOf(t, extensionsField(t, seqOf(t, []byte{0x30, 0x05, 0x01}))),
		"extension without ID":  seqOf(t, extensionsField(t, seqOf(t, seqOf(t, mustMarshal(t, true))))),
	}
	for name, tbs := range cases {
		if _, err := TBSDigest(tbs); !errors.Is(err, ErrCert) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDigestText(t *testing.T) {
	d := Digest(sha256.Sum256([]byte("tbs-x")))
	back, err := ParseDigest(d.String())
	if err != nil || back != d {
		t.Fatalf("%v, %v", back, err)
	}
	for _, bad := range []string{"", "abc", d.String() + "A", d.String()[:42] + "=", "kPYe4TclFMsM9R3f6RaAkkZMqT6s7tBIqzemuT4FtJB"} {
		if _, err := ParseDigest(bad); !errors.Is(err, ErrDigest) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestCovers(t *testing.T) {
	cases := []struct {
		cert, public string
		want         bool
	}{
		{"a.example", "A.EXAMPLE", true},
		{"*.example", "a.example", true},
		{"*.example", "example", false},
		{"*.example", "b.a.example", false},
		{"*.", "a.", false},
		{"*.example", ".example", false},
		{"a*.example", "ab.example", false},
		{"*.*.example", "a.b.example", false},
	}
	for _, c := range cases {
		if got := Covers(c.cert, c.public); got != c.want {
			t.Errorf("Covers(%q, %q) = %v", c.cert, c.public, got)
		}
	}
}

func TestResultText(t *testing.T) {
	for r, want := range map[Result]string{None: "none", Alarm: "alarm", PastExposure: "past-exposure", Result(9): "none"} {
		if r.String() != want {
			t.Errorf("%d: %s", r, r.String())
		}
	}
}
