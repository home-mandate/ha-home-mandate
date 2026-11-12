// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"sync"
	"time"
)

const (
	// nonceLifetime is how long a nonce is accepted, and how often its key rotates.
	nonceLifetime = 5 * time.Minute
	nonceMACBytes = 16
	nonceBytes    = 8 + nonceMACBytes
)

// nonces makes and checks server nonces (RFC 9449 section 8) without keeping state per
// nonce: a nonce is a timestamp with an HMAC of it. The key lives only in memory and
// rotates every nonceLifetime; the previous key is kept so that a nonce made just before a
// rotation stays valid for its lifetime. A restart invalidates all nonces, and the client
// then gets a new one with use_dpop_nonce.
type nonces struct {
	mu        sync.Mutex
	key, prev [32]byte
	rotated   time.Time
}

func newNonces(now time.Time) *nonces {
	n := &nonces{rotated: now}
	n.key = randomKey()
	n.prev = randomKey()
	return n
}

func randomKey() [32]byte {
	var k [32]byte
	_, _ = rand.Read(k[:]) // crypto/rand.Read never fails (Go 1.24+).
	return k
}

// make returns a fresh nonce, base64url of the Unix time and its MAC.
func (n *nonces) make(now time.Time) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.rotate(now)
	var raw [nonceBytes]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(now.Unix()))
	copy(raw[8:], mac(n.key, raw[:8]))
	return b64.EncodeToString(raw[:])
}

// valid reports whether s is a nonce of ours that is not older than nonceLifetime.
func (n *nonces) valid(s string, now time.Time) bool {
	raw, err := b64.DecodeString(s)
	if err != nil || len(raw) != nonceBytes {
		return false
	}
	issued := time.Unix(int64(binary.BigEndian.Uint64(raw[:8])), 0)
	if issued.After(now.Add(time.Second)) || now.Sub(issued) > nonceLifetime {
		return false
	}
	n.mu.Lock()
	n.rotate(now)
	key, prev := n.key, n.prev
	n.mu.Unlock()
	got := raw[8:]
	return subtle.ConstantTimeCompare(got, mac(key, raw[:8])) == 1 || subtle.ConstantTimeCompare(got, mac(prev, raw[:8])) == 1
}

func (n *nonces) rotate(now time.Time) {
	if now.Sub(n.rotated) < nonceLifetime {
		return
	}
	n.prev, n.key, n.rotated = n.key, randomKey(), now
}

func mac(key [32]byte, data []byte) []byte {
	h := hmac.New(sha256.New, key[:])
	h.Write(data)
	return h.Sum(nil)[:nonceMACBytes]
}
