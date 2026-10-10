// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

var update = flag.Bool("update", false, "rewrite the reference notifications in testdata")

const (
	u1 = "1a2b3c4d5e6f708192a3b4c5d6e7f801" // approver, German
	u2 = "2b3c4d5e6f708192a3b4c5d6e7f80112" // approver, household language
	u3 = "3c4d5e6f708192a3b4c5d6e7f8011223" // configured, but not an approver of the mandate
)

type sent struct {
	service string
	n       ha.Notification
}

// fakeNotifier records notifications; services in fail refuse them.
type fakeNotifier struct {
	mu   sync.Mutex
	sent []sent
	fail map[string]bool
	ch   chan sent
	// before runs before each delivery, outside the lock (e.g. to cancel meanwhile).
	before func(service string)
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{fail: map[string]bool{}, ch: make(chan sent, 32)}
}

func (f *fakeNotifier) Notify(_ context.Context, service string, n ha.Notification) error {
	if f.before != nil {
		f.before(service)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[service] {
		return errors.New("notify failed")
	}
	f.sent = append(f.sent, sent{service, n})
	f.ch <- sent{service, n}
	return nil
}

func (f *fakeNotifier) setFail(service string, fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[service] = fail
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeNotifier) next(t *testing.T) sent {
	t.Helper()
	select {
	case s := <-f.ch:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
		return sent{}
	}
}

type env struct {
	svc       *Service
	notifier  *fakeNotifier
	approvers *Approvers
}

func newEnv(t *testing.T, maxTimeout time.Duration) env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	approvers := NewApprovers(st.DB(), audit.New(st.DB(), "household:hm-0123456789ab"))
	for _, a := range []Approver{{UserID: u1, Devices: phones("mobile_app_markus"), Language: "de"},
		{UserID: u2, Devices: phones("mobile_app_anna")}, {UserID: u3, Devices: phones("mobile_app_guest")}} {
		if err := approvers.Put(context.Background(), a, changer); err != nil {
			t.Fatal(err)
		}
	}
	n := newFakeNotifier()
	svc := New(Config{Approvers: approvers, Notifier: n, Language: func() i18n.Lang { return i18n.EN }, MaxTimeout: maxTimeout})
	return env{svc: svc, notifier: n, approvers: approvers}
}

// phones are devices that may also answer critical requests (default of a phone).
func phones(services ...string) []Device {
	out := make([]Device, len(services))
	for i, s := range services {
		out[i] = Device{Service: s, Critical: true}
	}
	return out
}

func request() Request {
	return Request{ClientID: "hm-client:voice", Agent: "Voice assistant", Device: "Front door", Action: "unlock", Reason: "The parcel service is at the door",
		Approvers: []string{u1, u2, "not-configured"}, Timeout: time.Minute}
}

type answer struct {
	res Result
	err error
}

func (e env) ask(req Request) chan answer {
	ch := make(chan answer, 1)
	go func() {
		res, err := e.svc.Ask(context.Background(), req)
		ch <- answer{res, err}
	}()
	return ch
}

func wait(t *testing.T, ch chan answer) answer {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("Ask did not return")
		return answer{}
	}
}

// nonceOf returns the nonce of a notification's approve button.
func nonceOf(t *testing.T, s sent) string {
	t.Helper()
	if len(s.n.Actions) != 2 || !strings.HasPrefix(s.n.Actions[0].Action, "HM_APPROVE_") {
		t.Fatalf("actions = %+v", s.n.Actions)
	}
	return strings.TrimPrefix(s.n.Actions[0].Action, "HM_APPROVE_")
}

func event(action, userID string) ha.Event {
	data, _ := json.Marshal(map[string]any{"action": action})
	return ha.Event{EventType: ha.EventMobileAppNotificationAction, Data: data, Context: ha.EventContext{UserID: userID}}
}

// E2E scenario 2 at unit level: an approver confirms.
func TestApprovedByAnApprover(t *testing.T) {
	e := newEnv(t, time.Minute)
	ch := e.ask(request())
	first, second := e.notifier.next(t), e.notifier.next(t)
	byService := map[string]sent{first.service: first, second.service: second}
	de, en := byService["mobile_app_markus"], byService["mobile_app_anna"]
	if de.n.Title != "Freigabe nötig: Voice assistant" || en.n.Title != "Approval needed: Voice assistant" {
		t.Errorf("titles %q / %q", de.n.Title, en.n.Title)
	}
	if !strings.Contains(de.n.Message, "Angabe des Agenten, nicht geprüft: The parcel service is at the door") ||
		!strings.Contains(de.n.Message, "entriegeln") {
		t.Errorf("message = %q", de.n.Message)
	}
	nonce := nonceOf(t, de)
	if len(nonce) != 32 || nonceOf(t, en) != nonce || strings.Trim(nonce, "0123456789abcdef") != "" {
		t.Errorf("nonce %q", nonce)
	}
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u2))
	a := wait(t, ch)
	if a.err != nil || a.res.Outcome != OutcomeApproved || a.res.By != u2 || a.res.At.IsZero() {
		t.Errorf("result = %+v, %v", a.res, a.err)
	}
	// Nobody else was asked: u3 is configured but not an approver of this mandate.
	if n := e.notifier.count(); n != 2 {
		t.Errorf("%d notifications", n)
	}
}

func TestRejectedByAnApprover(t *testing.T) {
	e := newEnv(t, time.Minute)
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.HandleEvent(event("HM_DENY_"+nonce, u1))
	if a := wait(t, ch); a.res.Outcome != OutcomeRejected || a.res.By != u1 {
		t.Errorf("result = %+v", a.res)
	}
}

// E2E scenario 3 at unit level: no answer → timeout; a late answer is discarded.
func TestTimeout(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	start := time.Now()
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	a := wait(t, ch)
	if a.err != nil || a.res.Outcome != OutcomeTimeout || a.res.By != "" || time.Since(start) < 50*time.Millisecond {
		t.Errorf("result = %+v, %v", a.res, a.err)
	}
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1)) // must not panic or block
}

// The mandate's timeout applies when it is shorter than the upper limit.
func TestMandateTimeoutShortensTheWait(t *testing.T) {
	e := newEnv(t, time.Hour)
	req := request()
	req.Timeout = 30 * time.Millisecond
	if a := wait(t, e.ask(req)); a.res.Outcome != OutcomeTimeout {
		t.Errorf("result = %+v", a.res)
	}
}

// E2E scenario 4 at unit level (decision W8): an answer from someone who may not
// approve ends the request as invalid_response and warns the approvers.
func TestAnswerFromANonApprover(t *testing.T) {
	e := newEnv(t, time.Minute)
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.notifier.next(t)
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u3))
	a := wait(t, ch)
	if a.res.Outcome != OutcomeInvalidResponse || a.res.By != u3 {
		t.Errorf("result = %+v", a.res)
	}
	warnings := map[string]sent{}
	for range 2 {
		s := e.notifier.next(t)
		warnings[s.service] = s
	}
	de, en := warnings["mobile_app_markus"], warnings["mobile_app_anna"]
	if de.n.Title != "Warnung: unberechtigte Antwort" || !strings.Contains(de.n.Message, u3) || len(de.n.Actions) != 0 ||
		en.n.Title != "Warning: unauthorised answer" || !strings.Contains(en.n.Message, "Front door") {
		t.Errorf("warnings = %+v", warnings)
	}
	if _, ok := warnings["mobile_app_guest"]; ok {
		t.Error("the non-approver was warned")
	}
	// A valid answer afterwards changes nothing.
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
}

func TestAnswerWithoutUserIsInvalid(t *testing.T) {
	e := newEnv(t, time.Minute)
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, ""))
	if a := wait(t, ch); a.res.Outcome != OutcomeInvalidResponse || a.res.By != "unknown" {
		t.Errorf("result = %+v", a.res)
	}
}

// Negative catalog: "yes" and "no" at the same time → the first valid answer counts.
func TestFirstAnswerCounts(t *testing.T) {
	e := newEnv(t, time.Minute)
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
	e.svc.HandleEvent(event("HM_DENY_"+nonce, u2))
	if a := wait(t, ch); a.res.Outcome != OutcomeApproved || a.res.By != u1 {
		t.Errorf("result = %+v", a.res)
	}
}

// Negative catalog: answer with an unknown, expired or used nonce → discarded.
func TestForeignAndMalformedAnswersAreIgnored(t *testing.T) {
	e := newEnv(t, 200*time.Millisecond)
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	for _, ev := range []ha.Event{
		event("HM_APPROVE_00000000000000000000000000000000", u1), // unknown nonce
		event("HM_APPROVE_"+strings.ToUpper(nonce), u1),          // not the canonical form
		event("HM_APPROVE_"+nonce[:31], u1),                      // truncated
		event("HM_APPROVE_"+nonce+"x", u1),                       // extended
		event("OPEN_GARAGE", u1),                                 // another integration's action
		event("", u1),
		{EventType: ha.EventMobileAppNotificationAction, Data: json.RawMessage(`[1]`), Context: ha.EventContext{UserID: u1}},
	} {
		e.svc.HandleEvent(ev)
	}
	if a := wait(t, ch); a.res.Outcome != OutcomeTimeout {
		t.Errorf("result = %+v", a.res)
	}
}

func TestNoApproverCanBeReached(t *testing.T) {
	e := newEnv(t, time.Minute)
	req := request()
	req.Approvers = []string{"not-configured"}
	if _, err := e.svc.Ask(context.Background(), req); !errors.Is(err, ErrNoApprover) {
		t.Errorf("no configured approver: %v", err)
	}
	e.notifier.setFail("mobile_app_markus", true)
	e.notifier.setFail("mobile_app_anna", true)
	if _, err := e.svc.Ask(context.Background(), request()); !errors.Is(err, ErrNoApprover) {
		t.Errorf("no notification delivered: %v", err)
	}
	// One delivered notification is enough.
	e.notifier.setFail("mobile_app_anna", false)
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u2))
	if a := wait(t, ch); a.res.Outcome != OutcomeApproved {
		t.Errorf("result = %+v", a.res)
	}
}

// Issue #27: a request does not end with the call that made it; an answer after the
// caller gave up still counts, and the timeout ends it as before.
func TestTheRequestOutlivesTheCall(t *testing.T) {
	e := newEnv(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	p, err := e.svc.Start(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	nonce := nonceOf(t, e.notifier.next(t))
	cancel()
	select {
	case res := <-p.Done:
		t.Fatalf("ended with the call: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	if open := e.svc.Open(); len(open) != 1 || open[0].ID != p.ID || !p.Expires.Equal(open[0].ExpiresAt) {
		t.Fatalf("open = %+v, pending %+v", open, p)
	}
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
	if res := <-p.Done; res.Outcome != OutcomeApproved || res.ID != p.ID {
		t.Errorf("result = %+v", res)
	}

	req := request()
	req.Timeout = 100 * time.Millisecond
	p, err = e.svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res := <-p.Done; res.Outcome != OutcomeTimeout {
		t.Errorf("timeout = %+v", res)
	}
}

// SPEC-v0 section 11.1 item 8: the agent may withdraw its own open request; that is no
// answer, and an answer given before stands.
func TestCancelRequest(t *testing.T) {
	e := newEnv(t, time.Minute)
	p, err := e.svc.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	nonce := nonceOf(t, e.notifier.next(t))
	_ = e.notifier.next(t) // the second approver's
	if e.svc.CancelRequest("0123") || e.svc.CancelRequest("") {
		t.Error("unknown request cancelled")
	}
	if !e.svc.CancelRequest(p.ID) {
		t.Fatal("not cancelled")
	}
	if res := <-p.Done; res.Outcome != OutcomeCancelled || res.Cause != audit.CauseWithdrawn || res.By != "" {
		t.Errorf("result = %+v", res)
	}
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1)) // too late: discarded

	p, _ = e.svc.Start(context.Background(), request())
	nonce = nonceOf(t, e.notifier.next(t))
	_ = e.notifier.next(t)
	e.svc.HandleEvent(event("HM_DENY_"+nonce, u1))
	if res := <-p.Done; res.Outcome != OutcomeRejected {
		t.Errorf("answered = %+v", res)
	}
	if e.svc.CancelRequest(p.ID) {
		t.Error("an answered request was withdrawn")
	}
}

// TESTING section 5: approval notifications in both languages against stored references.
func TestReferenceNotifications(t *testing.T) {
	req := Request{ClientID: "https://agent.example.org/voice", EntityID: "lock.front_door", Agent: "Voice assistant", Device: "Front door", Action: "unlock",
		Reason: "**URGENT** open now: https://evil.example.org/x\u202e", Approvers: []string{u1}}
	for _, lang := range i18n.Supported {
		got, _ := json.MarshalIndent(buildRequest(lang, req, "00112233445566778899aabbccddeeff"), "", "  ")
		path := filepath.Join("testdata", "notification_"+string(lang)+".json")
		if *update {
			if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(want)) != string(got) {
			t.Errorf("%s:\n%s\nwant\n%s", lang, got, want)
		}
	}
}

// Negative catalog: very long or manipulated reason (control characters, Markdown,
// links) → truncated, sanitized, marked as the agent's claim.
func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":                     "plain text",
		"  spaced \t\n out  ":            "spaced out",
		"bidi\u202eoverride":             "bidi override",
		"zero\u200bwidth":                "zero width",
		"line\u2028sep":                  "line sep",
		"**bold** _it_ `code` [x](y)":    "bold _it_ code x y",
		"see https://evil.example.org/a": "see https evil.example.org/a",
		"<script>alert(1)</script>":      "script alert 1 /script",
		"{device}":                       "device",
		"":                               "",
		strings.Repeat("a", 250):         strings.Repeat("a", maxReason-1) + "…",
	} {
		if got := sanitize(in, maxReason); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	req := request()
	req.Reason = "\u202e\u200b  "
	if n := buildRequest(i18n.EN, req, "00112233445566778899aabbccddeeff"); strings.Contains(n.Message, "claim") {
		t.Errorf("empty reason shown: %q", n.Message)
	}
}

// Every approver is asked once, even if the mandate names them twice; Home-Mandate's own
// Home Assistant user is never asked, so its token cannot approve.
func TestRecipients(t *testing.T) {
	e := newEnv(t, time.Minute)
	e.svc.cfg.ServiceUser = func() string { return u2 }
	req := request()
	req.Approvers = []string{u1, u1, u2}
	ch := e.ask(req)
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u2))
	if a := wait(t, ch); a.res.Outcome != OutcomeInvalidResponse {
		t.Errorf("answer of the service user = %+v", a.res)
	}
	if n := countRequests(e.notifier, "mobile_app_anna"); n != 0 {
		t.Error("the service user was asked")
	}
	if n := countRequests(e.notifier, ""); n != 1 {
		t.Errorf("%d requests sent, want 1", n)
	}
	req.Approvers = []string{u2}
	if _, err := e.svc.Ask(context.Background(), req); !errors.Is(err, ErrNoApprover) {
		t.Errorf("only the service user: %v", err)
	}
}

// countRequests counts the approval requests (with buttons) sent, to service if set.
func countRequests(f *fakeNotifier, service string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if len(s.n.Actions) > 0 && (service == "" || s.service == service) {
			n++
		}
	}
	return n
}

// The human sees the service data that will be sent, not only the action.
func TestParametersAreShown(t *testing.T) {
	req := request()
	req.Params = map[string]any{"temperature": 21.5, "hvac_mode": "heat", "note": "**x** https://evil.example.org"}
	msg := buildRequest(i18n.EN, req, "00112233445566778899aabbccddeeff").Message
	if !strings.Contains(msg, "Parameters: hvac_mode=heat, note=x https evil.example.org, temperature=21.5") {
		t.Errorf("message = %q", msg)
	}
	if strings.Contains(buildRequest(i18n.EN, request(), "00112233445566778899aabbccddeeff").Message, "Parameters") {
		t.Error("empty parameters shown")
	}
}

func TestDefaultUpperLimit(t *testing.T) {
	if s := New(Config{}); s.cfg.MaxTimeout != defaultMaxTimeout || s.cfg.Logger == nil {
		t.Errorf("config = %+v", s.cfg)
	}
}

// Negative catalog: a second answer and an answer after the timeout are discarded and
// logged (without the nonce).
func TestDiscardedAnswersAreLogged(t *testing.T) {
	var buf safeBuffer
	e := newEnv(t, 100*time.Millisecond)
	e.svc.cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	ch := e.ask(request())
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
	e.svc.HandleEvent(event("HM_DENY_"+nonce, u2))
	wait(t, ch)
	ch = e.ask(request())
	late := nonceOf(t, e.notifier.next(t))
	e.notifier.next(t)
	wait(t, ch)
	e.svc.HandleEvent(event("HM_APPROVE_"+late, u1))
	out := buf.String()
	if strings.Count(out, "approval answer discarded") != 2 || !strings.Contains(out, u2) || strings.Contains(out, nonce) || strings.Contains(out, late) {
		t.Errorf("log:\n%s", out)
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// OnOpened announces a request once it was delivered, with what Open shows; the result
// names the request it ended.
func TestOnOpenedAndResultID(t *testing.T) {
	e := newEnv(t, time.Minute)
	opened := make(chan Open, 1)
	e.svc.cfg.OnOpened = func(o Open) { opened <- o }
	req := request()
	req.EntityID, req.Area = "lock.front_door", "hall"
	ch := e.ask(req)
	var o Open
	select {
	case o = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("OnOpened not called")
	}
	if o.ID == "" || o.Request.EntityID != "lock.front_door" || o.Request.Area != "hall" || len(o.Recipients) != 2 {
		t.Errorf("opened = %+v", o)
	}
	nonce := nonceOf(t, e.notifier.next(t))
	e.notifier.next(t)
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
	a := wait(t, ch)
	if a.err != nil || a.res.ID != o.ID || a.res.Outcome != OutcomeApproved {
		t.Errorf("result = %+v, %v", a.res, a.err)
	}
}

// The UI shows the service data and the reason exactly as the push does (S1).
func TestShownParamsAndText(t *testing.T) {
	got := ShownParams(map[string]any{"temperature": 21.5, "brightness_pct": 100, "x<script>": "[link](https://evil)"})
	if len(got) != 3 || got[0] != (Param{"brightness_pct", "100"}) || got[1] != (Param{"temperature", "21.5"}) ||
		got[2].Name != "x script" || strings.Contains(got[2].Value, "://") || strings.ContainsAny(got[2].Value, "[]()") {
		t.Errorf("ShownParams = %+v", got)
	}
	long := strings.Repeat("a", 300)
	if p := ShownParams(map[string]any{long: long}); len([]rune(p[0].Name)) != ShownNameMax || len([]rune(p[0].Value)) != ShownNameMax {
		t.Errorf("long param = %+v", p)
	}
	if len(ShownParams(nil)) != 0 {
		t.Error("ShownParams(nil) not empty")
	}
	if got := ShownText("a\u202eb\nc", ShownReasonMax); got != "a b c" {
		t.Errorf("ShownText = %q", got)
	}
}

// On shutdown the door closes before anything else (SPEC-v0 section 11.1 items 8 and 9):
// an answer after StopAnswers is discarded and logged like one to a request that is no
// longer open, on the phone and in the UI; the request stays open, so the next start
// records it as interrupted, which it then truly was.
func TestNoAnswerIsTakenAfterStopAnswers(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	var buf safeBuffer
	e.svc.cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	e.put(t, Approver{UserID: u2, Devices: phones("mobile_app_anna"), UI: true})
	e.admins.put(u2, true)
	p, err := e.svc.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	id := e.waitOpen(t, 1)[0].ID
	nonce := nonceOf(t, e.notifier.next(t))
	e.svc.StopAnswers()
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
	if !strings.Contains(buf.String(), "approval answer discarded: unknown, expired or already answered") {
		t.Errorf("log = %s", buf.String())
	}
	if _, err := e.svc.Answer(context.Background(), id, u2, true); !errors.Is(err, ErrNotPending) {
		t.Errorf("UI answer after the stop: %v", err)
	}
	if !e.svc.IsOpen(id) || e.svc.IsOpen("0123") {
		t.Error("IsOpen")
	}
	select {
	case res := <-p.Done:
		t.Fatalf("ended by a discarded answer: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
}
