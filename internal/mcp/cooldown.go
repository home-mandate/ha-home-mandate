// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import "time"

const (
	// cooldownMin is how long an agent waits before it may ask again for a device after a
	// refusal, a timeout or an invalid answer, against wearing the approvers down; every
	// further one doubles the wait up to cooldownMax. An approval ends the waits, and so
	// does a quiet cooldownMax after the last one.
	cooldownMin = time.Minute
	cooldownMax = time.Hour
)

// cooldown is the wait of one agent for one device.
type cooldown struct {
	until time.Time
	wait  time.Duration // the last wait
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

// coolDown starts the next wait of the agent for entityID after a request that was not
// approved.
func (g *Gateway) coolDown(clientID, entityID string) {
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
	wait := cooldownMin
	if c, ok := g.cooldowns[key]; ok && now.Before(c.until.Add(cooldownMax)) {
		wait = min(2*c.wait, cooldownMax)
	}
	g.cooldowns[key] = cooldown{until: now.Add(wait), wait: wait}
}

// forgive ends the waits of the agent for entityID after an approval.
func (g *Gateway) forgive(clientID, entityID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.cooldowns, cooldownKey(clientID, entityID))
}
