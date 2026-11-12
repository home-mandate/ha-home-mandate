// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
)

// privateMembers are the JWK members of a private or symmetric key (RFC 7518 section 6).
// A proof whose jwk carries one of them is refused (RFC 9449 section 4.3 item 7).
var privateMembers = []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"}

// publicKey is the key of a proof with the RFC 7638 thumbprint it is bound by.
type publicKey struct {
	key crypto.PublicKey
	alg string
	jkt string
}

// parseJWK reads the public key of the header member jwk: P-256 for ES256, Ed25519 for
// EdDSA. Other members, such as use or kid, are ignored (RFC 7517 section 4).
func parseJWK(raw json.RawMessage, alg string) (publicKey, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return publicKey{}, errors.New("jwk is not an object")
	}
	for _, name := range privateMembers {
		if _, ok := members[name]; ok {
			return publicKey{}, errors.New("jwk holds a private key")
		}
	}
	kty, crv := stringMember(members, "kty"), stringMember(members, "crv")
	switch {
	case alg == algES256 && kty == "EC" && crv == "P-256":
		return parseP256(members)
	case alg == algEdDSA && kty == "OKP" && crv == "Ed25519":
		return parseEd25519(members)
	}
	return publicKey{}, errors.New("jwk does not fit the algorithm")
}

func parseP256(members map[string]json.RawMessage) (publicKey, error) {
	x, errX := decodeFixed(stringMember(members, "x"), 32)
	y, errY := decodeFixed(stringMember(members, "y"), 32)
	if errX != nil || errY != nil {
		return publicKey{}, errors.New("jwk coordinates are not 32 bytes of base64url")
	}
	// ParseUncompressedPublicKey checks that the point lies on the curve.
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	if err != nil {
		return publicKey{}, errors.New("jwk is not a point on P-256")
	}
	jkt := thumbprint(`{"crv":"P-256","kty":"EC","x":"` + b64.EncodeToString(x) + `","y":"` + b64.EncodeToString(y) + `"}`)
	return publicKey{key: key, alg: algES256, jkt: jkt}, nil
}

func parseEd25519(members map[string]json.RawMessage) (publicKey, error) {
	if _, ok := members["y"]; ok {
		return publicKey{}, errors.New("jwk of Ed25519 with y")
	}
	x, err := decodeFixed(stringMember(members, "x"), ed25519.PublicKeySize)
	if err != nil {
		return publicKey{}, errors.New("jwk x is not 32 bytes of base64url")
	}
	if !canonicalEd25519(x) || smallOrderEd25519[[ed25519.PublicKeySize]byte(x)] {
		return publicKey{}, errors.New("jwk is a non-canonical or small-order Ed25519 key")
	}
	jkt := thumbprint(`{"crv":"Ed25519","kty":"OKP","x":"` + b64.EncodeToString(x) + `"}`)
	return publicKey{key: ed25519.PublicKey(x), alg: algEdDSA, jkt: jkt}, nil
}

// thumbprint is the RFC 7638 thumbprint of the canonical JWK: the required members in
// lexicographic order, without white space. The members are base64url, so nothing needs
// escaping.
func thumbprint(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return b64.EncodeToString(sum[:])
}

func stringMember(members map[string]json.RawMessage, name string) string {
	var s string
	if raw, ok := members[name]; !ok || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func decodeFixed(s string, size int) ([]byte, error) {
	b, err := b64.DecodeString(s)
	if err != nil || len(b) != size {
		return nil, errors.New("wrong length")
	}
	return b, nil
}

// smallOrderEd25519 holds the encodings of the Ed25519 points of small order (as SPEC-v0
// section 7.1 lists them): with such a key a signature verifies for whole classes of
// messages, so anyone could make proofs for a token bound to it.
var smallOrderEd25519 = func() map[[ed25519.PublicKeySize]byte]bool {
	set := make(map[[ed25519.PublicKeySize]byte]bool)
	for _, encoded := range []string{
		"0100000000000000000000000000000000000000000000000000000000000000",
		"0100000000000000000000000000000000000000000000000000000000000080",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0000000000000000000000000000000000000000000000000000000000000080",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc85",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac03fa",
	} {
		key, err := hex.DecodeString(encoded)
		if err != nil {
			panic(err)
		}
		set[[ed25519.PublicKeySize]byte(key)] = true
	}
	return set
}()

// ed25519FieldPrime is p = 2^255 - 19.
var ed25519FieldPrime = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))

// canonicalEd25519 reports whether a point encoding has y < p: the little-endian value of
// the bytes without the sign bit. crypto/ed25519 accepts non-canonical encodings, which
// would give one key two thumbprints.
func canonicalEd25519(encoded []byte) bool {
	y := make([]byte, len(encoded))
	for i, b := range encoded {
		y[len(encoded)-1-i] = b
	}
	y[0] &= 0x7f
	return new(big.Int).SetBytes(y).Cmp(ed25519FieldPrime) < 0
}
