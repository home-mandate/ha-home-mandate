// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The example proofs of RFC 9449 sections 4.1 (token endpoint) and 7.1 (resource, with
// ath), and the thumbprint of their key (section 6.1).
const (
	rfcResourceProof = "eyJ0eXAiOiJkcG9wK2p3dCIsImFsZyI6IkVTMjU2IiwiandrIjp7Imt0eSI6IkVDIiwieCI6Imw4dEZyaHgtMzR0VjNoUklDUkRZOXpDa0RscEJoRjQyVVFVZldWQVdCRnMiLCJ5IjoiOVZFNGpmX09rX282NHpiVFRsY3VOSmFqSG10NnY5VERWclUwQ2R2R1JEQSIsImNydiI6IlAtMjU2In19" +
		".eyJqdGkiOiJlMWozVl9iS2ljOC1MQUVCIiwiaHRtIjoiR0VUIiwiaHR1IjoiaHR0cHM6Ly9yZXNvdXJjZS5leGFtcGxlLm9yZy9wcm90ZWN0ZWRyZXNvdXJjZSIsImlhdCI6MTU2MjI2MjYxOCwiYXRoIjoiZlVIeU8ycjJaM0RaNTNFc05yV0JiMHhXWG9hTnk1OUlpS0NBcWtzbVFFbyJ9" +
		".2oW9RP35yRqzhrtNP86L-Ey71EOptxRimPPToA1plemAgR6pxHF8y6-yqyVnmcw6Fy1dqd-jfxSYoMxhAJpLjA"
	rfcAccessToken = "Kz~8mXK1EalYznwH-LC-1fBAo.4Ljp~zsPE_NeO.gxU"
	rfcProof       = "eyJ0eXAiOiJkcG9wK2p3dCIsImFsZyI6IkVTMjU2IiwiandrIjp7Imt0eSI6IkVDIiwieCI6Imw4dEZyaHgtMzR0VjNoUklDUkRZOXpDa0RscEJoRjQyVVFVZldWQVdCRnMiLCJ5IjoiOVZFNGpmX09rX282NHpiVFRsY3VOSmFqSG10NnY5VERWclUwQ2R2R1JEQSIsImNydiI6IlAtMjU2In19" +
		".eyJqdGkiOiItQndDM0VTYzZhY2MybFRjIiwiaHRtIjoiUE9TVCIsImh0dSI6Imh0dHBzOi8vc2VydmVyLmV4YW1wbGUuY29tL3Rva2VuIiwiaWF0IjoxNTYyMjYyNjE2fQ" +
		".2-GxA6T8lP4vfrg8v-FdWP0A0zdrj8igiMLvqRMUvwnQg4PtFLbdLXiOSsX0x7NVY-FNyJK70nfbV37xRZT3Lg"
	rfcJKT = "0ZcOCORZNYy-DWpqq30jZyJGHTN0d2HglBV3uiguA4I"
)

func TestRFC9449Example(t *testing.T) {
	clock := &testClock{t: time.Unix(1562262616, 0)}
	v := NewVerifier(clock.now)
	p, err := v.Verify(headerWith(rfcProof), Expect{Method: http.MethodPost, URL: "https://server.example.com/token"})
	if err != nil {
		t.Fatal(err)
	}
	if p.JKT != rfcJKT {
		t.Errorf("JKT = %s, want %s", p.JKT, rfcJKT)
	}
	resource := Expect{Method: http.MethodGet, URL: "https://resource.example.org/protectedresource", AccessToken: rfcAccessToken}
	if p, err := v.Verify(headerWith(rfcResourceProof), resource); err != nil || p.JKT != rfcJKT {
		t.Errorf("resource example: %+v, %v", p, err)
	}
	clock.t = clock.t.Add(Window + time.Second)
	if _, err := NewVerifier(clock.now).Check(headerWith(rfcProof), Expect{Method: http.MethodPost, URL: "https://server.example.com/token"}); !errors.Is(err, ErrInvalidProof) {
		t.Errorf("proof outside the window: err = %v", err)
	}
}

func TestValidProofs(t *testing.T) {
	for _, alg := range Algorithms {
		v, clock := newTestVerifier()
		s := newSigner(t, alg)
		p, err := v.Verify(headerWith(s.proof(t, clock.t, nil)), Expect{Method: http.MethodPost, URL: testURL})
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if p.JKT != thumbprintOf(t, s) || len(p.JKT) != 43 {
			t.Errorf("%s: JKT = %s", alg, p.JKT)
		}
		// The same key gives the same thumbprint for the next proof.
		if q, err := v.Verify(headerWith(s.proof(t, clock.t, nil)), Expect{Method: http.MethodPost, URL: testURL}); err != nil || q.JKT != p.JKT {
			t.Errorf("%s: second proof %v, %s", alg, err, q.JKT)
		}
	}
}

func thumbprintOf(t testing.TB, s signer) string {
	t.Helper()
	k, err := parseJWK(mustJSON(t, s.jwk()), s.alg)
	if err != nil {
		t.Fatal(err)
	}
	return k.jkt
}

// TestProofMutations changes a valid proof one field at a time.
func TestProofMutations(t *testing.T) {
	ec, ed := newSigner(t, algES256), newSigner(t, algEdDSA)
	set := func(where, name string, value any) func(h, c map[string]any) {
		return func(h, c map[string]any) {
			m := c
			if where == "header" {
				m = h
			}
			if value == nil {
				delete(m, name)
			} else {
				m[name] = value
			}
		}
	}
	jwkWith := func(s signer, name string, value any) func(h, c map[string]any) {
		return func(h, _ map[string]any) {
			k := s.jwk()
			if value == nil {
				delete(k, name)
			} else {
				k[name] = value
			}
			h["jwk"] = k
		}
	}
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name string
		s    signer
		edit func(h, c map[string]any)
	}{
		{"typ missing", ec, set("header", "typ", nil)},
		{"typ JWT", ec, set("header", "typ", "JWT")},
		{"typ upper case", ec, set("header", "typ", "DPOP+JWT")},
		{"alg none", ec, set("header", "alg", "none")},
		{"alg HS256", ec, set("header", "alg", "HS256")},
		{"alg RS256", ec, set("header", "alg", "RS256")},
		{"alg EdDSA with a P-256 key", ec, set("header", "alg", "EdDSA")},
		{"alg ES256 with an Ed25519 key", ed, set("header", "alg", "ES256")},
		{"alg missing", ec, set("header", "alg", nil)},
		{"jwk missing", ec, set("header", "jwk", nil)},
		{"jwk not an object", ec, set("header", "jwk", "key")},
		{"jwk with d", ec, jwkWith(ec, "d", "AAAA")},
		{"jwk with k", ed, jwkWith(ed, "k", "AAAA")},
		{"jwk P-384", ec, jwkWith(ec, "crv", "P-384")},
		{"jwk x missing", ec, jwkWith(ec, "x", nil)},
		{"jwk y missing", ec, jwkWith(ec, "y", nil)},
		{"jwk off the curve", ec, jwkWith(ec, "y", b64.EncodeToString(make([]byte, 32)))},
		{"jwk Ed25519 with y", ed, jwkWith(ed, "y", "AAAA")},
		{"jwk Ed25519 x short", ed, jwkWith(ed, "x", "AAAA")},
		{"jwk x padded", ed, jwkWith(ed, "x", b64.EncodeToString(make([]byte, 32))+"=")},
		{"crit", ec, set("header", "crit", []string{"x"})},
		{"jti missing", ec, set("claims", "jti", nil)},
		{"jti empty", ec, set("claims", "jti", "")},
		{"jti too long", ec, set("claims", "jti", strings.Repeat("j", maxJTIBytes+1))},
		{"jti a number", ec, set("claims", "jti", 7)},
		{"htm GET", ec, set("claims", "htm", http.MethodGet)},
		{"htm lower case", ec, set("claims", "htm", "post")},
		{"htm missing", ec, set("claims", "htm", nil)},
		{"htu other host", ec, set("claims", "htu", "https://evil.example/oauth/token")},
		{"htu other path", ec, set("claims", "htu", "https://gateway.example/mcp")},
		{"htu http", ec, set("claims", "htu", "http://gateway.example/oauth/token")},
		{"htu other port", ec, set("claims", "htu", "https://gateway.example:8443/oauth/token")},
		{"htu relative", ec, set("claims", "htu", "/oauth/token")},
		{"htu missing", ec, set("claims", "htu", nil)},
		{"htu with user info", ec, set("claims", "htu", "https://u@gateway.example/oauth/token")},
		{"iat missing", ec, set("claims", "iat", nil)},
		{"iat a string", ec, set("claims", "iat", "1800000000")},
		{"iat negative", ec, set("claims", "iat", -1)},
		{"iat too old", ec, set("claims", "iat", now.Add(-Window-time.Second).Unix())},
		{"iat too new", ec, set("claims", "iat", now.Add(Window+time.Second).Unix())},
	}
	for _, tc := range cases {
		v, clock := newTestVerifier()
		clock.t = now
		_, err := v.Verify(headerWith(tc.s.proof(t, now, tc.edit)), Expect{Method: http.MethodPost, URL: testURL})
		if !errors.Is(err, ErrInvalidProof) {
			t.Errorf("%s: err = %v, want ErrInvalidProof", tc.name, err)
		}
	}
}

func TestProofsThatStillMatch(t *testing.T) {
	ec := newSigner(t, algES256)
	now := time.Unix(1_800_000_000, 0)
	for name, htu := range map[string]string{
		"query":        testURL + "?x=1",
		"fragment":     testURL + "#f",
		"default port": "https://gateway.example:443/oauth/token",
		"upper case":   "HTTPS://GATEWAY.EXAMPLE/oauth/token",
	} {
		v, clock := newTestVerifier()
		clock.t = now
		edit := func(_, c map[string]any) { c["htu"] = htu; c["extra"] = true; c["iat"] = float64(now.Unix()) + 0.5 }
		if _, err := v.Verify(headerWith(ec.proof(t, now, edit)), Expect{Method: http.MethodPost, URL: testURL}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// iat at both edges of the window.
	for _, iat := range []time.Time{now.Add(-Window), now.Add(Window)} {
		v, clock := newTestVerifier()
		clock.t = now
		if _, err := v.Verify(headerWith(ec.proof(t, now, func(_, c map[string]any) { c["iat"] = iat.Unix() })), Expect{Method: http.MethodPost, URL: testURL}); err != nil {
			t.Errorf("iat %v: %v", iat.Sub(now), err)
		}
	}
}

func TestMalformedProofs(t *testing.T) {
	v, clock := newTestVerifier()
	ec, other := newSigner(t, algES256), newSigner(t, algES256)
	good := ec.proof(t, clock.t, nil)
	parts := strings.Split(good, ".")
	header := `{"typ":"dpop+jwt","alg":"ES256","jwk":` + string(mustJSON(t, ec.jwk())) + `}`
	claims := `{"jti":"a","htm":"POST","htu":"` + testURL + `","iat":1800000000}`
	for name, proof := range map[string]string{
		"empty":                 "",
		"two parts":             parts[0] + "." + parts[1],
		"four parts":            good + ".x",
		"header not base64url":  "!!." + parts[1] + "." + parts[2],
		"padded signature":      good + "=",
		"short signature":       parts[0] + "." + parts[1] + "." + parts[2][:20],
		"signature of another":  parts[0] + "." + parts[1] + "." + strings.Split(other.proof(t, clock.t, nil), ".")[2],
		"claims changed":        parts[0] + "." + b64.EncodeToString([]byte(claims)) + "." + parts[2],
		"too long":              good + strings.Repeat("A", maxProofBytes),
		"header named twice":    ec.signRaw(t, []byte(`{"typ":"dpop+jwt","alg":"none","alg":"ES256","jwk":`+string(mustJSON(t, ec.jwk()))+`}`), []byte(claims)),
		"claim named twice":     ec.signRaw(t, []byte(header), []byte(`{"jti":"a","htm":"GET","htm":"POST","htu":"`+testURL+`","iat":1800000000}`)),
		"jwk member twice":      ec.signRaw(t, []byte(strings.Replace(header, `"kty":"EC"`, `"kty":"EC","kty":"EC"`, 1)), []byte(claims)),
		"header not an object":  ec.signRaw(t, []byte(`["dpop+jwt"]`), []byte(claims)),
		"claims not an object":  ec.signRaw(t, []byte(header), []byte(`"claims"`)),
		"claims not JSON":       ec.signRaw(t, []byte(header), []byte(`{"jti":`)),
		"claims with trailing":  ec.signRaw(t, []byte(header), []byte(claims+`{}`)),
		"nested too deeply":     ec.signRaw(t, []byte(header), []byte(`{"x":`+strings.Repeat("[", 20)+strings.Repeat("]", 20)+`}`)),
		"header null":           ec.signRaw(t, []byte(`null`), []byte(claims)),
		"iat not a finite date": ec.signRaw(t, []byte(header), []byte(`{"jti":"a","htm":"POST","htu":"`+testURL+`","iat":1e300}`)),
	} {
		if _, err := v.Verify(headerWith(proof), Expect{Method: http.MethodPost, URL: testURL}); !errors.Is(err, ErrInvalidProof) {
			t.Errorf("%s: err = %v, want ErrInvalidProof", name, err)
		}
	}
}

func TestHeaders(t *testing.T) {
	v, clock := newTestVerifier()
	ec := newSigner(t, algES256)
	if _, err := v.Check(http.Header{}, Expect{Method: http.MethodPost, URL: testURL}); !errors.Is(err, ErrNoProof) || errors.Is(err, ErrInvalidProof) {
		t.Errorf("no header: err = %v, want ErrNoProof", err)
	}
	two := headerWith(ec.proof(t, clock.t, nil), ec.proof(t, clock.t, nil))
	if _, err := v.Check(two, Expect{Method: http.MethodPost, URL: testURL}); !errors.Is(err, ErrInvalidProof) {
		t.Errorf("two headers: err = %v", err)
	}
}

func TestSmallOrderAndNonCanonicalEd25519KeysAreRefused(t *testing.T) {
	for encoded := range smallOrderEd25519 {
		if _, err := parseJWK(mustJSON(t, map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64.EncodeToString(encoded[:])}), algEdDSA); err == nil {
			t.Errorf("small-order key %x accepted", encoded)
		}
	}
	// y = p, which crypto/ed25519 reads as y = 0.
	nonCanonical := []byte{0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}
	if _, err := parseJWK(mustJSON(t, map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64.EncodeToString(nonCanonical)}), algEdDSA); err == nil {
		t.Error("non-canonical key accepted")
	}
}

func TestAccessTokenHash(t *testing.T) {
	v, clock := newTestVerifier()
	ec := newSigner(t, algEdDSA)
	e := Expect{Method: http.MethodGet, URL: "https://gateway.example/mcp", AccessToken: testToken}
	proof := func(ath any) string {
		return ec.proof(t, clock.t, func(_, c map[string]any) {
			c["htm"], c["htu"] = http.MethodGet, "https://gateway.example/mcp"
			if ath != nil {
				c["ath"] = ath
			}
		})
	}
	if _, err := v.Verify(headerWith(proof(AccessTokenHash(testToken))), e); err != nil {
		t.Fatal(err)
	}
	for name, ath := range map[string]any{"missing": nil, "of another token": AccessTokenHash("hm_at_other"), "padded": AccessTokenHash(testToken) + "="} {
		if _, err := v.Verify(headerWith(proof(ath)), e); !errors.Is(err, ErrInvalidProof) {
			t.Errorf("ath %s: err = %v", name, err)
		}
	}
	// RFC 9449 section 7.1.
	if got := AccessTokenHash(rfcAccessToken); got != "fUHyO2r2Z3DZ53EsNrWBb0xWXoaNy59IiKCAqksmQEo" {
		t.Errorf("ath of the RFC example = %s", got)
	}
}

func TestReplay(t *testing.T) {
	v, clock := newTestVerifier()
	ec, ed := newSigner(t, algES256), newSigner(t, algEdDSA)
	e := Expect{Method: http.MethodPost, URL: testURL}
	fixed := func(_, c map[string]any) { c["jti"] = "same-jti" }
	proof := ec.proof(t, clock.t, fixed)
	if _, err := v.Verify(headerWith(proof), e); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(headerWith(proof), e); !errors.Is(err, ErrInvalidProof) {
		t.Errorf("replayed proof: err = %v", err)
	}
	if _, err := v.Verify(headerWith(ec.proof(t, clock.t, fixed)), e); !errors.Is(err, ErrInvalidProof) {
		t.Errorf("new proof with a used jti: err = %v", err)
	}
	// Another key has its own jti space.
	if _, err := v.Verify(headerWith(ed.proof(t, clock.t, fixed)), e); err != nil {
		t.Errorf("same jti under another key: %v", err)
	}
	// Check alone does not record the jti.
	fresh := ec.proof(t, clock.t, nil)
	for range 2 {
		if _, err := v.Check(headerWith(fresh), e); err != nil {
			t.Fatalf("Check: %v", err)
		}
	}
	if err := v.Accept(Proof{JKT: "x"}); !errors.Is(err, ErrInvalidProof) {
		t.Errorf("Accept of an unchecked proof: err = %v", err)
	}
}

func TestReplayCacheIsBounded(t *testing.T) {
	c := newReplayCache(2)
	now := time.Unix(1_800_000_000, 0)
	for _, k := range []string{"a", "b"} {
		if err := c.add(k, now.Add(Window), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.add("c", now.Add(Window), now); !errors.Is(err, errCacheFull) {
		t.Errorf("full cache: err = %v", err)
	}
	if err := c.add("a", now.Add(Window), now); !errors.Is(err, errReplay) {
		t.Errorf("replay in a full cache: err = %v", err)
	}
	later := now.Add(Window)
	if err := c.add("c", later.Add(Window), later); err != nil {
		t.Errorf("after expiry: %v", err)
	}
	if len(c.expires) != 1 {
		t.Errorf("expired entries kept: %d", len(c.expires))
	}
}

func TestNonces(t *testing.T) {
	v, clock := newTestVerifier()
	ec := newSigner(t, algES256)
	e := Expect{Method: http.MethodPost, URL: testURL, Nonce: true}
	withNonce := func(n any) string {
		return ec.proof(t, clock.t, func(_, c map[string]any) {
			if n != nil {
				c["nonce"] = n
			}
		})
	}
	if p, err := v.Verify(headerWith(withNonce(nil)), e); !errors.Is(err, ErrUseNonce) || errors.Is(err, ErrInvalidProof) || p.JKT != "" {
		t.Errorf("no nonce: %+v, %v, want ErrUseNonce", p, err)
	}
	nonce := v.Nonce()
	if len(nonce) != 32 {
		t.Errorf("nonce %q", nonce)
	}
	if _, err := v.Verify(headerWith(withNonce(nonce)), e); err != nil {
		t.Fatalf("valid nonce: %v", err)
	}
	for name, n := range map[string]any{
		"forged":      b64.EncodeToString(make([]byte, nonceBytes)),
		"short":       nonce[:20],
		"not base64":  "!!!",
		"a number":    7,
		"of another":  NewVerifier(clock.now).Nonce(),
		"from future": futureNonce(v, clock),
	} {
		if _, err := v.Verify(headerWith(withNonce(n)), e); !errors.Is(err, ErrUseNonce) {
			t.Errorf("nonce %s: err = %v, want ErrUseNonce", name, err)
		}
	}
	// A nonce outlives one rotation of the key but not its lifetime.
	clock.t = clock.t.Add(nonceLifetime - time.Second)
	fresh := v.Nonce() // rotates here
	if _, err := v.Verify(headerWith(withNonce(nonce)), e); err != nil {
		t.Errorf("nonce within its lifetime after a rotation: %v", err)
	}
	clock.t = clock.t.Add(2 * time.Second)
	if _, err := v.Verify(headerWith(withNonce(nonce)), e); !errors.Is(err, ErrUseNonce) {
		t.Errorf("expired nonce: err = %v", err)
	}
	if _, err := v.Verify(headerWith(withNonce(fresh)), e); err != nil {
		t.Errorf("fresh nonce: %v", err)
	}
	// An invalid proof is ErrInvalidProof even without a nonce.
	if _, err := v.Verify(headerWith(ec.proof(t, clock.t, func(_, c map[string]any) { c["htm"] = "GET" })), e); !errors.Is(err, ErrInvalidProof) {
		t.Errorf("invalid proof without nonce: err = %v", err)
	}
}

func futureNonce(v *Verifier, clock *testClock) string {
	saved := clock.t
	clock.t = clock.t.Add(time.Minute)
	n := v.Nonce()
	clock.t = saved
	return n
}

func TestNonceKeysRotate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	n := newNonces(now)
	old := n.make(now)
	n.rotate(now.Add(nonceLifetime))
	n.rotate(now.Add(2 * nonceLifetime))
	// The MAC key of old is gone; even with a fresh timestamp it would not verify.
	raw, _ := b64.DecodeString(old)
	if n.valid(old, now.Add(2*nonceLifetime)) || string(raw[8:]) == string(mac(n.key, raw[:8])) || string(raw[8:]) == string(mac(n.prev, raw[:8])) {
		t.Error("nonce valid after two rotations")
	}
}

func TestNormalizeHTU(t *testing.T) {
	for in, want := range map[string]string{
		"https://Gateway.Example":           "https://gateway.example/",
		"https://gateway.example:443/mcp":   "https://gateway.example/mcp",
		"http://gateway.example:80/mcp":     "http://gateway.example/mcp",
		"https://gateway.example:8443/mcp?": "https://gateway.example:8443/mcp",
		"https://[::1]/mcp":                 "https://[::1]/mcp",
		"https://[::1]:8443/mcp#x":          "https://[::1]:8443/mcp",
		"https://gateway.example/a%2Fb":     "https://gateway.example/a%2Fb",
	} {
		if got, ok := normalizeHTU(in); !ok || got != want {
			t.Errorf("normalizeHTU(%s) = %s, %v, want %s", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "/mcp", "ftp://gateway.example/", "mailto:a@b", "https://u:p@gateway.example/", "https://%zz/", "https:///mcp"} {
		if got, ok := normalizeHTU(in); ok {
			t.Errorf("normalizeHTU(%s) = %s, want refusal", in, got)
		}
	}
}
