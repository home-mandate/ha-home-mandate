// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// maxMissedRemovals bounds the removals kept for the next connection; past it the oldest
// go, and their notifications stay on the device with buttons that do nothing.
const maxMissedRemovals = 256

// removal is a notification to remove from a device.
type removal struct {
	service, tag string
}

// Reconnected runs after the connection to Home Assistant came back (issue #34). Answers
// tapped during the gap never arrived: each request that is still open replaces its
// notifications under the same tag, with the same buttons and a note, so that the person
// can answer again, once per request. Removals that failed during the gap follow.
func (s *Service) Reconnected() {
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()
	s.mu.Lock()
	missed := s.missed
	s.missed = nil
	var open []*pending
	if !s.stopped {
		for _, p := range s.pending {
			// Once per request: an unsteady connection must not wear the approvers down
			// (SPEC-v0 section 11.1 item 7). Without an answer the request is denied.
			if !p.done && !p.reasked {
				p.reasked = true
				open = append(open, p)
			}
		}
	}
	s.mu.Unlock()
	for _, m := range missed {
		s.remove(ctx, m)
	}
	for _, p := range open {
		for _, r := range p.recipients {
			s.askAgain(ctx, p, r)
		}
	}
}

// askAgain sends the person's notification of p again, with the note, to the devices that
// had it.
func (s *Service) askAgain(ctx context.Context, p *pending, r recipient) {
	s.mu.Lock()
	n, ok := p.notices[r.UserID]
	devices := p.devicesOf(r.UserID)
	s.mu.Unlock()
	if !ok {
		return
	}
	s.send(ctx, p, r.UserID, devices, withNote(n, i18n.T(s.language(r.Approver), i18n.ApprovalReconnected, nil)))
}

// withNote is n with note as its first line; the actions are shared, not changed.
func withNote(n ha.Notification, note string) ha.Notification {
	n.Message = note + "\n" + n.Message
	return n
}

// remove removes a notification; if that fails it waits for the next connection.
func (s *Service) remove(ctx context.Context, r removal) {
	if err := s.cfg.Clearer.ClearNotification(ctx, r.service, r.tag); err != nil {
		s.cfg.Logger.Warn("notification of an ended approval request not removed", "notify_service", r.service, "error", err)
		s.missedRemoval(r.service, r.tag)
	}
}

// missedRemoval keeps a removal for the next connection.
func (s *Service) missedRemoval(service, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.missed) == maxMissedRemovals {
		s.missed = append([]removal(nil), s.missed[1:]...)
	}
	s.missed = append(s.missed, removal{service, tag})
}
