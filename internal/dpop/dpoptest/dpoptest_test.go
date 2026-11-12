// SPDX-License-Identifier: AGPL-3.0-or-later

package dpoptest_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/dpop"
	"github.com/home-mandate/ha-home-mandate/internal/dpop/dpoptest"
)

// The proofs of the test client pass the gateway's verifier, with ath and nonce.
func TestProofsVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	v := dpop.NewVerifier(func() time.Time { return now })
	var seed [32]byte
	seed[0] = 1
	for _, alg := range []string{dpoptest.ES256, dpoptest.EdDSA} {
		fresh, err := dpoptest.NewKey(alg)
		if err != nil {
			t.Fatal(err)
		}
		fixed, err := dpoptest.FixedKey(alg, seed)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := dpoptest.FixedKey(alg, seed)
		if fixed.JKT() != again.JKT() || fixed.JKT() == fresh.JKT() || fresh.Alg() != alg {
			t.Errorf("%s: fixed keys differ or equal a fresh one", alg)
		}
		for _, k := range []*dpoptest.Key{fresh, fixed} {
			proof, err := k.Proof(dpoptest.Claims{Method: http.MethodGet, URL: "https://hm.test/mcp", AccessToken: "hma_x",
				Nonce: v.Nonce(), IssuedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			h := http.Header{}
			h.Set(dpop.HeaderName, proof)
			p, err := v.Verify(h, dpop.Expect{Method: http.MethodGet, URL: "https://hm.test/mcp", AccessToken: "hma_x", Nonce: true})
			if err != nil || p.JKT != k.JKT() {
				t.Errorf("%s: %+v, %v", alg, p, err)
			}
		}
	}
	if _, err := dpoptest.NewKey("RS256"); err == nil {
		t.Error("RS256 key made")
	}
	if _, err := dpoptest.FixedKey("RS256", seed); err == nil {
		t.Error("RS256 fixed key made")
	}
}
