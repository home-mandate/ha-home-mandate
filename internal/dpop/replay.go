// SPDX-License-Identifier: AGPL-3.0-or-later

package dpop

import (
	"sync"
	"time"
)

// replayCache remembers the jti of every accepted proof until its iat leaves the window
// (RFC 9449 section 11.1). The gateway is one process, so memory is enough. The cache is
// bounded: when it is full of proofs that are still within the window, a new proof is
// refused rather than an old one forgotten, so a flood can never open a way to replay.
type replayCache struct {
	mu      sync.Mutex
	max     int
	expires map[string]time.Time
}

func newReplayCache(max int) *replayCache {
	return &replayCache{max: max, expires: make(map[string]time.Time)}
}

// errReplay and errCacheFull are reasons for ErrInvalidProof.
type replayError string

func (e replayError) Error() string { return string(e) }

const (
	errReplay    replayError = "jti was used before"
	errCacheFull replayError = "too many proofs within the time window"
)

// add records key until expires; it fails for a key seen before and when the cache is
// full after dropping what has expired.
func (c *replayCache) add(key string, expires, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until, ok := c.expires[key]; ok && now.Before(until) {
		return errReplay
	}
	if len(c.expires) >= c.max {
		for k, until := range c.expires {
			if !now.Before(until) {
				delete(c.expires, k)
			}
		}
		if len(c.expires) >= c.max {
			return errCacheFull
		}
	}
	c.expires[key] = expires
	return nil
}
