// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// An answer tapped while Home Assistant was away is lost. After the reconnect the request
// that is still open asks again, under its tag, with the same nonce: the answer then counts.
func TestOpenRequestAsksAgainAfterAReconnect(t *testing.T) {
	je := newJournalEnv(t)
	ch := je.ask(journaled())
	first := je.notifier.next(t)
	second := je.notifier.next(t)
	third := je.notifier.next(t)

	je.delivered(t, 3)
	je.svc.Reconnected()

	got := map[string]sent{}
	for range 3 {
		s := je.notifier.next(t)
		got[s.service] = s
	}
	for _, before := range []sent{first, second, third} {
		s, ok := got[before.service]
		if !ok {
			t.Fatalf("%s not asked again: %v", before.service, got)
		}
		if s.n.Tag != before.n.Tag || !slices.Equal(s.n.Actions, before.n.Actions) {
			t.Errorf("%s: tag or buttons changed: %+v", before.service, s.n)
		}
		if !strings.HasSuffix(s.n.Message, "\n"+before.n.Message) {
			t.Errorf("%s: message = %q", before.service, s.n.Message)
		}
	}
	je.svc.HandleEvent(event("HM_APPROVE_"+nonceOf(t, first), u1))
	if a := wait(t, ch); a.res.Outcome != OutcomeApproved {
		t.Errorf("answer after the reconnect = %s", a.res.Outcome)
	}
}

// The note is in the language of the person who gets it.
func TestAskingAgainUsesTheApproversLanguage(t *testing.T) {
	je := newJournalEnv(t)
	je.ask(journaled())
	for range 3 {
		je.notifier.next(t)
	}
	je.delivered(t, 3)
	je.svc.Reconnected()
	want := map[string]string{
		"mobile_app_markus": i18n.T(i18n.DE, i18n.ApprovalReconnected, nil),
		"mobile_app_anna":   i18n.T(i18n.EN, i18n.ApprovalReconnected, nil),
	}
	for range 3 {
		s := je.notifier.next(t)
		if note, ok := want[s.service]; ok && !strings.HasPrefix(s.n.Message, note+"\n") {
			t.Errorf("%s: message = %q", s.service, s.n.Message)
		}
	}
}

// What ended is not asked again, and a device that never had the request does not get it.
func TestOnlyOpenRequestsAskAgainOnDevicesThatHadThem(t *testing.T) {
	je := newJournalEnv(t)
	je.notifier.setFail("mobile_app_tablet", true)
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	_ = je.notifier.next(t)
	je.notifier.setFail("mobile_app_tablet", false)

	je.delivered(t, 2)
	je.svc.Reconnected()
	again := []string{je.notifier.next(t).service, je.notifier.next(t).service}
	if slices.Contains(again, "mobile_app_tablet") {
		t.Errorf("asked again on %v", again)
	}

	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u1))
	wait(t, ch)
	je.svc.background.Wait()
	before := je.notifier.count()
	je.svc.Reconnected()
	if je.notifier.count() != before {
		t.Error("an ended request asked again")
	}
}

// A request ended during the gap could not be removed from the devices; its buttons would
// stay there and do nothing. The removal follows with the reconnect, once.
func TestRemovalMissedDuringTheGapFollowsWithTheReconnect(t *testing.T) {
	je := newJournalEnv(t)
	je.clearer.err = errors.New("disconnected")
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u1))
	wait(t, ch)
	je.svc.background.Wait()
	failed := len(je.clearer.list())

	je.clearer.mu.Lock()
	je.clearer.err = nil
	je.clearer.mu.Unlock()
	before := je.notifier.count()
	je.svc.Reconnected()
	if got := je.clearer.list()[failed:]; len(got) != failed {
		t.Errorf("after the reconnect cleared %+v, want %d", got, failed)
	}
	if je.notifier.count() != before {
		t.Error("an ended request asked again")
	}
	je.svc.Reconnected()
	if got := len(je.clearer.list()); got != 2*failed {
		t.Errorf("cleared %d times, want %d", got, 2*failed)
	}
}

// Missed removals are bounded: an unreachable Home Assistant cannot grow the list for ever.
func TestMissedRemovalsAreBounded(t *testing.T) {
	je := newJournalEnv(t)
	for range maxMissedRemovals + 10 {
		je.svc.missedRemoval("mobile_app_markus", "hm_request_x")
	}
	je.svc.mu.Lock()
	defer je.svc.mu.Unlock()
	if len(je.svc.missed) != maxMissedRemovals {
		t.Errorf("kept %d", len(je.svc.missed))
	}
}

// An unsteady connection asks once per request, not once per reconnect.
func TestARequestAsksAgainOnce(t *testing.T) {
	je := newJournalEnv(t)
	je.ask(journaled())
	for range 3 {
		je.notifier.next(t)
	}
	je.delivered(t, 3)
	je.svc.Reconnected()
	for range 3 {
		je.notifier.next(t)
	}
	before := je.notifier.count()
	je.svc.Reconnected()
	if je.notifier.count() != before {
		t.Error("asked again a second time")
	}
}

// After StopAnswers nothing asks again: the buttons would do nothing.
func TestNothingAsksAgainAfterAnswersStopped(t *testing.T) {
	je := newJournalEnv(t)
	je.ask(journaled())
	for range 3 {
		je.notifier.next(t)
	}
	before := je.notifier.count()
	je.svc.StopAnswers()
	je.svc.Reconnected()
	if je.notifier.count() != before {
		t.Error("asked again after StopAnswers")
	}
}

// Home Assistant takes connections before its integrations are up: right after a restart
// notify.mobile_app_… does not exist yet. A removal that fails then is tried again without
// waiting for another connection.
func TestRemovalIsRetriedWhileTheNotifyServiceIsNotThereYet(t *testing.T) {
	je := newJournalEnv(t)
	je.svc.cfg.ReconnectRetry = 5 * time.Millisecond
	je.clearer.err = errors.New("disconnected")
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u1))
	wait(t, ch)
	je.svc.background.Wait()

	before := len(je.clearer.list())
	je.svc.Reconnected()
	waitUntil(t, func() bool { return len(je.clearer.list()) > before+3 }, "the removal is tried again")

	je.clearer.mu.Lock()
	je.clearer.err = nil
	je.clearer.mu.Unlock()
	waitUntil(t, func() bool {
		je.svc.mu.Lock()
		defer je.svc.mu.Unlock()
		return len(je.svc.missed) == 0
	}, "the removal goes through")
}

// The same for asking again: the person must not lose the one time to a service that is not
// up yet.
func TestAskingAgainIsRetriedWhileTheNotifyServiceIsNotThereYet(t *testing.T) {
	je := newJournalEnv(t)
	je.svc.cfg.ReconnectRetry = 5 * time.Millisecond
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	for range 2 {
		je.notifier.next(t)
	}
	je.delivered(t, 3)
	je.notifier.setFail(s.service, true)

	je.svc.Reconnected()
	for range 2 {
		if got := je.notifier.next(t); got.service == s.service {
			t.Fatalf("%s should have failed", s.service)
		}
	}
	je.notifier.setFail(s.service, false)
	again := je.notifier.next(t)
	if again.service != s.service || !strings.HasSuffix(again.n.Message, "\n"+s.n.Message) {
		t.Errorf("asked again = %+v", again)
	}
	je.svc.HandleEvent(event("HM_APPROVE_"+nonceOf(t, again), u1))
	if a := wait(t, ch); a.res.Outcome != OutcomeApproved {
		t.Errorf("answer = %s", a.res.Outcome)
	}
}

// A request that ended while the retries run is not asked again.
func TestRetryDoesNotAskForAnEndedRequest(t *testing.T) {
	je := newJournalEnv(t)
	je.svc.cfg.ReconnectRetry = 20 * time.Millisecond
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	for range 2 {
		je.notifier.next(t)
	}
	je.delivered(t, 3)
	je.notifier.setFail(s.service, true)
	je.svc.Reconnected()
	for range 2 {
		je.notifier.next(t)
	}
	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u1))
	wait(t, ch)
	je.svc.background.Wait()
	je.notifier.setFail(s.service, false)
	before := je.notifier.count()
	time.Sleep(100 * time.Millisecond)
	if je.notifier.count() != before {
		t.Error("an ended request asked again")
	}
}

// delivered waits until the open requests have recorded n devices: a device gets its
// notification before that.
func (je *journalEnv) delivered(t *testing.T, n int) {
	t.Helper()
	waitUntil(t, func() bool {
		je.svc.mu.Lock()
		defer je.svc.mu.Unlock()
		got := 0
		for _, p := range je.svc.pending {
			got += len(p.delivered)
		}
		return got == n
	}, "the delivery is recorded")
}

func waitUntil(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
