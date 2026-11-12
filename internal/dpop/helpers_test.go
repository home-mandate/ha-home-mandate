// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/dpop/dpoptest"
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

// signer makes proofs, with any header and claims, through dpoptest.
type signer struct{ key *dpoptest.Key }

func newSigner(t testing.TB, alg string) signer {
	t.Helper()
	k, err := dpoptest.NewKey(alg)
	if err != nil {
		t.Fatal(err)
	}
	return signer{key: k}
}

// fixedSigner has the same key in every process.
func fixedSigner(t testing.TB, alg string) signer {
	t.Helper()
	var seed [32]byte
	seed[31] = 7
	k, err := dpoptest.FixedKey(alg, seed)
	if err != nil {
		t.Fatal(err)
	}
	return signer{key: k}
}

func (s signer) jwk() map[string]any { return s.key.JWK() }

// proof builds a proof; edit may change the header and the claims before signing.
func (s signer) proof(t testing.TB, now time.Time, edit func(header, claims map[string]any)) string {
	t.Helper()
	claims := map[string]any{"jti": randomJTI(t), "htm": http.MethodPost, "htu": testURL, "iat": now.Unix()}
	p, err := s.key.ProofWith(func(header map[string]any) {
		if edit != nil {
			edit(header, claims)
		}
	}, claims)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (s signer) signRaw(t testing.TB, header, claims []byte) string {
	t.Helper()
	p, err := s.key.SignRaw(header, claims)
	if err != nil {
		t.Fatal(err)
	}
	return p
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
