// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"errors"
	"net/http"
	"testing"
)

// FuzzParseProof: an arbitrary DPoP header never panics, fails with one of the two
// errors, and is accepted only if it is signed by one of the two fixed test keys (the
// keys are fixed so that every fuzz worker knows them).
func FuzzParseProof(f *testing.F) {
	v, clock := newTestVerifier()
	ec, ed := fixedSigner(f, algES256), fixedSigner(f, algEdDSA)
	ours := map[string]bool{}
	for _, s := range []signer{ec, ed} {
		ours[thumbprintOf(f, s)] = true
		f.Add(s.proof(f, clock.t, nil))
	}
	f.Add(rfcProof)
	f.Add("")
	f.Add("a.b.c")
	e := Expect{Method: http.MethodPost, URL: testURL}
	f.Fuzz(func(t *testing.T, proof string) {
		p, err := v.Check(headerWith(proof), e)
		if err == nil {
			if !ours[p.JKT] {
				t.Fatalf("accepted %q", proof)
			}
			return
		}
		if !errors.Is(err, ErrInvalidProof) && !errors.Is(err, ErrNoProof) {
			t.Fatalf("error of no kind: %v", err)
		}
	})
}
