// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dpoptest makes DPoP proofs (RFC 9449) for tests and for the test client
// tools/dpopclient: a client's side of what package dpop checks. It never verifies.
package dpoptest

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// Algorithms of a Key.
const (
	ES256 = "ES256"
	EdDSA = "EdDSA"
)

var b64 = base64.RawURLEncoding

// Key is a client's DPoP key.
type Key struct {
	alg string
	ec  *ecdsa.PrivateKey
	ed  ed25519.PrivateKey
}

// NewKey returns a fresh key for alg.
func NewKey(alg string) (*Key, error) {
	switch alg {
	case EdDSA:
		_, k, err := ed25519.GenerateKey(rand.Reader)
		return &Key{alg: alg, ed: k}, err
	case ES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		return &Key{alg: alg, ec: k}, err
	}
	return nil, errors.New("dpoptest: alg must be ES256 or EdDSA")
}

// FixedKey returns the key of seed, the same in every process (32 bytes, not zero).
func FixedKey(alg string, seed [32]byte) (*Key, error) {
	switch alg {
	case EdDSA:
		return &Key{alg: alg, ed: ed25519.NewKeyFromSeed(seed[:])}, nil
	case ES256:
		k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), seed[:])
		return &Key{alg: alg, ec: k}, err
	}
	return nil, errors.New("dpoptest: alg must be ES256 or EdDSA")
}

// Alg is the key's algorithm.
func (k *Key) Alg() string { return k.alg }

// JWK is the public key as the jwk header member holds it.
func (k *Key) JWK() map[string]any {
	if k.alg == EdDSA {
		return map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64.EncodeToString(k.ed.Public().(ed25519.PublicKey))}
	}
	b, _ := k.ec.PublicKey.Bytes()
	return map[string]any{"kty": "EC", "crv": "P-256", "x": b64.EncodeToString(b[1:33]), "y": b64.EncodeToString(b[33:])}
}

// JKT is the RFC 7638 thumbprint of the key.
func (k *Key) JKT() string {
	jwk := k.JWK()
	canonical := `{"crv":"` + jwk["crv"].(string) + `","kty":"` + jwk["kty"].(string) + `","x":"` + jwk["x"].(string) + `"`
	if y, ok := jwk["y"].(string); ok {
		canonical += `,"y":"` + y + `"`
	}
	sum := sha256.Sum256([]byte(canonical + "}"))
	return b64.EncodeToString(sum[:])
}

// Claims are the claims of a proof that a client sets.
type Claims struct {
	Method, URL string
	// AccessToken, if set, gives ath; Nonce, if set, nonce.
	AccessToken, Nonce string
	IssuedAt           time.Time
}

// Proof returns a proof for c with a fresh jti.
func (k *Key) Proof(c Claims) (string, error) {
	jti := make([]byte, 16)
	_, _ = rand.Read(jti) // crypto/rand.Read never fails (Go ≥ 1.24)
	claims := map[string]any{"jti": b64.EncodeToString(jti), "htm": c.Method, "htu": c.URL, "iat": c.IssuedAt.Unix()}
	if c.AccessToken != "" {
		sum := sha256.Sum256([]byte(c.AccessToken))
		claims["ath"] = b64.EncodeToString(sum[:])
	}
	if c.Nonce != "" {
		claims["nonce"] = c.Nonce
	}
	return k.ProofWith(nil, claims)
}

// ProofWith signs claims under the header typ, alg and jwk, which edit may change; for
// tests of malformed proofs.
func (k *Key) ProofWith(edit func(header map[string]any), claims map[string]any) (string, error) {
	header := map[string]any{"typ": "dpop+jwt", "alg": k.alg, "jwk": k.JWK()}
	if edit != nil {
		edit(header)
	}
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return k.SignRaw(h, c)
}

// SignRaw signs a header and claims given as bytes, so that tests can send what
// encoding/json would never write.
func (k *Key) SignRaw(header, claims []byte) (string, error) {
	input := b64.EncodeToString(header) + "." + b64.EncodeToString(claims)
	sig, err := k.sign([]byte(input))
	if err != nil {
		return "", err
	}
	return input + "." + b64.EncodeToString(sig), nil
}

func (k *Key) sign(input []byte) ([]byte, error) {
	if k.alg == EdDSA {
		return ed25519.Sign(k.ed, input), nil
	}
	digest := sha256.Sum256(input)
	r, s, err := ecdsa.Sign(rand.Reader, k.ec, digest[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	s.FillBytes(out[32:])
	return out, nil
}
