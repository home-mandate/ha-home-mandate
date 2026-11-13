// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

const watchedName = "home.example.org"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fakeSource struct {
	name  string
	mu    sync.Mutex
	certs []Parsed
	err   error
	calls int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(_ context.Context, _, cursor string) (Batch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Batch{}, f.err
	}
	out := f.certs
	f.certs = nil
	return Batch{Certs: out, Cursor: cursor + "x", Unread: 1}, nil
}

func (f *fakeSource) add(p ...Parsed) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.certs = append(f.certs, p...)
}
func (f *fakeSource) fail(err error) { f.mu.Lock(); defer f.mu.Unlock(); f.err = err }

type sent struct {
	service string
	n       ha.Notification
}

type fakeHA struct {
	mu      sync.Mutex
	pushes  []sent
	cleared []string
	bell    map[string]ha.Notification
	failAll bool
}

func (f *fakeHA) Notify(_ context.Context, service string, n ha.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		return errors.New("offline")
	}
	f.pushes = append(f.pushes, sent{service, n})
	return nil
}

func (f *fakeHA) ClearNotification(_ context.Context, service, tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		return errors.New("offline")
	}
	f.cleared = append(f.cleared, service+"/"+tag)
	return nil
}

func (f *fakeHA) Ring(_ context.Context, id string, n ha.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		return errors.New("offline")
	}
	f.bell[id] = n
	return nil
}

func (f *fakeHA) Clear(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		return errors.New("offline")
	}
	delete(f.bell, id)
	return nil
}

func (f *fakeHA) takePushes() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.pushes
	f.pushes = nil
	return out
}

func (f *fakeHA) bellShows(id string) (ha.Notification, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.bell[id]
	return n, ok
}

type fakeApprovers struct {
	list []approval.Approver
	err  error
}

func (f *fakeApprovers) List(context.Context) ([]approval.Approver, error) { return f.list, f.err }

type fakeKey struct {
	mu       sync.Mutex
	key      Digest
	recheck  Digest // returned with recheck when set
	err      error
	rechecks int
}

func (k *fakeKey) Key(_ context.Context, recheck bool) (Digest, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return Digest{}, k.err
	}
	if recheck {
		k.rechecks++
		if k.recheck != (Digest{}) {
			k.key = k.recheck
		}
	}
	return k.key, nil
}

type harness struct {
	t       *testing.T
	w       *Watcher
	store   *Store
	clock   *clock
	spotter *fakeSource
	crtsh   *fakeSource
	ha      *fakeHA
	key     *fakeKey
	appr    *fakeApprovers
	admins  map[string]bool
	changes int
	logs    *syncBuffer
}

// syncBuffer is a log buffer the background settling may write while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Approvers: anna (iPhone, critical; UI with critical), ben (Android not critical; UI
// without critical), and Home-Mandate's own user.
func newHarness(t *testing.T) *harness {
	t.Helper()
	s, _ := newStore(t)
	h := &harness{t: t, store: s, clock: &clock{t: t0}, spotter: &fakeSource{name: SourceCertSpotter},
		crtsh: &fakeSource{name: SourceCrtSh}, ha: &fakeHA{bell: map[string]ha.Notification{}},
		key: &fakeKey{key: digestOf("own-key")}, admins: map[string]bool{"anna": true, "ben": true}, logs: &syncBuffer{}}
	h.appr = &fakeApprovers{list: []approval.Approver{
		{UserID: "anna", Devices: []approval.Device{{Service: "mobile_app_iphone", Critical: true}}, UI: true, UICritical: true, Language: "de"},
		{UserID: "ben", Devices: []approval.Device{{Service: "mobile_app_pixel"}}, UI: true},
		{UserID: "hm-service", Devices: []approval.Device{{Service: "mobile_app_hm", Critical: true}}, UI: true, UICritical: true},
	}}
	h.w = New(Config{Name: watchedName, Store: s, Sources: []Source{h.spotter, h.crtsh}, Key: h.key, Approvers: h.appr,
		IsAdmin:     func(_ context.Context, u string) (bool, error) { return h.admins[u], nil },
		ServiceUser: func() string { return "hm-service" },
		UserName:    func(_ context.Context, u string) string { return map[string]string{"anna": "Anna"}[u] },
		Notifier:    h.ha, Bell: h.ha, Language: func() i18n.Lang { return i18n.EN },
		ConfirmTimeout: func() time.Duration { return 5 * time.Minute }, Link: func() string { return "/hm/system" },
		OnChange: func() { h.changes++ }, Now: h.clock.now,
		Logger: slog.New(slog.NewTextHandler(h.logs, nil))})
	return h
}

func (h *harness) round() { h.w.Round(context.Background()) }

// foreign is a certificate for the watched name with a key not the gateway's.
func foreign(id string, notBefore, notAfter time.Time) Parsed {
	return Parsed{Cert: Cert{Digest: digestOf("tbs-" + id), Names: []string{watchedName}, NotBefore: notBefore, NotAfter: notAfter,
		PublicKey: digestOf("key-" + id), Logged: notBefore}, Issuer: "CN=Evil CA [click](https://x.example)", Serial: id}
}

func TestFirstRoundIsOneInventory(t *testing.T) {
	h := newHarness(t)
	own := foreign("own", t0.Add(-time.Hour), t0.Add(60*24*time.Hour))
	own.PublicKey = h.key.key
	other := foreign("b", t0.Add(-time.Hour), t0.Add(60*24*time.Hour))
	other.Names = []string{"other.example.org"} // not for the name: not even stored
	h.spotter.add(foreign("a", t0.Add(-24*time.Hour), t0.Add(60*24*time.Hour)), own, other)
	h.crtsh.add(foreign("c", t0.Add(-48*time.Hour), t0.Add(30*24*time.Hour)))
	h.round()
	pushes := h.ha.takePushes()
	// One notice per device of each approver (not Home-Mandate's own user): two.
	if len(pushes) != 2 {
		t.Fatalf("%d pushes: %+v", len(pushes), pushes)
	}
	for _, p := range pushes {
		if !strings.Contains(p.n.Title, watchedName) || !strings.Contains(p.n.Message, ": 2") || p.n.URL != "/hm/system" {
			t.Errorf("inventory %+v", p)
		}
		if strings.Contains(p.n.Message, "[click]") || strings.Contains(p.n.Message, "(https") || strings.Contains(p.n.Message, "://") {
			t.Errorf("issuer not cleaned: %q", p.n.Message)
		}
	}
	if !strings.Contains(pushes[0].n.Title, "Bestandsaufnahme") {
		t.Errorf("anna's notice is not in German: %q", pushes[0].n.Title)
	}
	if n, ok := h.ha.bellShows(bellAlarm); !ok || !strings.Contains(n.Message, ": 2") {
		t.Fatalf("bell %+v %v", n, ok)
	}
	certs, _ := h.store.Certs(context.Background())
	if len(certs) != 3 {
		t.Fatalf("%d certificates stored, want the three for the name", len(certs))
	}
	// Nothing new: no push again, the bell stays.
	h.clock.advance(Interval)
	h.round()
	if p := h.ha.takePushes(); len(p) != 0 {
		t.Fatalf("repeated: %+v", p)
	}
	if !strings.Contains(h.logs.String(), "could not be read") || h.changes < 2 {
		t.Error("unread entries not logged or no change announced")
	}
}

func TestLaterAlarmNamesTheCertificate(t *testing.T) {
	h := newHarness(t)
	h.spotter.add(foreign("first", t0, t0.Add(90*24*time.Hour)))
	h.round()
	if p := h.ha.takePushes(); len(p) != 2 || !strings.Contains(p[1].n.Title, "inventory") {
		t.Fatalf("the first notice is the inventory: %+v", p)
	}
	h.clock.advance(Interval)
	soon := foreign("later", t0.Add(24*time.Hour), t0.Add(90*24*time.Hour))
	past := foreign("past", t0.Add(-10*24*time.Hour), t0.Add(time.Minute))
	h.spotter.add(past, soon)
	h.round()
	pushes := h.ha.takePushes()
	if len(pushes) != 2 {
		t.Fatalf("%d pushes", len(pushes))
	}
	en := pushes[1].n
	if !strings.Contains(en.Title, "not obtained") || !strings.Contains(en.Message, "not valid yet") ||
		!strings.Contains(en.Message, "More of them: 1") || !strings.Contains(en.Message, "2026-11-14") {
		t.Fatalf("alarm first, then the count: %+v", en)
	}
	// Only a past exposure is new: it is reported as such.
	h.clock.advance(Interval)
	h.spotter.add(foreign("gone", t0.Add(-10*24*time.Hour), t0.Add(time.Minute)))
	h.round()
	pushes = h.ha.takePushes()
	if len(pushes) != 2 || !strings.Contains(pushes[1].n.Title, "expired") {
		t.Fatalf("past exposure %+v", pushes)
	}
}

// TestRenewalNotYetTakenOverRaisesNoAlarm: before telling, the key is read again.
func TestRenewalNotYetTakenOverRaisesNoAlarm(t *testing.T) {
	h := newHarness(t)
	h.round()
	renewed := foreign("renewed", t0, t0.Add(90*24*time.Hour))
	h.key.recheck = renewed.PublicKey
	h.spotter.add(renewed)
	h.clock.advance(Interval)
	h.round()
	if p := h.ha.takePushes(); len(p) != 0 || h.key.rechecks != 1 {
		t.Fatalf("%d pushes, %d rechecks", len(p), h.key.rechecks)
	}
	if _, ok := h.ha.bellShows(bellAlarm); ok {
		t.Fatal("bell rung for the gateway's own renewal")
	}
	keys, _ := h.store.Keys(context.Background())
	if keys.Current != renewed.PublicKey || len(keys.Earlier) != 1 {
		t.Fatalf("keys %+v", keys)
	}
}

func TestWithoutAKeyEveryCertificateCounts(t *testing.T) {
	h := newHarness(t)
	h.w.cfg.Key = nil
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.round()
	st, err := h.w.Status(context.Background())
	if err != nil || st.KeyKnown || len(st.Certs) != 1 || st.Certs[0].Result != Alarm {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestAKeyThatCannotBeReadKeepsTheLastOne(t *testing.T) {
	h := newHarness(t)
	h.round()
	h.key.err = ErrKey
	h.clock.advance(Interval)
	h.round()
	h.round()
	st, _ := h.w.Status(context.Background())
	if !st.KeyKnown || st.KeyError == "" || strings.Count(h.logs.String(), "own certificate not readable") != 1 {
		t.Fatalf("%+v\n%s", st, h.logs)
	}
}

func TestRateLimitWaitsBeforeAskingAgain(t *testing.T) {
	h := newHarness(t)
	h.spotter.fail(&RateLimitedError{Source: SourceCertSpotter, RetryAfter: time.Hour})
	h.round()
	h.clock.advance(Interval)
	h.round()
	if h.spotter.calls != 1 || h.crtsh.calls != 2 {
		t.Fatalf("calls: spotter %d, crt.sh %d", h.spotter.calls, h.crtsh.calls)
	}
	h.clock.advance(time.Hour)
	h.round()
	if h.spotter.calls != 2 {
		t.Fatalf("not asked again after the wait: %d", h.spotter.calls)
	}
	st, _ := h.store.Source(context.Background(), SourceCertSpotter, watchedName)
	if st.Failure == "" || !st.LastSuccess.IsZero() {
		t.Fatalf("%+v", st)
	}
}

func TestMonitoringNotPossibleAfter24Hours(t *testing.T) {
	h := newHarness(t)
	h.spotter.fail(errors.New("down"))
	h.crtsh.fail(errors.New("down"))
	h.round()
	h.clock.advance(Unavailable - time.Minute)
	h.round()
	if _, ok := h.ha.bellShows(bellUnavailable); ok {
		t.Fatal("rung before 24 hours")
	}
	h.clock.advance(time.Minute)
	h.round()
	n, ok := h.ha.bellShows(bellUnavailable)
	if !ok || !strings.Contains(n.Message, watchedName) {
		t.Fatalf("not rung: %+v", n)
	}
	st, _ := h.w.Status(context.Background())
	if !st.Unavailable || !st.LastSuccess.IsZero() || len(st.Sources) != 2 || st.Sources[0].Failure == "" {
		t.Fatalf("status %+v", st)
	}
	// It rings once: when someone dismissed it, the next round does not bring it back.
	_ = h.ha.Clear(context.Background(), bellUnavailable)
	h.clock.advance(Interval)
	h.round()
	if _, ok := h.ha.bellShows(bellUnavailable); ok {
		t.Fatal("rung again")
	}
	h.crtsh.fail(nil)
	h.round()
	st, _ = h.w.Status(context.Background())
	if st.Unavailable || st.LastSuccess.IsZero() {
		t.Fatalf("after a success %+v", st)
	}
}

func TestBellAndNotificationFailuresAreRetried(t *testing.T) {
	h := newHarness(t)
	h.ha.failAll = true
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.round()
	h.ha.failAll = false
	h.clock.advance(Interval)
	h.round()
	if _, ok := h.ha.bellShows(bellAlarm); !ok {
		t.Fatal("bell not rung after Home Assistant came back")
	}
	if !strings.Contains(h.logs.String(), "notification not delivered") {
		t.Fatal("failed delivery not logged")
	}
}

// ackFor asks for an acknowledgement of a fresh alarm and returns it and the alarm.
func ackFor(t *testing.T, h *harness) (Pending, Parsed) {
	t.Helper()
	p := foreign("a", t0, t0.Add(90*24*time.Hour))
	h.spotter.add(p)
	h.round()
	h.ha.takePushes()
	pending, err := h.w.RequestAck(context.Background(), p.Digest, "anna")
	if err != nil {
		t.Fatal(err)
	}
	return pending, p
}

func action(t *testing.T, s sent, prefix string) string {
	t.Helper()
	for _, a := range s.n.Actions {
		if strings.HasPrefix(a.Action, prefix) {
			return a.Action
		}
	}
	t.Fatalf("no %s action in %+v", prefix, s.n)
	return ""
}

func pressed(user, act string) ha.Event {
	data, _ := json.Marshal(map[string]string{"action": act})
	return ha.Event{EventType: ha.EventMobileAppNotificationAction, Data: data, Context: ha.EventContext{UserID: user}}
}

// waitFor polls until cond holds; settling after a button runs in the background.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAcknowledgementConfirmedOnThePhone(t *testing.T) {
	h := newHarness(t)
	pending, p := ackFor(t, h)
	pushes := h.ha.takePushes()
	// Like a critical request: only anna's iPhone; ben's phone asks for no unlocking.
	if len(pushes) != 1 || pushes[0].service != "mobile_app_iphone" || !pushes[0].n.Sticky || pushes[0].n.Tag == "" {
		t.Fatalf("pushes %+v", pushes)
	}
	if !strings.Contains(pushes[0].n.Message, "Anna") || len(pushes[0].n.Actions) != 2 || !pushes[0].n.Actions[1].Destructive {
		t.Fatalf("request %+v", pushes[0].n)
	}
	if !slices.Equal(pending.UIUsers, []string{"anna"}) || pending.RequestedBy != "anna" {
		t.Fatalf("pending %+v", pending)
	}
	again, err := h.w.RequestAck(context.Background(), p.Digest, "ben")
	if err != nil || again.ID != pending.ID || len(h.ha.takePushes()) != 0 {
		t.Fatalf("a second request for the same certificate: %+v %v", again, err)
	}
	h.w.HandleEvent(pressed("anna", action(t, pushes[0], actionYes)))
	waitFor(t, func() bool { acks, _ := h.store.Acks(context.Background()); return len(acks) == 1 })
	acks, _ := h.store.Acks(context.Background())
	if acks[0].By != "anna" || acks[0].Via != ViaPush || acks[0].Digest != p.Digest {
		t.Fatalf("ack %+v", acks[0])
	}
	waitFor(t, func() bool { _, ok := h.ha.bellShows(bellAlarm); return !ok })
	h.ha.mu.Lock()
	cleared := slices.Clone(h.ha.cleared)
	h.ha.mu.Unlock()
	if len(cleared) != 1 || !strings.HasPrefix(cleared[0], "mobile_app_iphone/hm_ct_") {
		t.Fatalf("cleared %v", cleared)
	}
	info := h.ha.takePushes()
	if len(info) != 2 || !strings.Contains(info[1].n.Message, "on the phone") {
		t.Fatalf("approvers not told who confirmed: %+v", info)
	}
	// The button again does nothing.
	h.w.HandleEvent(pressed("anna", action(t, pushes[0], actionYes)))
	st, _ := h.w.Status(context.Background())
	if len(st.Pending) != 0 || len(st.Certs) != 1 || st.Certs[0].Result != None || st.Certs[0].Ack == nil {
		t.Fatalf("status %+v", st)
	}
}

func TestAcknowledgementButtonsOfOthersAndNo(t *testing.T) {
	for name, tc := range map[string]struct{ user, prefix string }{
		"no":                   {"anna", actionNo},
		"not an approver":      {"mallory", actionYes},
		"Home-Mandate's own":   {"hm-service", actionYes},
		"no user in the event": {"", actionYes},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, _ = ackFor(t, h)
			pushes := h.ha.takePushes()
			h.w.HandleEvent(pressed(tc.user, action(t, pushes[0], tc.prefix)))
			waitFor(t, func() bool { h.ha.mu.Lock(); defer h.ha.mu.Unlock(); return len(h.ha.cleared) == 1 })
			if acks, _ := h.store.Acks(context.Background()); len(acks) != 0 {
				t.Fatal("acknowledged")
			}
			if _, ok := h.ha.bellShows(bellAlarm); !ok {
				t.Fatal("alarm ended")
			}
		})
	}
}

func TestAcknowledgementEventsThatAreNotOurs(t *testing.T) {
	h := newHarness(t)
	_, _ = ackFor(t, h)
	for _, e := range []ha.Event{
		{Data: json.RawMessage(`not json`)},
		pressed("anna", "HM_APPROVE_00112233445566778899aabbccddeeff"),
		pressed("anna", actionYes+"short"),
		pressed("anna", actionYes+strings.Repeat("0", 32)),
	} {
		h.w.HandleEvent(e)
	}
	st, _ := h.w.Status(context.Background())
	if len(st.Pending) != 1 {
		t.Fatalf("pending ended by a foreign event: %+v", st.Pending)
	}
}

func TestAcknowledgementInTheUI(t *testing.T) {
	h := newHarness(t)
	pending, _ := ackFor(t, h)
	ctx := context.Background()
	if err := h.w.AnswerAck(ctx, pending.ID, "mallory", true); !errors.Is(err, ErrNotApprover) {
		t.Fatalf("not an approver: %v", err)
	}
	if err := h.w.AnswerAck(ctx, pending.ID, "hm-service", true); !errors.Is(err, ErrNotApprover) {
		t.Fatalf("own user: %v", err)
	}
	if err := h.w.AnswerAck(ctx, "nope", "anna", true); !errors.Is(err, ErrNotPending) {
		t.Fatalf("unknown: %v", err)
	}
	h.admins["anna"] = false // rights withdrawn meanwhile
	if err := h.w.AnswerAck(ctx, pending.ID, "anna", true); !errors.Is(err, ErrChannel) {
		t.Fatalf("no longer an administrator: %v", err)
	}
	h.admins["anna"] = true
	if err := h.w.AnswerAck(ctx, pending.ID, "anna", true); err != nil {
		t.Fatal(err)
	}
	acks, _ := h.store.Acks(ctx)
	if len(acks) != 1 || acks[0].Via != ViaUI {
		t.Fatalf("%+v", acks)
	}
	if info := h.ha.takePushes(); len(info) != 3 || !strings.Contains(info[2].n.Message, "in Home-Mandate") {
		t.Fatalf("approvers not told: %+v", info)
	}
	if err := h.w.AnswerAck(ctx, pending.ID, "anna", true); !errors.Is(err, ErrNotPending) {
		t.Fatalf("answered twice: %v", err)
	}
}

// ben may answer ordinary requests in the UI, not critical ones; the confirmation of an
// acknowledgement is one.
func TestAcknowledgementNeedsTheCriticalChannel(t *testing.T) {
	h := newHarness(t)
	h.appr.list[0].UI = false // anna only by phone
	pending, _ := ackFor(t, h)
	if err := h.w.AnswerAck(context.Background(), pending.ID, "ben", true); !errors.Is(err, ErrNotApprover) {
		t.Fatalf("ben: %v", err)
	}
	if err := h.w.AnswerAck(context.Background(), pending.ID, "anna", true); !errors.Is(err, ErrChannel) {
		t.Fatalf("anna without the UI: %v", err)
	}
}

func TestAcknowledgementRefusals(t *testing.T) {
	h := newHarness(t)
	p := foreign("a", t0, t0.Add(90*24*time.Hour))
	ctx := context.Background()
	if _, err := h.w.RequestAck(ctx, p.Digest, "anna"); !errors.Is(err, ErrNotAlarming) {
		t.Fatalf("unknown certificate: %v", err)
	}
	h.spotter.add(p)
	h.round()
	h.appr.list = []approval.Approver{{UserID: "ben", Devices: []approval.Device{{Service: "mobile_app_pixel"}}}}
	if _, err := h.w.RequestAck(ctx, p.Digest, "anna"); !errors.Is(err, ErrNoApprover) {
		t.Fatalf("nobody for critical requests: %v", err)
	}
	h.appr.err = errors.New("db")
	if _, err := h.w.RequestAck(ctx, p.Digest, "anna"); err == nil {
		t.Fatal("approvers unreadable")
	}
	if h.w.uiAllowed(ctx, "anna") {
		t.Fatal("UI allowed with approvers unreadable")
	}
	// Every device unreachable, nobody in the UI.
	h.appr.err = nil
	h.appr.list = []approval.Approver{{UserID: "anna", Devices: []approval.Device{{Service: "mobile_app_iphone", Critical: true}}}}
	h.ha.failAll = true
	if _, err := h.w.RequestAck(ctx, p.Digest, "anna"); !errors.Is(err, ErrNoApprover) {
		t.Fatalf("no device reached: %v", err)
	}
}

func TestAcknowledgementExpires(t *testing.T) {
	h := newHarness(t)
	pending, _ := ackFor(t, h)
	h.ha.takePushes()
	h.clock.advance(5 * time.Minute)
	if err := h.w.AnswerAck(context.Background(), pending.ID, "anna", true); !errors.Is(err, ErrNotPending) {
		t.Fatalf("expired: %v", err)
	}
	if st, _ := h.w.Status(context.Background()); len(st.Pending) != 0 {
		t.Fatal("expired one listed")
	}
	h.round()
	h.ha.mu.Lock()
	defer h.ha.mu.Unlock()
	if len(h.ha.cleared) != 1 || !strings.Contains(h.logs.String(), "not confirmed in time") {
		t.Fatalf("cleared %v", h.ha.cleared)
	}
}

func TestStatusListsAlarmsAndAcknowledgementsNewestFirst(t *testing.T) {
	h := newHarness(t)
	a := foreign("a", t0, t0.Add(90*24*time.Hour))
	h.spotter.add(a)
	h.round()
	h.clock.advance(Interval)
	b := foreign("b", t0, t0.Add(90*24*time.Hour))
	h.spotter.add(b)
	h.round()
	if err := h.store.Acknowledge(context.Background(), Ack{Digest: a.Digest, By: "anna", Via: ViaUI, At: t0}); err != nil {
		t.Fatal(err)
	}
	st, err := h.w.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != watchedName || !st.Since.Equal(t0) || !st.KeyKnown || len(st.Certs) != 2 ||
		st.Certs[0].Digest != b.Digest || st.Certs[0].Result != Alarm || st.Certs[1].Ack == nil || st.Certs[1].Result != None {
		t.Fatalf("%+v", st)
	}
}

func TestStoreFailuresStopTheRoundSafely(t *testing.T) {
	h := newHarness(t)
	_, db := newStore(t)
	h.w.cfg.Store = NewStore(db)
	_ = db.Close()
	h.round()
	if _, err := h.w.Status(context.Background()); err == nil {
		t.Fatal("status from a closed store")
	}
	if _, err := h.w.RequestAck(context.Background(), digestOf("x"), "anna"); err == nil {
		t.Fatal("request on a closed store")
	}
	if !strings.Contains(h.logs.String(), "store unavailable") {
		t.Fatal("not logged")
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if err := h.w.AnswerAck(context.Background(), "x", "anna", true); !errors.Is(err, ErrNotPending) {
		t.Fatal(err)
	}
}

func TestApplicable(t *testing.T) {
	for in, want := range map[string]string{
		"":                                             "no_public_url",
		"http://home.example.org":                      "no_tls",
		"https://192.0.2.1:8443":                       "ip_address",
		"https://[2001:db8::1]":                        "ip_address",
		"https://localhost:8443":                       "not_public_name",
		"https://homeassistant.local":                  "not_public_name",
		"https://hm.home.arpa":                         "not_public_name",
		"https://hm.fritz.box.internal":                "not_public_name",
		"https://-bad.example.org":                     "not_public_name",
		"https://in_valid.example.org":                 "not_public_name",
		"https://" + strings.Repeat("a.", 127) + "org": "not_public_name",
		"https://HM.Example.ORG.:8443/x":               "",
	} {
		name, reason := Applicable(in)
		if reason != want || (want == "" && name != "hm.example.org") {
			t.Errorf("Applicable(%q) = %q, %q", in, name, reason)
		}
	}
	if _, reason := Applicable("https://%zz"); reason != "no_tls" {
		t.Errorf("unparsable: %s", reason)
	}
}

func TestDefaultsOfNew(t *testing.T) {
	s, _ := newStore(t)
	w := New(Config{Name: watchedName, Store: s})
	if w.cfg.Language() != i18n.Default || w.confirmTimeout() != defaultConfirmTimeout || w.link() != "" || w.serviceUser() != "" ||
		w.userName(context.Background(), "u") != "u" || w.isAdmin(context.Background(), "u") {
		t.Fatal("defaults")
	}
	w.cfg.Link = func() string { return "javascript:alert(1)" }
	if w.link() != "" {
		t.Fatal("invalid tap target used")
	}
	w.cfg.IsAdmin = func(context.Context, string) (bool, error) { return true, fmt.Errorf("down") }
	if w.isAdmin(context.Background(), "u") {
		t.Fatal("an error counts as administrator")
	}
	w.Round(context.Background()) // no sources, no notifier, no bell: nothing breaks
	if _, err := w.RequestAck(context.Background(), digestOf("x"), "u"); !errors.Is(err, ErrNotAlarming) {
		t.Fatal(err)
	}
}

// TestStoreFailureAtEachStep: whichever table fails, a round ends without a panic and
// says so in the log; nothing is told on half-read data.
func TestStoreFailureAtEachStep(t *testing.T) {
	for _, table := range []string{"ct_names", "ct_keys", "ct_sources", "ct_certs", "ct_acks"} {
		t.Run(table, func(t *testing.T) {
			s, db := newStore(t)
			h := newHarness(t)
			h.w.cfg.Store = s
			h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
			if table != "ct_names" {
				h.round() // the name is watched, so the failure comes later
				h.spotter.add(foreign("b", t0, t0.Add(90*24*time.Hour)))
			}
			h.ha.takePushes()
			if _, err := db.Exec("DROP TABLE " + table); err != nil {
				t.Fatal(err)
			}
			h.clock.advance(Interval)
			h.round()
			if !strings.Contains(h.logs.String(), "store unavailable") && !strings.Contains(h.logs.String(), "not stored") {
				t.Fatalf("not logged:\n%s", h.logs)
			}
			if p := h.ha.takePushes(); len(p) != 0 && table != "ct_sources" {
				t.Fatalf("told on half-read data: %+v", p)
			}
			if _, err := h.w.Status(context.Background()); err == nil {
				t.Fatal("status without the table")
			}
		})
	}
}

// TestAcknowledgementThatCannotBeStored: the confirmation ends, the alarm stays.
func TestAcknowledgementThatCannotBeStored(t *testing.T) {
	s, db := newStore(t)
	h := newHarness(t)
	h.w.cfg.Store = s
	pending, _ := ackFor(t, h)
	if _, err := db.Exec("DROP TABLE ct_acks"); err != nil {
		t.Fatal(err)
	}
	h.ha.failAll = true // the notification cannot be removed either
	if err := h.w.AnswerAck(context.Background(), pending.ID, "anna", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.logs.String(), "acknowledgement not stored") || !strings.Contains(h.logs.String(), "notification not removed") {
		t.Fatalf("not logged:\n%s", h.logs)
	}
}

// TestInventoryWhenTheFirstRoundsFindNothing: the first notice ever is the inventory, even
// when the sources failed or were empty at first.
func TestInventoryWhenTheFirstRoundsFindNothing(t *testing.T) {
	h := newHarness(t)
	h.spotter.fail(errors.New("down"))
	h.round()
	h.spotter.fail(nil)
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.clock.advance(Interval)
	h.round()
	if p := h.ha.takePushes(); len(p) != 2 || !strings.Contains(p[1].n.Title, "inventory") {
		t.Fatalf("%+v", p)
	}
}

// TestNoticeThatReachedNobodyIsSentAgain: a certificate is marked as told only once a
// device was reached.
func TestNoticeThatReachedNobodyIsSentAgain(t *testing.T) {
	h := newHarness(t)
	h.ha.failAll = true
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.round()
	certs, _ := h.store.Certs(context.Background())
	if !certs[0].NotifiedAt.IsZero() {
		t.Fatal("marked as told though nobody was reached")
	}
	h.ha.failAll = false
	h.clock.advance(Interval)
	h.round()
	if p := h.ha.takePushes(); len(p) != 2 {
		t.Fatalf("not sent again: %+v", p)
	}
	certs, _ = h.store.Certs(context.Background())
	if certs[0].NotifiedAt.IsZero() {
		t.Fatal("not marked once told")
	}
	// Approvers without any device: there is nobody to try, so it counts as told.
	h2 := newHarness(t)
	h2.appr.list = []approval.Approver{{UserID: "anna", UI: true}}
	h2.spotter.add(foreign("b", t0, t0.Add(90*24*time.Hour)))
	h2.round()
	if certs, _ := h2.store.Certs(context.Background()); certs[0].NotifiedAt.IsZero() {
		t.Fatal("waiting for a device that does not exist")
	}
	// Approvers unreadable: told again later.
	h3 := newHarness(t)
	h3.appr.err = errors.New("db")
	h3.spotter.add(foreign("c", t0, t0.Add(90*24*time.Hour)))
	h3.round()
	if certs, _ := h3.store.Certs(context.Background()); !certs[0].NotifiedAt.IsZero() {
		t.Fatal("marked as told with the approvers unreadable")
	}
}

// TestBellsAfterARestart: a new watcher on the store of an old one takes back a "not
// possible" notification left from before and rings the alarm bell anew.
func TestBellsAfterARestart(t *testing.T) {
	h := newHarness(t)
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.round()
	h.ha.bell[bellUnavailable] = ha.Notification{Message: "left from before"}
	restarted := New(h.w.cfg)
	restarted.Round(context.Background())
	if _, ok := h.ha.bellShows(bellUnavailable); ok {
		t.Fatal("old notification left")
	}
	if _, ok := h.ha.bellShows(bellAlarm); !ok {
		t.Fatal("alarm bell not rung after the restart")
	}
}

// TestBellRingsOnlyWhenTheCountChanges: dismissed, it comes back with a change only.
func TestBellRingsOnlyWhenTheCountChanges(t *testing.T) {
	h := newHarness(t)
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.round()
	_ = h.ha.Clear(context.Background(), bellAlarm)
	h.clock.advance(Interval)
	h.round()
	if _, ok := h.ha.bellShows(bellAlarm); ok {
		t.Fatal("rung again without a change")
	}
	h.spotter.add(foreign("b", t0, t0.Add(90*24*time.Hour)))
	h.clock.advance(Interval)
	h.round()
	if n, ok := h.ha.bellShows(bellAlarm); !ok || !strings.Contains(n.Message, ": 2") {
		t.Fatalf("not rung with the new count: %+v", n)
	}
}

// TestPushAnswerOfAnApproverRemovedMeanwhile: the button counts only for someone who is
// still an approver with a device for critical requests.
func TestPushAnswerOfAnApproverRemovedMeanwhile(t *testing.T) {
	h := newHarness(t)
	_, _ = ackFor(t, h)
	pushes := h.ha.takePushes()
	h.appr.list = h.appr.list[1:] // anna is no approver any more
	h.w.HandleEvent(pressed("anna", action(t, pushes[0], actionYes)))
	waitFor(t, func() bool { return strings.Contains(h.logs.String(), "may no longer confirm") })
	if acks, _ := h.store.Acks(context.Background()); len(acks) != 0 {
		t.Fatal("acknowledged")
	}
}

// TestConcurrentRequestsSendOneConfirmation: two "This is mine" at once, one request.
func TestConcurrentRequestsSendOneConfirmation(t *testing.T) {
	h := newHarness(t)
	p := foreign("a", t0, t0.Add(90*24*time.Hour))
	h.spotter.add(p)
	h.round()
	h.ha.takePushes()
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i := range ids {
		wg.Go(func() {
			got, err := h.w.RequestAck(context.Background(), p.Digest, "anna")
			if err != nil {
				t.Error(err)
			}
			ids[i] = got.ID
		})
	}
	wg.Wait()
	if ids[0] != ids[1] || len(h.ha.takePushes()) != 1 {
		t.Fatalf("two confirmations: %v", ids)
	}
}

func TestEventsAfterTheStopAreIgnored(t *testing.T) {
	h := newHarness(t)
	_, _ = ackFor(t, h)
	pushes := h.ha.takePushes()
	h.w.stop()
	h.w.HandleEvent(pressed("anna", action(t, pushes[0], actionYes)))
	if st, _ := h.w.Status(context.Background()); len(st.Pending) != 1 {
		t.Fatal("taken after the stop")
	}
}
