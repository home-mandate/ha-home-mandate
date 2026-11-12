// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

const (
	testURL   = "https://gateway.example/oauth/token"
	testToken = "hm_at_test-token"
)

// testClock is a settable clock for a Verifier.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func newTestVerifier() (*Verifier, *testClock) {
	c := &testClock{t: time.Unix(1_800_000_000, 0)}
	return NewVerifier(c.now), c
}

// signer makes proofs with an ES256 or EdDSA key.
type signer struct {
	alg string
	ec  *ecdsa.PrivateKey
	ed  ed25519.PrivateKey
}

func newSigner(t testing.TB, alg string) signer {
	t.Helper()
	if alg == algEdDSA {
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return signer{alg: alg, ed: k}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{alg: alg, ec: k}
}

// fixedSigner has the same key in every process.
func fixedSigner(t testing.TB, alg string) signer {
	t.Helper()
	seed := make([]byte, 32)
	seed[31] = 7
	if alg == algEdDSA {
		return signer{alg: alg, ed: ed25519.NewKeyFromSeed(seed)}
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), seed)
	if err != nil {
		t.Fatal(err)
	}
	return signer{alg: alg, ec: k}
}

func (s signer) jwk() map[string]any {
	if s.alg == algEdDSA {
		return map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64.EncodeToString(s.ed.Public().(ed25519.PublicKey))}
	}
	b, _ := s.ec.PublicKey.Bytes()
	return map[string]any{"kty": "EC", "crv": "P-256", "x": b64.EncodeToString(b[1:33]), "y": b64.EncodeToString(b[33:])}
}

func (s signer) sign(t testing.TB, input []byte) []byte {
	t.Helper()
	if s.alg == algEdDSA {
		return ed25519.Sign(s.ed, input)
	}
	digest := sha256.Sum256(input)
	r, sv, err := ecdsa.Sign(rand.Reader, s.ec, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	sv.FillBytes(out[32:])
	return out
}

// proof builds a proof; edit may change the header and the claims before signing.
func (s signer) proof(t testing.TB, now time.Time, edit func(header, claims map[string]any)) string {
	t.Helper()
	header := map[string]any{"typ": "dpop+jwt", "alg": s.alg, "jwk": s.jwk()}
	claims := map[string]any{"jti": randomJTI(t), "htm": http.MethodPost, "htu": testURL, "iat": now.Unix()}
	if edit != nil {
		edit(header, claims)
	}
	return s.signRaw(t, mustJSON(t, header), mustJSON(t, claims))
}

func (s signer) signRaw(t testing.TB, header, claims []byte) string {
	input := b64.EncodeToString(header) + "." + b64.EncodeToString(claims)
	return input + "." + b64.EncodeToString(s.sign(t, []byte(input)))
}

func randomJTI(t testing.TB) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b64.EncodeToString(b)
}

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func headerWith(proofs ...string) http.Header {
	h := http.Header{}
	for _, p := range proofs {
		h.Add(HeaderName, p)
	}
	return h
}
