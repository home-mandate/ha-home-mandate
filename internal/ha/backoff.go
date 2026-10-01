// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// nextBackoff doubles d up to limit.
func nextBackoff(d, limit time.Duration) time.Duration {
	return min(2*d, limit)
}

// jitter returns a random duration in [d/2, d] so that many gateways do not reconnect
// in lockstep after a Home Assistant restart.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return half + time.Duration(binary.LittleEndian.Uint64(b[:])%uint64(d-half+1))
}
