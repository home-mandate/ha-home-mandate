// SPDX-License-Identifier: AGPL-3.0-or-later

package dpoptest

import (
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"
)

func TestErrorsArePassedOn(t *testing.T) {
	k, err := NewKey(ES256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.ProofWith(func(h map[string]any) { h["x"] = func() {} }, map[string]any{}); err == nil {
		t.Error("header that is no JSON signed")
	}
	if _, err := k.ProofWith(nil, map[string]any{"x": func() {}}); err == nil {
		t.Error("claims that are no JSON signed")
	}
	broken := &Key{alg: ES256, ec: &ecdsa.PrivateKey{PublicKey: k.ec.PublicKey, D: big.NewInt(0)}}
	if _, err := broken.Proof(Claims{Method: "POST", URL: "https://hm.test/", IssuedAt: time.Unix(0, 0)}); err == nil {
		t.Error("signed with a zero scalar")
	}
}
