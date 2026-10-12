// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import "time"

const (
	// cooldownMin is how long an agent waits before it may ask again for a device after a
	// refusal, an invalid answer, a withdrawal or the second timeout in a row, against
	// wearing the approvers down; every further one doubles the wait up to cooldownMax. A
	// single timeout starts no wait (decision 2026-10-10: the human may not have seen the
	// request; the agent asks once more). An approval ends the waits, and so does a quiet
	// cooldownMax after the last one.
	cooldownMin = time.Minute
	cooldownMax = time.Hour
)

// cooldown is the wait of one agent for one device.
type cooldown struct {
	until    time.Time
	wait     time.Duration // the last wait; zero after a single timeout
	timedOut bool          // the last request ended by timeout
}

func cooldownKey(clientID, entityID string) string {
	return clientID + " " + entityID
}

// cooling returns how long the agent must still wait before it may ask for entityID; 0
// if it may.
func (g *Gateway) cooling(clientID, entityID string) time.Duration {
	now := g.cfg.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	key := cooldownKey(clientID, entityID)
	c, ok := g.cooldowns[key]
	if !ok {
		return 0
	}
	if !now.Before(c.until.Add(cooldownMax)) { // a quiet hour: forgotten
		delete(g.cooldowns, key)
	}
	return max(c.until.Sub(now), 0)
}

// coolDown starts the next wait of the agent for entityID after a request that was
// refused, answered invalidly or withdrawn.
func (g *Gateway) coolDown(clientID, entityID string) {
	g.startWait(clientID, entityID, false)
}

// coolDownAfterTimeout notes a request that timed out: the first in a row starts no wait,
// a further one does.
func (g *Gateway) coolDownAfterTimeout(clientID, entityID string) {
	g.startWait(clientID, entityID, true)
}

func (g *Gateway) startWait(clientID, entityID string, timeout bool) {
	now := g.cfg.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	// Forget the waits of a quiet hour, so that devices never asked about again do not stay.
	for k, c := range g.cooldowns {
		if !now.Before(c.until.Add(cooldownMax)) {
			delete(g.cooldowns, k)
		}
	}
	key := cooldownKey(clientID, entityID)
	c, ok := g.cooldowns[key]
	if timeout && !(ok && c.timedOut) {
		// The first timeout in a row: noted, no wait; a wait running already stands.
		if !ok {
			c = cooldown{until: now}
		}
		c.timedOut = true
		g.cooldowns[key] = c
		return
	}
	wait := cooldownMin
	if ok && c.wait > 0 {
		wait = min(2*c.wait, cooldownMax)
	}
	g.cooldowns[key] = cooldown{until: now.Add(wait), wait: wait, timedOut: timeout}
}

// forgive ends the waits of the agent for entityID after an approval.
func (g *Gateway) forgive(clientID, entityID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.cooldowns, cooldownKey(clientID, entityID))
}
