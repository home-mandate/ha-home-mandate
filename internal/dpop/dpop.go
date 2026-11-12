// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dpop verifies DPoP proofs (RFC 9449 section 4.3) with the standard library only:
// ES256 and EdDSA, typ dpop+jwt, a public jwk, htm, htu, iat within ±60 s, ath at the
// resource, a server nonce where it is required, and a jti that is not used twice. The
// thumbprint of the key (RFC 7638) is what tokens are bound to (cnf.jkt).
//
// Check is stateless and safe to run on any request. Accept records the jti; callers run it
// only once the request is otherwise authorized (a valid grant at the token endpoint, a
// known token at the resource), so that nobody without a grant or token can fill the
// replay cache.
package dpop

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	algES256 = "ES256"
	algEdDSA = "EdDSA"

	// HeaderName is the request header of a proof, NonceHeader the response header of a
	// server nonce (RFC 9449 sections 4.1 and 8).
	HeaderName  = "DPoP"
	NonceHeader = "DPoP-Nonce"

	// Window is how far iat may lie from the time of the server, either way.
	Window = 60 * time.Second
	// maxProofBytes bounds a proof; a P-256 proof with ath and nonce is about 600 bytes.
	maxProofBytes = 4096
	maxJTIBytes   = 256
	// replayEntries bounds the replay cache: at 2 * Window per entry, 20 000 proofs within
	// two minutes, far more than one household's agents send.
	replayEntries = 20_000
)

// Mode is whether the gateway offers DPoP (app option dpop, HM_DPOP).
type Mode string

const (
	// ModeOff: no DPoP in the metadata and challenges, Bearer tokens only. Agents bound
	// before stay bound and are still checked with DPoP.
	ModeOff Mode = "off"
	// ModeOptional: tokens are bound when the token request carries a proof.
	ModeOptional Mode = "optional"
	// ModeRequired: only bound tokens; the metadata say dpop_bound_access_tokens_required.
	ModeRequired Mode = "required"
)

// ParseMode reads a mode; empty is ModeOff.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case "":
		return ModeOff, nil
	case ModeOff, ModeOptional, ModeRequired:
		return m, nil
	}
	return "", errors.New("dpop must be off, optional or required")
}

// Algorithms lists the signature algorithms of a proof, for dpop_signing_alg_values_supported.
var Algorithms = []string{algES256, algEdDSA}

var (
	// ErrNoProof: the request carries no DPoP header.
	ErrNoProof = errors.New("no DPoP proof")
	// ErrInvalidProof: the proof is malformed, wrongly signed, for another request, too
	// old or too new, or replayed (error invalid_dpop_proof).
	ErrInvalidProof = errors.New("invalid DPoP proof")
	// ErrUseNonce: the proof lacks a valid server nonce (error use_dpop_nonce); the caller
	// sends a fresh one from Verifier.Nonce.
	ErrUseNonce = errors.New("DPoP nonce required")
)

var b64 = base64.RawURLEncoding.Strict()

// Expect describes the request a proof has to belong to.
type Expect struct {
	// Method is the HTTP method; URL is the request URI as the gateway is reached from
	// outside (its public URL and the path), never taken from the Host header.
	Method, URL string
	// AccessToken, if set, has to be matched by ath (at the resource, section 7).
	AccessToken string
	// Nonce requires a nonce of this Verifier (section 8).
	Nonce bool
}

// Proof is a checked proof.
type Proof struct {
	// JKT is the base64url SHA-256 thumbprint of the key (RFC 7638).
	JKT string
	jti string
	iat time.Time
}

// Verifier checks proofs. It holds the nonce key and the replay cache, so the gateway
// has exactly one.
type Verifier struct {
	now    func() time.Time
	nonces *nonces
	replay *replayCache
}

// NewVerifier returns a Verifier on the clock now.
func NewVerifier(now func() time.Time) *Verifier {
	return &Verifier{now: now, nonces: newNonces(now()), replay: newReplayCache(replayEntries)}
}

// Nonce returns a fresh server nonce for the DPoP-Nonce header.
func (v *Verifier) Nonce() string { return v.nonces.make(v.now()) }

// Verify is Check followed by Accept.
func (v *Verifier) Verify(h http.Header, e Expect) (Proof, error) {
	p, err := v.Check(h, e)
	if err != nil {
		return Proof{}, err
	}
	return p, v.Accept(p)
}

// Accept records the jti of a checked proof; a second use of it is ErrInvalidProof.
func (v *Verifier) Accept(p Proof) error {
	if p.jti == "" {
		return fmt.Errorf("%w: proof was not checked", ErrInvalidProof)
	}
	// One key's jti space is its own; the key is part of the cache key.
	if err := v.replay.add(p.JKT+"."+p.jti, p.iat.Add(Window), v.now()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidProof, err)
	}
	return nil
}

// Check verifies the single DPoP header of h against e, without recording its jti.
func (v *Verifier) Check(h http.Header, e Expect) (Proof, error) {
	values := h.Values(HeaderName)
	switch {
	case len(values) == 0:
		return Proof{}, ErrNoProof
	case len(values) > 1:
		return Proof{}, fmt.Errorf("%w: more than one DPoP header", ErrInvalidProof)
	}
	p, err := v.check(values[0], e)
	if err != nil && !errors.Is(err, ErrUseNonce) {
		return Proof{}, fmt.Errorf("%w: %w", ErrInvalidProof, err)
	}
	return p, err
}

func (v *Verifier) check(proof string, e Expect) (Proof, error) {
	if len(proof) > maxProofBytes {
		return Proof{}, errors.New("proof too long")
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		return Proof{}, errors.New("not a compact JWS")
	}
	key, err := checkHeader(parts[0])
	if err != nil {
		return Proof{}, err
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !verifySignature(key, []byte(parts[0]+"."+parts[1]), sig) {
		return Proof{}, errors.New("signature does not verify")
	}
	c, err := readClaims(parts[1])
	if err != nil {
		return Proof{}, err
	}
	now := v.now()
	if err := c.match(e, now); err != nil {
		return Proof{}, err
	}
	if e.Nonce && !v.nonces.valid(c.Nonce, now) {
		return Proof{JKT: key.jkt}, ErrUseNonce
	}
	return Proof{JKT: key.jkt, jti: c.JTI, iat: c.iat}, nil
}

// checkHeader reads the protected header: typ, a supported alg, a public jwk, and no crit.
func checkHeader(segment string) (publicKey, error) {
	members, err := decodeObject(segment)
	if err != nil {
		return publicKey{}, fmt.Errorf("header: %w", err)
	}
	if stringMember(members, "typ") != "dpop+jwt" {
		return publicKey{}, errors.New("typ is not dpop+jwt")
	}
	if _, ok := members["crit"]; ok {
		return publicKey{}, errors.New("crit is not supported")
	}
	alg := stringMember(members, "alg")
	if alg != algES256 && alg != algEdDSA {
		return publicKey{}, errors.New("alg is not ES256 or EdDSA")
	}
	return parseJWK(members["jwk"], alg)
}

func verifySignature(key publicKey, input, sig []byte) bool {
	switch k := key.key.(type) {
	case ed25519.PublicKey:
		return len(sig) == ed25519.SignatureSize && ed25519.Verify(k, input, sig)
	case *ecdsa.PublicKey:
		if len(sig) != 64 {
			return false
		}
		digest := sha256.Sum256(input)
		return ecdsa.Verify(k, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
	}
	return false
}

type claims struct {
	JTI   string `json:"jti"`
	HTM   string `json:"htm"`
	HTU   string `json:"htu"`
	ATH   string `json:"ath"`
	Nonce string `json:"nonce"`
	iat   time.Time
}

func readClaims(segment string) (claims, error) {
	members, err := decodeObject(segment)
	if err != nil {
		return claims{}, fmt.Errorf("claims: %w", err)
	}
	c := claims{JTI: stringMember(members, "jti"), HTM: stringMember(members, "htm"), HTU: stringMember(members, "htu"),
		ATH: stringMember(members, "ath"), Nonce: stringMember(members, "nonce")}
	var iat float64
	if raw, ok := members["iat"]; !ok || json.Unmarshal(raw, &iat) != nil || math.IsNaN(iat) || iat < 0 || iat > math.MaxInt32*4 {
		return claims{}, errors.New("iat is not a NumericDate")
	}
	c.iat = time.Unix(int64(iat), 0)
	if c.JTI == "" || len(c.JTI) > maxJTIBytes {
		return claims{}, errors.New("jti is missing or too long")
	}
	return c, nil
}

// match compares the claims with the request.
func (c claims) match(e Expect, now time.Time) error {
	if c.HTM != e.Method {
		return errors.New("htm does not match the method")
	}
	got, ok := normalizeHTU(c.HTU)
	want, wantOK := normalizeHTU(e.URL)
	if !ok || !wantOK || got != want {
		return errors.New("htu does not match the request")
	}
	if c.iat.Before(now.Add(-Window)) || c.iat.After(now.Add(Window)) {
		return errors.New("iat is outside the time window")
	}
	if e.AccessToken != "" && c.ATH != AccessTokenHash(e.AccessToken) {
		return errors.New("ath does not match the access token")
	}
	return nil
}

// AccessTokenHash is the ath of a token: base64url of its SHA-256.
func AccessTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return b64.EncodeToString(sum[:])
}

// decodeObject decodes a base64url segment that holds one JSON object whose members are
// all named once at every level; encoding/json would silently take the last of two.
func decodeObject(segment string) (map[string]json.RawMessage, error) {
	data, err := b64.DecodeString(segment)
	if err != nil {
		return nil, errors.New("not base64url")
	}
	if err := uniqueMembers(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return nil, errors.New("not a JSON object")
	}
	return members, nil
}

const maxDepth = 8

// uniqueMembers walks one JSON value and fails on a repeated member name or deep nesting.
func uniqueMembers(dec *json.Decoder, depth int) error {
	if depth > maxDepth {
		return errors.New("nested too deeply")
	}
	t, err := dec.Token()
	if err != nil {
		return errors.New("not valid JSON")
	}
	delim, ok := t.(json.Delim)
	if !ok || delim == '}' || delim == ']' {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if delim == '{' {
			name, err := dec.Token()
			if err != nil {
				return errors.New("not valid JSON")
			}
			key, _ := name.(string)
			if seen[key] {
				return errors.New("a member is named twice")
			}
			seen[key] = true
		}
		if err := uniqueMembers(dec, depth+1); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return errors.New("not valid JSON")
	}
	return nil
}
