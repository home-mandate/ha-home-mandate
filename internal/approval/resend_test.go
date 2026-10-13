// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

const tapURL = "https://hm.example.org/ui/#/audit/requests"

func clearedEvent(tag, userID string) ha.Event {
	data, _ := json.Marshal(map[string]any{"tag": tag, "message": "anything"})
	return ha.Event{EventType: ha.EventMobileAppNotificationCleared, Data: data, Context: ha.EventContext{UserID: userID}}
}

// quiet fails if a notification arrives within d.
func (f *fakeNotifier) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case s := <-f.ch:
		t.Fatalf("unexpected notification to %s", s.service)
	case <-time.After(d):
	}
}

// Issue #49: a tap opens where the request can be answered, and on Android it does not
// dismiss the notification; the link never carries the nonce.
func TestNotificationTapTargetAndSticky(t *testing.T) {
	for _, tt := range []struct {
		name, target string
	}{{"with a target", tapURL}, {"without", ""}} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, time.Minute)
			e.svc.cfg.TapTarget = func() string { return tt.target }
			e.ask(request())
			s := e.notifier.next(t)
			if s.n.URL != tt.target || !s.n.Sticky {
				t.Errorf("url %q, sticky %v", s.n.URL, s.n.Sticky)
			}
			if nonce := nonceOf(t, s); strings.Contains(s.n.URL, nonce) {
				t.Error("the link carries the nonce")
			}
			if !strings.Contains(s.n.Message, "Expand the notification") && !strings.Contains(s.n.Message, "Benachrichtigung aufziehen") {
				t.Errorf("no hint how to answer: %q", s.n.Message)
			}
		})
	}
}

// Swiped away or tapped by mistake while the request is open: sent again once per person,
// to the devices that had it, under the same tag and with the same buttons.
func TestClearedNotificationIsSentAgainOncePerPerson(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.ResendDelay = 10 * time.Millisecond
	ch := e.ask(request())
	first, second := e.notifier.next(t), e.notifier.next(t)
	byService := map[string]sent{first.service: first, second.service: second}
	markus := byService["mobile_app_markus"]

	e.svc.HandleCleared(clearedEvent(markus.n.Tag, u1))
	again := e.notifier.next(t)
	if again.service != "mobile_app_markus" || again.n.Tag != markus.n.Tag || nonceOf(t, again) != nonceOf(t, markus) || again.n.Message != markus.n.Message {
		t.Errorf("sent again = %+v", again)
	}
	e.svc.HandleCleared(clearedEvent(markus.n.Tag, u1)) // a second time: not again
	e.notifier.quiet(t, 100*time.Millisecond)

	e.svc.HandleCleared(clearedEvent(markus.n.Tag, u2))
	if anna := e.notifier.next(t); anna.service != "mobile_app_anna" {
		t.Errorf("sent again to %s", anna.service)
	}
	e.svc.HandleEvent(event("HM_APPROVE_"+nonceOf(t, markus), u1))
	if a := wait(t, ch); a.res.Outcome != OutcomeApproved {
		t.Errorf("outcome = %v", a.res.Outcome)
	}
}

// Never after the request ended, also when it ends during the wait (the button press
// itself clears the notification); never for foreign tags or people who did not get it.
func TestClearedNotificationIsNotSentAgain(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.ResendDelay = 50 * time.Millisecond
	ch := e.ask(request())
	first, _ := e.notifier.next(t), e.notifier.next(t)
	tag := first.n.Tag

	for _, ev := range []ha.Event{
		clearedEvent("hm_request_unknown", u1),
		clearedEvent("other_tag", u1),
		clearedEvent(tag, u3), // an approver who was not asked
		clearedEvent(tag, ""),
		{EventType: ha.EventMobileAppNotificationCleared, Data: []byte(`{"tag":`)},
	} {
		e.svc.HandleCleared(ev)
	}
	e.notifier.quiet(t, 150*time.Millisecond)

	e.svc.HandleCleared(clearedEvent(tag, u1))
	e.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, first), u1)) // answered during the wait
	wait(t, ch)
	e.notifier.quiet(t, 150*time.Millisecond)
	e.svc.HandleCleared(clearedEvent(tag, u2)) // after the end
	e.notifier.quiet(t, 150*time.Millisecond)
}

// A tap target Home Assistant would refuse (an http public URL) is left out; the request
// still reaches everyone.
func TestInvalidTapTargetNeverStopsTheRequest(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.TapTarget = func() string { return "http://localhost:8765/ui/#/audit/requests" }
	e.ask(request())
	for range 2 {
		if s := e.notifier.next(t); s.n.URL != "" || len(s.n.Actions) != 2 {
			t.Errorf("sent to %s: url %q", s.service, s.n.URL)
		}
	}
}

// A report while the request is still being delivered does not use up the one time.
func TestClearedDuringTheDeliveryIsNotCounted(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.ResendDelay = 10 * time.Millisecond
	tag := func() string {
		e.svc.mu.Lock()
		defer e.svc.mu.Unlock()
		for _, p := range e.svc.pending {
			return p.tag
		}
		return ""
	}
	var once sync.Once
	e.notifier.before = func(service string) {
		if service == "mobile_app_markus" {
			once.Do(func() { e.svc.HandleCleared(clearedEvent(tag(), u1)) })
		}
	}
	e.ask(request())
	first, _ := e.notifier.next(t), e.notifier.next(t)
	e.notifier.quiet(t, 100*time.Millisecond) // the early report sent nothing
	e.svc.HandleCleared(clearedEvent(first.n.Tag, u1))
	if again := e.notifier.next(t); again.service != "mobile_app_markus" {
		t.Errorf("sent again to %s", again.service)
	}
}

// The request ends while it is sent again: what was sent is removed again.
func TestResendRemovedWhenTheRequestEndsMeanwhile(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.ResendDelay = 10 * time.Millisecond
	clearer := &fakeClearer{}
	e.svc.cfg.Clearer = clearer
	ch := e.ask(request())
	first, second := e.notifier.next(t), e.notifier.next(t)
	markus := first
	if second.service == "mobile_app_markus" {
		markus = second
	}
	var once sync.Once
	e.notifier.before = func(service string) {
		once.Do(func() { e.svc.HandleEvent(event("HM_APPROVE_"+nonceOf(t, markus), u1)) })
	}
	e.svc.HandleCleared(clearedEvent(markus.n.Tag, u1))
	wait(t, ch)
	waitFor(t, func() bool {
		n := 0
		for _, c := range clearer.list() {
			if c.service == "mobile_app_markus" && c.tag == markus.n.Tag {
				n++
			}
		}
		return n >= 2 // by the end of the request, and after the late resend
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Issue #46: a read is asked for in words of its own, without the state.
func TestReadRequestText(t *testing.T) {
	req := request()
	req.Action, req.State = "read", "locked"
	for lang, want := range map[i18n.Lang]string{
		i18n.EN: "Voice assistant wants to read the state of “Front door”.",
		i18n.DE: "Voice assistant möchte den Zustand von „Front door“ lesen.",
	} {
		if n := buildRequest(lang, req, "00112233445566778899aabbccddeeff"); !strings.HasPrefix(n.Message, want+"\n") || strings.Contains(n.Message, "locked") {
			t.Errorf("%s: %q", lang, n.Message)
		}
	}
}
