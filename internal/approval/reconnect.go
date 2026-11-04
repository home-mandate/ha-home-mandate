// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"time"

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

// Home Assistant takes connections before its integrations are up: for a while after a
// restart notify.mobile_app_… does not exist yet (not_found). What fails after a reconnect
// is tried again on the same connection, for about five minutes.
const (
	reconnectRetry    = 5 * time.Second
	reconnectRetryMax = time.Minute
	reconnectAttempts = 8
)

// missedAsk is a person's devices that did not get a request again after a reconnect.
type missedAsk struct {
	p       *pending
	person  Approver
	devices []string
}

// Reconnected runs after the connection to Home Assistant came back (issue #34). Answers
// tapped during the gap never arrived: each request that is still open replaces its
// notifications under the same tag, with the same buttons and a note, so that the person
// can answer again, once per request. Removals that failed during the gap follow.
func (s *Service) Reconnected() {
	s.mu.Lock()
	if !s.stopped {
		for _, p := range s.pending {
			// Once per request: an unsteady connection must not wear the approvers down
			// (SPEC-v0 section 11.1 item 7). Without an answer the request is denied.
			if p.done || p.reasked {
				continue
			}
			p.reasked = true
			for _, r := range p.recipients {
				if devices := p.devicesOf(r.UserID); len(devices) > 0 {
					s.unasked = append(s.unasked, missedAsk{p, r.Approver, devices})
				}
			}
		}
	}
	s.retryRun++
	run := s.retryRun
	s.mu.Unlock()
	if s.catchUp() {
		s.retryLater(run, 0)
	}
}

// retryLater runs catchUp again after a wait, until nothing is left, the attempts are used
// up or another reconnect took over. Removals left then wait for the next connection. Not
// waited for on shutdown, like a resend.
func (s *Service) retryLater(run uint64, attempt int) {
	wait := min(s.cfg.ReconnectRetry<<attempt, reconnectRetryMax)
	time.AfterFunc(wait, func() {
		s.mu.Lock()
		current := run == s.retryRun && !s.stopped
		s.mu.Unlock()
		if current && s.catchUp() && attempt+1 < reconnectAttempts {
			s.retryLater(run, attempt+1)
		}
	})
}

// catchUp carries out the missed removals and asks and reports whether any failed again.
func (s *Service) catchUp() bool {
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()
	s.mu.Lock()
	missed, unasked := s.missed, s.unasked
	s.missed, s.unasked = nil, nil
	s.mu.Unlock()
	for _, m := range missed {
		s.remove(ctx, m)
	}
	for _, a := range unasked {
		s.askAgain(ctx, a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.missed) > 0 || len(s.unasked) > 0
}

// askAgain sends the person's notification of a request again, with the note, to the
// devices that had it; those it fails for wait for the next try.
func (s *Service) askAgain(ctx context.Context, a missedAsk) {
	s.mu.Lock()
	n, ok := a.p.notices[a.person.UserID]
	s.mu.Unlock()
	if !ok {
		return
	}
	failed := s.send(ctx, a.p, a.person.UserID, a.devices, withNote(n, i18n.T(s.language(a.person), i18n.ApprovalReconnected, nil)))
	if len(failed) == 0 || s.ended(a.p) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unasked = append(s.unasked, missedAsk{a.p, a.person, failed})
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
