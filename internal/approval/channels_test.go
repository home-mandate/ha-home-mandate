// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// Tests for decision F2 (answering on a phone or in the Home-Mandate UI) and F1
// (revocation and emergency stop end open requests). The combination tables are the
// ones in the plan; each row names one combination explicitly.

// fakeAdmins answers IsAdmin from a set; err makes every check fail.
type fakeAdmins struct {
	mu  sync.Mutex
	set map[string]bool
	err error
}

func (f *fakeAdmins) isAdmin(_ context.Context, user string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.set[user], f.err
}

func (f *fakeAdmins) put(user string, admin bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set[user] = admin
}

func (f *fakeAdmins) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

type bellCall struct {
	id string
	n  ha.Notification
}

// fakeBell records the persistent notifications (the bell in Home Assistant).
type fakeBell struct {
	mu      sync.Mutex
	rung    []bellCall
	cleared []string
	fail    bool
}

func (f *fakeBell) Ring(_ context.Context, id string, n ha.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("ring failed")
	}
	f.rung = append(f.rung, bellCall{id, n})
	return nil
}

func (f *fakeBell) Clear(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, id)
	if f.fail {
		return errors.New("clear failed")
	}
	return nil
}

func (f *fakeBell) state() ([]bellCall, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bellCall(nil), f.rung...), append([]string(nil), f.cleared...)
}

type channelEnv struct {
	env
	admins *fakeAdmins
	bell   *fakeBell
	bellOn bool
}

// newChannelEnv is newEnv with administrators and the bell; u1 is an administrator.
func newChannelEnv(t *testing.T, maxTimeout time.Duration) *channelEnv {
	t.Helper()
	e := &channelEnv{env: newEnv(t, maxTimeout), admins: &fakeAdmins{set: map[string]bool{u1: true}}, bell: &fakeBell{}}
	e.svc.cfg.IsAdmin = e.admins.isAdmin
	e.svc.cfg.Bell = e.bell
	e.svc.cfg.BellEnabled = func() bool { return e.bellOn }
	return e
}

func (e *channelEnv) put(t *testing.T, ap Approver) {
	t.Helper()
	if err := e.approvers.Put(context.Background(), ap); err != nil {
		t.Fatal(err)
	}
}

// waitOpen waits until n requests are open and returns them.
func (e *channelEnv) waitOpen(t *testing.T, n int) []Open {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if open := e.svc.Open(); len(open) == n {
			return open
		}
		if time.Now().After(deadline) {
			t.Fatalf("open = %+v, want %d", e.svc.Open(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Combination table "answer in the UI" (F2): approver of the request × UI configuration ×
// administrator now × critical action, each with approve and reject. A refused answer
// leaves the request open.
func TestAnswerInTheUICombinations(t *testing.T) {
	uiConfigs := map[string]Approver{
		"no UI":   {Devices: []string{"mobile_app_x"}},
		"UI":      {Devices: []string{"mobile_app_x"}, UI: true},
		"UI+crit": {Devices: []string{"mobile_app_x"}, UI: true, UICritical: true},
	}
	cases := []struct {
		member          bool
		config          string
		admin, critical bool
		want            error
	}{
		{false, "no UI", false, false, ErrNotApprover},
		{false, "no UI", false, true, ErrNotApprover},
		{false, "no UI", true, false, ErrNotApprover},
		{false, "no UI", true, true, ErrNotApprover},
		{false, "UI", false, false, ErrNotApprover},
		{false, "UI", false, true, ErrNotApprover},
		{false, "UI", true, false, ErrNotApprover},
		{false, "UI", true, true, ErrNotApprover},
		{false, "UI+crit", false, false, ErrNotApprover},
		{false, "UI+crit", false, true, ErrNotApprover},
		{false, "UI+crit", true, false, ErrNotApprover},
		{false, "UI+crit", true, true, ErrNotApprover},

		{true, "no UI", false, false, ErrChannel},
		{true, "no UI", false, true, ErrChannel},
		{true, "no UI", true, false, ErrChannel},
		{true, "no UI", true, true, ErrChannel},
		{true, "UI", false, false, ErrChannel},
		{true, "UI", false, true, ErrChannel},
		{true, "UI", true, false, nil},
		{true, "UI", true, true, ErrChannel},
		{true, "UI+crit", false, false, ErrChannel},
		{true, "UI+crit", false, true, ErrChannel},
		{true, "UI+crit", true, false, nil},
		{true, "UI+crit", true, true, nil},
	}
	if len(cases) != 2*len(uiConfigs)*4 {
		t.Fatalf("%d cases, want every combination", len(cases))
	}
	for _, c := range cases {
		for _, approve := range []bool{true, false} {
			t.Run(strings.Join([]string{c.config, label("member", c.member), label("admin", c.admin),
				label("critical", c.critical), label("approve", approve)}, ","), func(t *testing.T) {
				e := newChannelEnv(t, time.Minute)
				user := u3 // configured, but not an approver of the request
				if c.member {
					user = u2
				}
				ap := uiConfigs[c.config]
				ap.UserID = user
				e.put(t, ap)
				e.admins.put(user, c.admin)
				req := request()
				req.Critical = c.critical
				ch := e.ask(req)
				open := e.waitOpen(t, 1)[0]

				res, err := e.svc.Answer(context.Background(), open.ID, user, approve)
				if c.want != nil {
					if !errors.Is(err, c.want) {
						t.Fatalf("Answer = %+v, %v, want %v", res, err, c.want)
					}
					if len(e.svc.Open()) != 1 {
						t.Fatal("a refused answer ended the request")
					}
					// The request goes on: the approver u1 can still answer on the phone.
					e.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, e.notifier.next(t)), u1))
					if a := wait(t, ch); a.res.Outcome != OutcomeRejected || a.res.By != u1 || a.res.Via != ViaPush {
						t.Errorf("after the refused answer: %+v", a.res)
					}
					return
				}
				want := OutcomeRejected
				if approve {
					want = OutcomeApproved
				}
				if err != nil || res.Outcome != want || res.By != user || res.Via != ViaUI || res.At.IsZero() {
					t.Fatalf("Answer = %+v, %v", res, err)
				}
				if a := wait(t, ch); a.err != nil || a.res != res {
					t.Errorf("Ask = %+v, %v, want %+v", a.res, a.err, res)
				}
				if len(e.svc.Open()) != 0 {
					t.Error("answered request still open")
				}
			})
		}
	}
}

func label(name string, on bool) string {
	if on {
		return name
	}
	return "not " + name
}

// Whatever changed since the request was sent counts at the moment of the answer:
// administrator rights, the person's settings, a failing check (fail-closed).
func TestUIAnswerChecksTheCurrentState(t *testing.T) {
	for name, change := range map[string]func(e *channelEnv, t *testing.T){
		"no longer administrator": func(e *channelEnv, _ *testing.T) { e.admins.put(u2, false) },
		"UI switched off": func(e *channelEnv, t *testing.T) {
			e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}})
		},
		"approver removed": func(e *channelEnv, t *testing.T) {
			if err := e.approvers.Remove(context.Background(), u2); err != nil {
				t.Fatal(err)
			}
		},
		"administrator check fails": func(e *channelEnv, _ *testing.T) { e.admins.fail(errors.New("home assistant unavailable")) },
		"approver list unreadable": func(e *channelEnv, _ *testing.T) {
			e.svc.cfg.Approvers = failingList{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newChannelEnv(t, time.Minute)
			e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
			e.admins.put(u2, true)
			ch := e.ask(request())
			open := e.waitOpen(t, 1)[0]
			change(e, t)
			if _, err := e.svc.Answer(context.Background(), open.ID, u2, true); !errors.Is(err, ErrChannel) {
				t.Errorf("Answer = %v, want ErrChannel", err)
			}
			if len(e.svc.Open()) != 1 {
				t.Error("a refused answer ended the request")
			}
			e.svc.CancelAll()
			if a := wait(t, ch); a.res.Outcome != OutcomeCancelled {
				t.Errorf("result = %+v", a.res)
			}
		})
	}
}

type failingList struct{}

func (failingList) List(context.Context) ([]Approver, error) {
	return nil, errors.New("database closed")
}

// Negative catalog: unknown, guessed or empty request IDs and users.
func TestUIAnswerRejectsUnknownRequestsAndUsers(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
	e.admins.put(u2, true)
	ch := e.ask(request())
	open := e.waitOpen(t, 1)[0]
	if len(open.ID) != 32 || strings.Trim(open.ID, "0123456789abcdef") != "" {
		t.Errorf("id %q is not 128 random bits in hex", open.ID)
	}
	nonce := nonceOf(t, e.notifier.next(t))
	if open.ID == nonce {
		t.Fatal("the request ID is the nonce")
	}
	for name, id := range map[string]string{"unknown": "00000000000000000000000000000000", "empty": "",
		"the nonce": nonce, "upper case": strings.ToUpper(open.ID), "truncated": open.ID[:31]} {
		if _, err := e.svc.Answer(context.Background(), id, u2, true); !errors.Is(err, ErrNotPending) {
			t.Errorf("%s ID: %v", name, err)
		}
	}
	for name, user := range map[string]string{"empty user": "", "not a user ID": "a b", "unknown": "ffffffffffffffffffffffffffffffff"} {
		if _, err := e.svc.Answer(context.Background(), open.ID, user, true); !errors.Is(err, ErrNotApprover) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Home-Mandate's own user never answers, in the UI either.
	e.svc.cfg.ServiceUser = func() string { return u2 }
	if _, err := e.svc.Answer(context.Background(), open.ID, u2, true); !errors.Is(err, ErrNotApprover) {
		t.Errorf("service user: %v", err)
	}
	e.svc.CancelAll()
	if a := wait(t, ch); a.res.Outcome != OutcomeCancelled {
		t.Errorf("result = %+v", a.res)
	}
}

// Combination table "state × second channel": once a request has ended, neither the
// phone nor the UI changes anything.
func TestSecondAnswerAfterTheEnd(t *testing.T) {
	ends := []struct {
		name string
		end  func(e *channelEnv, t *testing.T, id, nonce string)
		want Result
	}{
		{"approved on the phone", func(e *channelEnv, _ *testing.T, _, nonce string) {
			e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
		}, Result{Outcome: OutcomeApproved, By: u1, Via: ViaPush}},
		{"rejected in the UI", func(e *channelEnv, t *testing.T, id, _ string) {
			if _, err := e.svc.Answer(context.Background(), id, u2, false); err != nil {
				t.Fatal(err)
			}
		}, Result{Outcome: OutcomeRejected, By: u2, Via: ViaUI}},
		{"invalid answer on the phone", func(e *channelEnv, _ *testing.T, _, nonce string) {
			e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u3))
		}, Result{Outcome: OutcomeInvalidResponse, By: u3, Via: ViaPush}},
		{"timed out", func(*channelEnv, *testing.T, string, string) {}, Result{Outcome: OutcomeTimeout}},
		{"agent revoked", func(e *channelEnv, _ *testing.T, _, _ string) { e.svc.CancelAgent("hm-client:voice") },
			Result{Outcome: OutcomeCancelled}},
		{"emergency stop", func(e *channelEnv, _ *testing.T, _, _ string) { e.svc.CancelAll() }, Result{Outcome: OutcomeCancelled}},
	}
	for _, end := range ends {
		for _, second := range []string{"phone", "UI"} {
			t.Run(end.name+", then "+second, func(t *testing.T) {
				e := newChannelEnv(t, time.Minute)
				var buf safeBuffer
				e.svc.cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
				e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
				e.admins.put(u2, true)
				req := request()
				if end.want.Outcome == OutcomeTimeout {
					req.Timeout = 300 * time.Millisecond
				}
				ch := e.ask(req)
				id := e.waitOpen(t, 1)[0].ID
				nonce := nonceOf(t, e.notifier.next(t))
				end.end(e, t, id, nonce)
				a := wait(t, ch)
				a.res.At = time.Time{}
				if a.err != nil || a.res != end.want {
					t.Fatalf("result = %+v, %v, want %+v", a.res, a.err, end.want)
				}
				switch second {
				case "phone":
					e.svc.HandleEvent(event("HM_DENY_"+nonce, u1))
					if !strings.Contains(buf.String(), "approval answer discarded") {
						t.Errorf("late phone answer not logged:\n%s", buf.String())
					}
				case "UI":
					if _, err := e.svc.Answer(context.Background(), id, u2, true); !errors.Is(err, ErrNotPending) {
						t.Errorf("late UI answer: %v", err)
					}
				}
				if len(e.svc.Open()) != 0 {
					t.Error("ended request still open")
				}
			})
		}
	}
}

// Negative catalog: phone and UI answer at the same moment → exactly one counts, and
// the caller of Answer learns which.
func TestPhoneAndUIRace(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
	e.admins.put(u2, true)
	for range 50 {
		ch := e.ask(request())
		id := e.waitOpen(t, 1)[0].ID
		nonce := nonceOf(t, e.notifier.next(t))
		e.notifier.next(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var uiRes Result
		var uiErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1)) }()
		go func() { defer wg.Done(); <-start; uiRes, uiErr = e.svc.Answer(context.Background(), id, u2, false) }()
		close(start)
		wg.Wait()
		a := wait(t, ch)
		switch {
		case uiErr == nil:
			if a.res != uiRes || a.res.Outcome != OutcomeRejected || a.res.Via != ViaUI {
				t.Fatalf("UI won, but result = %+v (UI %+v)", a.res, uiRes)
			}
		case errors.Is(uiErr, ErrNotPending):
			if a.res.Outcome != OutcomeApproved || a.res.Via != ViaPush {
				t.Fatalf("phone won, but result = %+v", a.res)
			}
		default:
			t.Fatalf("Answer = %v", uiErr)
		}
	}
}

// Combination table "mixed recipients": u1 only on the phone, u2 only in the UI.
func TestMixedRecipients(t *testing.T) {
	cases := []struct {
		name            string
		critical        bool
		u2Admin         bool
		u1PushFails     bool
		wantErr         bool
		wantUIUsers     []string
		wantPushRequest bool
	}{
		{"ordinary, both reachable", false, true, false, false, []string{u2}, true},
		{"ordinary, phone fails, UI still there", false, true, true, false, []string{u2}, false},
		{"ordinary, u2 no administrator", false, false, false, false, nil, true},
		{"ordinary, u2 no administrator, phone fails", false, false, true, true, nil, false},
		{"critical, u2 not for critical", true, true, false, false, nil, true},
		{"critical, u2 not for critical, phone fails", true, true, true, true, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newChannelEnv(t, time.Minute)
			e.put(t, Approver{UserID: u2, UI: true})
			e.admins.put(u2, c.u2Admin)
			e.notifier.setFail("mobile_app_markus", c.u1PushFails)
			req := request()
			req.Critical = c.critical
			if c.wantErr {
				if _, err := e.svc.Ask(context.Background(), req); !errors.Is(err, ErrNoApprover) {
					t.Fatalf("Ask = %v, want ErrNoApprover", err)
				}
				if len(e.svc.Open()) != 0 {
					t.Error("unreachable request left open")
				}
				return
			}
			ch := e.ask(req)
			open := e.waitOpen(t, 1)[0]
			if !equalStrings(open.UIUsers, c.wantUIUsers) || !equalStrings(open.Recipients, recipientsOf(c.wantUIUsers, !c.u1PushFails)) {
				t.Errorf("open = %+v", open)
			}
			if got := countRequests(e.notifier, "mobile_app_markus") == 1; got != c.wantPushRequest {
				t.Errorf("push to u1 = %v", got)
			}
			e.svc.CancelAll()
			if a := wait(t, ch); a.res.Outcome != OutcomeCancelled {
				t.Errorf("result = %+v", a.res)
			}
		})
	}
}

// recipientsOf lists who was reached: u1 by phone if delivered, then the UI users.
func recipientsOf(ui []string, phoneReached bool) []string {
	var out []string
	if phoneReached {
		out = append(out, u1)
	}
	return append(out, ui...)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Combination table "bell" (B2): switch × a recipient with the UI channel. The bell is
// neutral (nothing from the agent, no device, no nonce) and is cleared however the
// request ends.
func TestBellCombinations(t *testing.T) {
	cases := []struct {
		name     string
		bellOn   bool
		uiActive bool
		want     bool
	}{
		{"on, UI recipient", true, true, true},
		{"on, no UI recipient", true, false, false},
		{"off, UI recipient", false, true, false},
		{"off, no UI recipient", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newChannelEnv(t, time.Minute)
			e.bellOn = c.bellOn
			e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
			e.admins.put(u2, c.uiActive)
			ch := e.ask(request())
			id := e.waitOpen(t, 1)[0].ID
			nonce := nonceOf(t, e.notifier.next(t))
			e.svc.CancelAll()
			wait(t, ch)
			rung, cleared := e.bell.state()
			if (len(rung) == 1) != c.want || (len(cleared) == 1) != c.want {
				t.Fatalf("rung %+v, cleared %v", rung, cleared)
			}
			if !c.want {
				return
			}
			// Every HA user sees the bell: its ID is neither the request ID nor the nonce.
			if !strings.HasPrefix(rung[0].id, "hm_approval_") || strings.Contains(rung[0].id, id) || strings.Contains(rung[0].id, nonce) ||
				cleared[0] != rung[0].id {
				t.Errorf("bell id %q / %q (request %q)", rung[0].id, cleared[0], id)
			}
			text := rung[0].n.Title + " " + rung[0].n.Message
			for _, secret := range []string{"Voice assistant", "Front door", "parcel", "unlock", nonce, "http", "[", "]("} {
				if strings.Contains(text, secret) {
					t.Errorf("bell text contains %q: %q", secret, text)
				}
			}
			if len(rung[0].n.Actions) != 0 || rung[0].n.Message == "" {
				t.Errorf("bell = %+v", rung[0].n)
			}
		})
	}
}

// However a request ends, the bell is cleared exactly once.
func TestBellIsClearedAtEveryEnd(t *testing.T) {
	for name, end := range map[string]func(e *channelEnv, t *testing.T, id, nonce string, cancel context.CancelFunc){
		"phone answer": func(e *channelEnv, _ *testing.T, _, nonce string, _ context.CancelFunc) {
			e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1))
		},
		"UI answer": func(e *channelEnv, t *testing.T, id, _ string, _ context.CancelFunc) {
			if _, err := e.svc.Answer(context.Background(), id, u2, true); err != nil {
				t.Fatal(err)
			}
		},
		"timeout": func(*channelEnv, *testing.T, string, string, context.CancelFunc) {},
		"agent revoked": func(e *channelEnv, _ *testing.T, _, _ string, _ context.CancelFunc) {
			e.svc.CancelAgent("hm-client:voice")
		},
		"emergency stop":   func(e *channelEnv, _ *testing.T, _, _ string, _ context.CancelFunc) { e.svc.CancelAll() },
		"caller went away": func(_ *channelEnv, _ *testing.T, _, _ string, cancel context.CancelFunc) { cancel() },
	} {
		t.Run(name, func(t *testing.T) {
			e := newChannelEnv(t, time.Minute)
			e.bellOn = true
			e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
			e.admins.put(u2, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch := make(chan answer, 1)
			req := request()
			if name == "timeout" {
				req.Timeout = 300 * time.Millisecond
			}
			go func() {
				res, err := e.svc.Ask(ctx, req)
				ch <- answer{res, err}
			}()
			id := e.waitOpen(t, 1)[0].ID
			nonce := nonceOf(t, e.notifier.next(t))
			end(e, t, id, nonce, cancel)
			wait(t, ch)
			if rung, cleared := e.bell.state(); len(rung) != 1 || len(cleared) != 1 || cleared[0] != rung[0].id {
				t.Errorf("rung %+v, cleared = %v", rung, cleared)
			}
		})
	}
}

// A bell that fails or is missing never stops the request.
func TestBellFailures(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.bellOn = true
	e.bell.fail = true
	e.put(t, Approver{UserID: u2, UI: true})
	e.admins.put(u2, true)
	ch := e.ask(request())
	id := e.waitOpen(t, 1)[0].ID
	if _, err := e.svc.Answer(context.Background(), id, u2, true); err != nil {
		t.Fatal(err)
	}
	if a := wait(t, ch); a.res.Outcome != OutcomeApproved {
		t.Errorf("result = %+v", a.res)
	}
	e.svc.cfg.Bell = nil
	ch = e.ask(request())
	id = e.waitOpen(t, 1)[0].ID
	if _, err := e.svc.Answer(context.Background(), id, u2, false); err != nil {
		t.Fatal(err)
	}
	if a := wait(t, ch); a.res.Outcome != OutcomeRejected {
		t.Errorf("without bell: %+v", a.res)
	}
}

// F1: revoking an agent ends only its requests; the emergency stop ends all. Answers
// afterwards change nothing.
func TestCancel(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
	e.admins.put(u2, true)
	reqA, reqB := request(), request()
	reqA.ClientID, reqB.ClientID = "hm-client:a", "hm-client:b"
	chA := e.ask(reqA)
	e.waitOpen(t, 1)
	chB := e.ask(reqB)
	open := e.waitOpen(t, 2)

	if n := e.svc.CancelAgent(""); n != 0 {
		t.Errorf("empty client ID cancelled %d", n)
	}
	if n := e.svc.CancelAgent("hm-client:unknown"); n != 0 {
		t.Errorf("unknown agent cancelled %d", n)
	}
	if n := e.svc.CancelAgent("hm-client:a"); n != 1 {
		t.Errorf("CancelAgent = %d", n)
	}
	if a := wait(t, chA); a.res.Outcome != OutcomeCancelled || a.res.By != "" || a.res.Via != "" {
		t.Errorf("A = %+v", a.res)
	}
	left := e.waitOpen(t, 1)
	if left[0].Request.ClientID != "hm-client:b" {
		t.Errorf("left open = %+v", left)
	}
	for _, o := range open {
		if o.Request.ClientID == "hm-client:a" {
			if _, err := e.svc.Answer(context.Background(), o.ID, u2, true); !errors.Is(err, ErrNotPending) {
				t.Errorf("answer after cancel: %v", err)
			}
		}
	}
	if n := e.svc.CancelAll(); n != 1 {
		t.Errorf("CancelAll = %d", n)
	}
	if a := wait(t, chB); a.res.Outcome != OutcomeCancelled {
		t.Errorf("B = %+v", a.res)
	}
	if n := e.svc.CancelAll(); n != 0 || len(e.svc.Open()) != 0 {
		t.Errorf("second CancelAll = %d, open %+v", n, e.svc.Open())
	}
}

// Open lists what the UI shows: who was reached, who may answer there, and when the
// request expires (the shorter of the mandate's timeout and the upper limit).
func TestOpenRequest(t *testing.T) {
	e := newChannelEnv(t, time.Hour)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	e.svc.cfg.Now = func() time.Time { return now }
	e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
	e.admins.put(u2, true)
	req := request()
	req.Timeout = 90 * time.Second
	req.ClientID = "hm-client:voice"
	ch := e.ask(req)
	open := e.waitOpen(t, 1)[0]
	if !open.CreatedAt.Equal(now) || !open.ExpiresAt.Equal(now.Add(90*time.Second)) || open.Request.Agent != "Voice assistant" ||
		!equalStrings(open.Recipients, []string{u1, u2}) || !equalStrings(open.UIUsers, []string{u2}) {
		t.Errorf("open = %+v", open)
	}
	// Open returns copies: changing them changes nothing inside.
	open.UIUsers[0], open.Recipients[0] = "x", "y"
	if again := e.svc.Open()[0]; again.UIUsers[0] != u2 || again.Recipients[0] != u1 {
		t.Errorf("Open shares its slices: %+v", again)
	}
	e.svc.CancelAll()
	wait(t, ch)
}

// Without an administrator check nobody answers in the UI (fail-closed): a person with
// only the UI channel cannot be reached.
func TestWithoutAdminCheckNoUI(t *testing.T) {
	e := newEnv(t, time.Minute)
	if err := e.approvers.Put(context.Background(), Approver{UserID: u2, UI: true, UICritical: true}); err != nil {
		t.Fatal(err)
	}
	req := request()
	req.Approvers = []string{u2}
	if _, err := e.svc.Ask(context.Background(), req); !errors.Is(err, ErrNoApprover) {
		t.Errorf("Ask = %v, want ErrNoApprover", err)
	}
}

// Bell errors are logged, never shown with the request's content.
func TestBellErrorsAreLogged(t *testing.T) {
	e := newChannelEnv(t, 50*time.Millisecond)
	var buf safeBuffer
	e.svc.cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	e.bellOn, e.bell.fail = true, true
	e.put(t, Approver{UserID: u2, UI: true})
	e.admins.put(u2, true)
	wait(t, e.ask(request()))
	out := buf.String()
	if !strings.Contains(out, "approval hint in Home Assistant not shown") || !strings.Contains(out, "approval hint in Home Assistant not removed") ||
		strings.Contains(out, "Front door") {
		t.Errorf("log:\n%s", out)
	}
}

// A cancellation while the notifications go out stops the delivery: after an emergency
// stop nobody gets a "please approve", and the result is the cancellation.
func TestCancelDuringDelivery(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.put(t, Approver{UserID: u1, Devices: []string{"mobile_app_a", "mobile_app_b", "mobile_app_c"}})
	var once sync.Once
	e.notifier.before = func(string) { once.Do(func() { e.svc.CancelAll() }) }
	req := request()
	req.Approvers = []string{u1}
	res, err := e.svc.Ask(context.Background(), req)
	if err != nil || res.Outcome != OutcomeCancelled {
		t.Fatalf("Ask = %+v, %v", res, err)
	}
	// The first notification was on its way when the stop came; the other two devices
	// get none.
	if n := e.notifier.count(); n != 1 {
		t.Errorf("%d notifications sent, want only the one in flight", n)
	}
	if len(e.svc.Open()) != 0 {
		t.Error("cancelled request listed")
	}
}

// A person removed from the approvers (Withdraw) cannot answer any more, on either
// channel; an answer from their phone then counts as from a stranger.
func TestWithdrawnApprover(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
	e.admins.put(u2, true)
	ch := e.ask(request())
	id := e.waitOpen(t, 1)[0].ID
	nonce := nonceOf(t, e.notifier.next(t))
	if n := e.svc.Withdraw(u2); n != 1 {
		t.Errorf("Withdraw = %d", n)
	}
	if _, err := e.svc.Answer(context.Background(), id, u2, true); !errors.Is(err, ErrNotApprover) {
		t.Errorf("UI answer after withdrawal: %v", err)
	}
	e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u2))
	if a := wait(t, ch); a.res.Outcome != OutcomeInvalidResponse || a.res.By != u2 {
		t.Errorf("phone answer after withdrawal: %+v", a.res)
	}
	if n := e.svc.Withdraw(u2); n != 0 {
		t.Errorf("Withdraw without open requests = %d", n)
	}
}

// Negative catalog: phone, UI and cancellation at the same moment → exactly one result.
func TestThreeWayRace(t *testing.T) {
	e := newChannelEnv(t, time.Minute)
	e.put(t, Approver{UserID: u2, Devices: []string{"mobile_app_anna"}, UI: true})
	e.admins.put(u2, true)
	for range 30 {
		ch := e.ask(request())
		id := e.waitOpen(t, 1)[0].ID
		nonce := nonceOf(t, e.notifier.next(t))
		e.notifier.next(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var uiErr error
		var cancelled int
		wg.Add(3)
		go func() { defer wg.Done(); <-start; e.svc.HandleEvent(event("HM_APPROVE_"+nonce, u1)) }()
		go func() { defer wg.Done(); <-start; _, uiErr = e.svc.Answer(context.Background(), id, u2, false) }()
		go func() { defer wg.Done(); <-start; cancelled = e.svc.CancelAll() }()
		close(start)
		wg.Wait()
		a := wait(t, ch)
		winners := 0
		if uiErr == nil {
			winners++
		}
		winners += cancelled
		if a.res.Via == ViaPush {
			winners++
		}
		if winners != 1 {
			t.Fatalf("result %+v, UI %v, cancelled %d: %d winners", a.res, uiErr, cancelled, winners)
		}
	}
}
