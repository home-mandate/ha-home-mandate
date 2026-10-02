// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ratelimit limits the actions of each agent with a token bucket whose capacity
// and refill rate come from the mandate's limits.max_actions_per_hour (SPEC-v0 section 4:
// the PEP enforces the limit). State is kept in memory only; a restart refills buckets.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter holds one bucket per agent.
type Limiter struct {
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a Limiter using now as its clock (time.Now if nil).
func New(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{now: now, buckets: map[string]*bucket{}}
}

// Allow takes one token from the bucket of clientID, which holds at most perHour tokens
// and refills perHour tokens per hour. A non-positive limit denies.
func (l *Limiter) Allow(clientID string, perHour int) bool {
	if perHour <= 0 {
		return false
	}
	capacity := float64(perHour)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[clientID]
	if !ok {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[clientID] = b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 { // a clock jump backwards refills nothing
		b.tokens += elapsed.Hours() * capacity
		b.last = now
	}
	b.tokens = min(b.tokens, capacity) // also applies a lowered limit at once
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
