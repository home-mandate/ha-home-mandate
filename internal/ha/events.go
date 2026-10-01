// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"errors"
	"maps"
)

// Subscription is an event subscription. It survives reconnects.
type Subscription struct {
	c         *Client
	eventType string
	handler   func(Event)

	// guarded by c.mu: active on the connection with generation gen under id.
	gen uint64
	id  int64
	// regGen is the connection generation at registration. Subscriptions registered on
	// the current connection are activated by SubscribeEvents itself, older ones by
	// resubscribe, so that SubscribeEvents returns only after HA confirmed.
	regGen uint64
}

// SubscribeEvents subscribes to an allowlisted event type. handler runs on the read
// loop and must return quickly; it must not call the client synchronously. While
// disconnected, the subscription is registered and activated on the next connection.
func (c *Client) SubscribeEvents(ctx context.Context, eventType string, handler func(Event)) (*Subscription, error) {
	if handler == nil {
		return nil, errors.New("home assistant: nil event handler")
	}
	if err := checkAllowed(subscribeCommand(eventType)); err != nil {
		return nil, err
	}
	s := &Subscription{c: c, eventType: eventType, handler: handler}
	c.mu.Lock()
	s.regGen = c.gen
	c.subs[s] = struct{}{}
	c.mu.Unlock()

	err := c.activate(ctx, s)
	if err == nil || errors.Is(err, ErrDisconnected) {
		return s, nil
	}
	c.mu.Lock()
	delete(c.subs, s)
	c.mu.Unlock()
	return nil, err
}

// Unsubscribe ends the subscription. It is safe to call more than once.
func (s *Subscription) Unsubscribe(ctx context.Context) error {
	c := s.c
	c.mu.Lock()
	_, registered := c.subs[s]
	delete(c.subs, s)
	active := registered && s.gen == c.gen && c.conn != nil
	id := s.id
	if active {
		delete(c.subByID, id)
	}
	s.gen = 0
	c.mu.Unlock()

	if !active {
		return nil
	}
	_, err := c.request(ctx, command{Type: "unsubscribe_events", Fields: map[string]any{"subscription": id}})
	if errors.Is(err, ErrDisconnected) {
		return nil
	}
	return err
}

func subscribeCommand(eventType string) command {
	return command{Type: "subscribe_events", Fields: map[string]any{"event_type": eventType}}
}

// activate subscribes s on the current connection unless it already is.
func (c *Client) activate(ctx context.Context, s *Subscription) error {
	var id int64
	_, err := c.send(ctx, subscribeCommand(s.eventType), func(newID int64, gen uint64) bool {
		if _, ok := c.subs[s]; !ok || s.gen == gen {
			return false
		}
		id, s.gen, s.id = newID, gen, newID
		c.subByID[newID] = s
		return true
	})
	if errors.Is(err, errSkipped) {
		return nil
	}
	if err != nil && id != 0 {
		c.mu.Lock()
		if s.id == id {
			delete(c.subByID, id)
			s.gen = 0
		}
		c.mu.Unlock()
		// Cancelled after the command was written: HA may hold the subscription.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			go c.unsubscribeID(id)
		}
	}
	return err
}

// unsubscribeID ends a subscription on HA on a best-effort basis.
func (c *Client) unsubscribeID(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	_, _ = c.request(ctx, command{Type: "unsubscribe_events", Fields: map[string]any{"subscription": id}})
}

func (c *Client) resubscribe(ctx context.Context) {
	c.mu.Lock()
	var subs []*Subscription
	for s := range maps.Keys(c.subs) {
		if s.regGen < c.gen {
			subs = append(subs, s)
		}
	}
	c.mu.Unlock()
	for _, s := range subs {
		err := c.activate(ctx, s)
		if err != nil && !errors.Is(err, ErrDisconnected) && ctx.Err() == nil {
			c.log.Error("home assistant subscription failed", "event_type", s.eventType, "error", err)
		}
	}
}
