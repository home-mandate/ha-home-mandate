// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// resendDelay is how long a cleared notification waits before it is sent again: pressing
// a button clears it too, and the answer must end the request first.
const resendDelay = 5 * time.Second

// HandleCleared takes a mobile_app_notification_cleared event (Android): a request's
// notification was swiped away or dismissed by a tap while the request may still be open
// (issue #49). After resendDelay, if the request is still open, the person gets it again
// on the devices that had it, under the same tag, once per request and person. iOS reports
// nothing; the tap target covers it. It runs on the read loop of the Home Assistant
// client: it never calls the client synchronously.
func (s *Service) HandleCleared(e ha.Event) {
	var data struct {
		Tag string `json:"tag"`
	}
	user := e.Context.UserID
	if err := json.Unmarshal(e.Data, &data); err != nil || !strings.HasPrefix(data.Tag, tagPrefix) || user == "" {
		return // not ours
	}
	s.mu.Lock()
	p := s.byTag(data.Tag)
	// Only once a device of the person has it: a report during the delivery would
	// otherwise use up the one time.
	if p == nil || p.done || s.stopped || p.resent[user] || len(p.devicesOf(user)) == 0 {
		s.mu.Unlock()
		return
	}
	p.resent[user] = true
	s.mu.Unlock()
	// Not waited for on shutdown: after StopAnswers or the end of the request it sends
	// nothing.
	time.AfterFunc(s.cfg.ResendDelay, func() { s.resend(p, user) })
}

// byTag returns the open request whose notifications carry tag, or nil; the caller holds
// the lock.
func (s *Service) byTag(tag string) *pending {
	for _, p := range s.pending {
		if p.tag == tag {
			return p
		}
	}
	return nil
}

// devicesOf returns the devices of user the request reached; the caller holds the lock.
func (p *pending) devicesOf(user string) []string {
	var devices []string
	for _, d := range p.delivered {
		if d.UserID == user {
			devices = append(devices, d.Service)
		}
	}
	return devices
}

// resend sends user's notification of p again to the devices that had it, while p is
// open. If the request ended meanwhile, what was sent is removed again: the end's own
// clearing may have come before it.
func (s *Service) resend(p *pending, user string) {
	s.mu.Lock()
	n, ok := p.notices[user]
	devices := p.devicesOf(user)
	open := ok && !p.done && !s.stopped
	s.mu.Unlock()
	if !open {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), warnTimeout)
	defer cancel()
	var sent []string
	for _, device := range devices {
		if s.ended(p) {
			break
		}
		if err := s.cfg.Notifier.Notify(ctx, device, n); err != nil {
			s.cfg.Logger.Warn("approval request not sent again", "approver", user, "notify_service", device, "error", err)
			continue
		}
		sent = append(sent, device)
	}
	if !s.ended(p) || s.cfg.Clearer == nil {
		return
	}
	for _, device := range sent {
		if err := s.cfg.Clearer.ClearNotification(ctx, device, p.tag); err != nil {
			s.cfg.Logger.Warn("notification of an ended approval request not removed", "notify_service", device, "error", err)
		}
	}
}
