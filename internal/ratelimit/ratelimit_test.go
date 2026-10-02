// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newLimiter() (*Limiter, *clock) {
	c := &clock{now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	return New(c.Now), c
}

func TestBurstUpToTheHourlyLimitThenDeny(t *testing.T) {
	l, _ := newLimiter()
	for i := range 3 {
		if !l.Allow("agent-a", 3) {
			t.Fatalf("request %d denied", i+1)
		}
	}
	if l.Allow("agent-a", 3) {
		t.Error("request 4 allowed with a limit of 3 per hour")
	}
}

func TestRefillsContinuously(t *testing.T) {
	l, c := newLimiter()
	for range 60 {
		l.Allow("agent-a", 60)
	}
	if l.Allow("agent-a", 60) {
		t.Fatal("allowed after the bucket was empty")
	}
	c.advance(59 * time.Second)
	if l.Allow("agent-a", 60) {
		t.Error("allowed before one token was refilled")
	}
	c.advance(time.Second)
	if !l.Allow("agent-a", 60) {
		t.Error("denied after one token was refilled")
	}
	c.advance(10 * time.Hour)
	allowed := 0
	for range 100 {
		if l.Allow("agent-a", 60) {
			allowed++
		}
	}
	if allowed != 60 {
		t.Errorf("after a long pause %d requests allowed, want the capacity 60", allowed)
	}
}

func TestAgentsAreIndependent(t *testing.T) {
	l, _ := newLimiter()
	l.Allow("agent-a", 1)
	if l.Allow("agent-a", 1) {
		t.Error("agent-a over its limit")
	}
	if !l.Allow("agent-b", 1) {
		t.Error("agent-b limited by agent-a")
	}
}

func TestLowerLimitTakesEffectImmediately(t *testing.T) {
	l, _ := newLimiter()
	l.Allow("agent-a", 100) // bucket of 100, 99 left
	allowed := 0
	for range 20 {
		if l.Allow("agent-a", 5) {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("after lowering the limit to 5, %d requests allowed", allowed)
	}
}

func TestInvalidLimitDenies(t *testing.T) {
	l, _ := newLimiter()
	for _, limit := range []int{0, -1} {
		if l.Allow("agent-a", limit) {
			t.Errorf("limit %d allowed a request", limit)
		}
	}
}

func TestClockGoingBackwardsGrantsNothing(t *testing.T) {
	l, c := newLimiter()
	l.Allow("agent-a", 1)
	c.advance(-time.Hour)
	if l.Allow("agent-a", 1) {
		t.Error("a clock jump backwards refilled the bucket")
	}
}

func TestConcurrentUseNeverExceedsTheLimit(t *testing.T) {
	l, _ := newLimiter()
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 50 {
		wg.Go(func() {
			if l.Allow("agent-a", 10) {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if allowed != 10 {
		t.Errorf("%d concurrent requests allowed, want 10", allowed)
	}
}

func TestDefaultClock(t *testing.T) {
	if !New(nil).Allow("a", 1) {
		t.Error("first request denied with the real clock")
	}
}
