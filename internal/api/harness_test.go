// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/spec"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

const (
	household      = "household:hm-0123456789ab"
	adminID        = "8f2b1c0d9e7a4b3c8f2b1c0d9e7a4b3c" // Markus, owner
	annaID         = "1a2b3c4d5e6f708192a3b4c5d6e7f809" // Anna, administrator
	guestID        = "0f1e2d3c4b5a69788796a5b4c3d2e1f0" // Gast, no administrator
	serviceID      = "5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b" // Home-Mandate's own user
	remote         = supervisorAddr + ":40404"
	supervisorAddr = "172.30.32.2"
)

// fakeHA is a scriptable Home Assistant.
type fakeHA struct {
	mu        sync.Mutex
	users     []ha.AuthUser
	usersErr  error
	states    []ha.State
	entities  []ha.EntityEntry
	devices   []ha.Device
	services  []string
	err       error // for everything but the users
	notified  []string
	notifyErr map[string]error
	userCalls int
}

func (f *fakeHA) ListUsers(context.Context) ([]ha.AuthUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCalls++
	return slices.Clone(f.users), f.usersErr
}

func (f *fakeHA) GetStates(context.Context) ([]ha.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states, f.err
}

func (f *fakeHA) ListEntities(context.Context) ([]ha.EntityEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entities, f.err
}

func (f *fakeHA) ListDevices(context.Context) ([]ha.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices, f.err
}

func (f *fakeHA) NotifyServices(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.services, f.err
}

func (f *fakeHA) Notify(_ context.Context, service string, n ha.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.notifyErr[service]; err != nil {
		return err
	}
	f.notified = append(f.notified, service+"|"+n.Title+"|"+n.Message+"|"+strings.Repeat("A", len(n.Actions)))
	return nil
}

func (f *fakeHA) set(fn func(*fakeHA)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeHA) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.notified)
}

func fakeHousehold() *fakeHA {
	return &fakeHA{
		users: []ha.AuthUser{
			{ID: adminID, Name: "Markus", IsOwner: true, IsActive: true, GroupIDs: []string{"system-admin"}},
			{ID: annaID, Name: "Anna", IsActive: true, GroupIDs: []string{"system-admin"}},
			{ID: guestID, Name: "Gast", IsActive: true, GroupIDs: []string{"system-users"}},
			{ID: serviceID, Name: "Home-Mandate", IsActive: true, GroupIDs: []string{"system-admin"}},
			{ID: "supervisor00000000000000000000000", Name: "Supervisor", IsActive: true, SystemGenerated: true, GroupIDs: []string{"system-admin"}},
		},
		states: []ha.State{
			{EntityID: "person.markus", State: "home", Attributes: map[string]any{"friendly_name": "Markus M.", "user_id": adminID,
				"device_trackers": []any{"device_tracker.iphone_markus", "device_tracker.mac"}}},
			{EntityID: "person.anna", State: "home", Attributes: map[string]any{"user_id": annaID, "device_trackers": []any{"device_tracker.pixel"}}},
			{EntityID: "person.gast", State: "away", Attributes: map[string]any{"friendly_name": "Gast", "user_id": guestID}},
			{EntityID: "person.hm", State: "home", Attributes: map[string]any{"friendly_name": "HM", "user_id": serviceID}},
			{EntityID: "person.nobody", State: "home", Attributes: map[string]any{"friendly_name": "No user"}},
			{EntityID: "light.kitchen", State: "on"},
		},
		entities: []ha.EntityEntry{
			{EntityID: "device_tracker.iphone_markus", DeviceID: "dev-iphone"},
			{EntityID: "device_tracker.mac", DeviceID: "dev-mac"},
			{EntityID: "device_tracker.pixel", DeviceID: "dev-pixel"},
		},
		devices: []ha.Device{
			{ID: "dev-iphone", Name: "iPhone von Markus", Manufacturer: "Apple", Model: "iPhone16,1"},
			{ID: "dev-mac", Name: "MacBook Pro", NameByUser: "Arbeits-Mac <b>", Manufacturer: "Apple", Model: "MacBookPro18,1"},
			{ID: "dev-pixel", Name: "Pixel 9", Manufacturer: "Google", Model: "Pixel 9"},
			{ID: "dev-ipad", Name: "Küchen-iPad", Manufacturer: "Apple", Model: "iPad13,1"},
		},
		services: []string{"mobile_app_iphone_von_markus", "mobile_app_kuchen_ipad", "mobile_app_macbook_pro", "mobile_app_old_phone", "mobile_app_pixel_9"},
	}
}

// fakeCatalog is the device catalog.
type fakeCatalog struct {
	devices  []catalog.Device
	areas    []catalog.Area
	notReady bool
}

func (c *fakeCatalog) Ready() bool           { return !c.notReady }
func (c *fakeCatalog) All() []catalog.Device { return c.devices }
func (c *fakeCatalog) Areas() []catalog.Area { return c.areas }
func (c *fakeCatalog) Lookup(id string) (catalog.Device, bool) {
	i := slices.IndexFunc(c.devices, func(d catalog.Device) bool { return d.EntityID == id })
	if i < 0 {
		return catalog.Device{}, false
	}
	return c.devices[i], true
}

func house() *fakeCatalog {
	return &fakeCatalog{
		devices: []catalog.Device{
			{EntityID: "light.kitchen", Category: "light", Area: "kitchen", Attributes: map[string]any{"friendly_name": "Küchenlicht"}},
			{EntityID: "lock.front_door", Category: "lock", Area: "hall", Attributes: map[string]any{"friendly_name": "Haustür"}},
			{EntityID: "sensor.outside", Category: "sensor"},
			{EntityID: "number.wallbox", Category: "other", Attributes: map[string]any{"friendly_name": "Wallbox"}},
		},
		areas: []catalog.Area{{ID: "hall", Name: "Flur"}, {ID: "kitchen", Name: "Küche"}},
	}
}

type harness struct {
	t         *testing.T
	srv       *Server
	h         http.Handler
	ha        *fakeHA
	cat       *fakeCatalog
	marks     *fakeMarks
	renames   *catalog.Renames
	st        *store.Store
	log       *audit.Log
	agents    *agent.Store
	mandates  *mandate.Store
	adm       *admission.Store
	approvers *approval.Approvers
	approvals *approval.Service
	notifier  *recordingNotifier
	now       *clock
	status    Status
	pairing   Pairing
	direct    SignIn // direct mode on when set
	publicURL string
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recordingNotifier takes the pushes of the approval service.
type recordingNotifier struct {
	mu   sync.Mutex
	sent []ha.Notification
}

func (n *recordingNotifier) Notify(_ context.Context, _ string, msg ha.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, msg)
	return nil
}

var testStart = time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)

func voiceTemplate(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := fs.ReadFile(spec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	if edit != nil {
		edit(doc)
	}
	out, _ := json.Marshal(doc)
	return out
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), household)
	agents := agent.New(st.DB(), log)
	mandates := mandate.New(st.DB(), log, household, "urn:uuid:5b0c9f4e-8f1a-4c2e-9d3b-7a6e5f4d3c2b")
	adm := admission.New(st.DB(), log, agents, mandates, household)
	// The template is part of the household the tests start with: stored directly, so
	// that the audit log starts empty.
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO mandate_templates (name, document, created_at, created_by) VALUES (?, ?, ?, ?)`,
		"voice-assistant", string(voiceTemplate(t, nil)), testStart.Format(time.RFC3339Nano), adminID); err != nil {
		t.Fatal(err)
	}
	renames, err := catalog.LoadRenames(ctx, st.DB())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ha: fakeHousehold(), cat: house(), marks: &fakeMarks{set: map[string]string{}}, renames: renames, st: st, log: log, agents: agents, mandates: mandates, adm: adm,
		approvers: approval.NewApprovers(st.DB(), log), notifier: &recordingNotifier{}, now: &clock{t: testStart},
		status: Status{HAConnected: true, HASince: testStart.Add(-time.Hour), HAVersion: "2026.9.4", ServiceUser: serviceID,
			TimeZone: "Europe/Berlin", Language: "de", Units: map[string]string{"temperature": "°C", "length": "km", "extra": "x"}}}
	h.build()
	return h
}

// build (re)creates the server, e.g. after a change of the pairing service.
func (h *harness) build() {
	var srv *Server
	h.approvals = approval.New(approval.Config{Approvers: h.approvers, Notifier: h.notifier, MaxTimeout: time.Minute,
		ServiceUser: func() string { return serviceID },
		IsAdmin:     func(ctx context.Context, u string) (bool, error) { return srv.IsAdmin(ctx, u) },
		OnOpened:    func(o approval.Open) { srv.ApprovalOpened(o) }})
	srv = New(Config{Store: h.st, Log: h.log, Agents: h.agents, Mandates: h.mandates, Admission: h.adm, Approvers: h.approvers,
		Approvals: h.approvals, Pairing: h.pairing, HA: h.ha, Catalog: h.cat, Marks: h.marks, Renames: h.renames, Status: func() Status { return h.status },
		UI:    http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ui") }),
		Proxy: netip.MustParseAddr(supervisorAddr), Principal: household, Mode: "app", Version: "0.1.0", Commit: "abc123", MCPURL: "https://hm.example.org:8765/mcp",
		TLS: func() TLSStatus { return TLSStatus{Present: true, ValidUntil: testStart.Add(90 * 24 * time.Hour)} }, Retention: 30 * 24 * time.Hour, ApprovalTimeout: 5 * time.Minute,
		Direct: h.direct, PublicURL: h.publicURL,
		DirectUI: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "direct ui") }),
		Now:      h.now.Now})
	h.log.OnCommit(srv.AuditCommitted)
	h.srv, h.h = srv, srv.Handler()
}

type reqOpt func(*http.Request)

func as(user string) reqOpt { return func(r *http.Request) { r.Header.Set("X-Remote-User-Id", user) } }
func header(k, v string) reqOpt {
	return func(r *http.Request) {
		if v == "" {
			r.Header.Del(k)
			return
		}
		r.Header.Set(k, v)
	}
}
func from(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }

type result struct {
	code   int
	header http.Header
	body   []byte
}

func (r result) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("answer %d is no JSON: %v\n%s", r.code, err, r.body)
	}
}

func (r result) errCode() string {
	var e errorBody
	_ = json.Unmarshal(r.body, &e)
	return e.Code
}

func (r result) field() string {
	var e errorBody
	_ = json.Unmarshal(r.body, &e)
	return e.Field
}

const autoCSRF = "auto"

// withApproversVersion adds the current approvers version to changes of approvers that
// do not name one (the tests of conflicts name theirs).
func (h *harness) withApproversVersion(method, path string, body any) (any, string) {
	if !strings.HasPrefix(path, "/api/approvers/") || strings.HasSuffix(path, "/test") {
		return body, path
	}
	version, err := h.approvers.Version(context.Background())
	if err != nil {
		version = strings.Repeat("0", 32)
	}
	switch {
	case method == http.MethodPut:
		if m, ok := body.(map[string]any); ok {
			if _, has := m["base_version"]; !has {
				copied := maps.Clone(m)
				copied["base_version"] = version
				return copied, path
			}
		}
	case method == http.MethodDelete && !strings.Contains(path, "?"):
		return body, path + "?base_version=" + version
	}
	return body, path
}

// do sends a request as the Supervisor would, for the owner, with what a write needs.
func (h *harness) do(method, path string, body any, opts ...reqOpt) result {
	h.t.Helper()
	body, path = h.withApproversVersion(method, path, body)
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, "http://ingress.local"+path, rd)
	req.RemoteAddr = remote
	req.Header.Set("X-Remote-User-Id", adminID)
	if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("X-HM-CSRF", autoCSRF)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	if req.Header.Get("X-HM-CSRF") == autoCSRF { // the token of whoever the request is from
		req.Header.Set("X-HM-CSRF", h.srv.csrfToken(req.Header.Get("X-Remote-User-Id"), h.now.Now()))
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return result{code: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func (h *harness) ok(method, path string, body any, out any, opts ...reqOpt) {
	h.t.Helper()
	r := h.do(method, path, body, opts...)
	if r.code != http.StatusOK && r.code != http.StatusNoContent {
		h.t.Fatalf("%s %s = %d %s", method, path, r.code, r.body)
	}
	if out != nil {
		r.json(h.t, out)
	}
}

// admit admits an agent through the admission, as a pairing would.
func (h *harness) admit(name string) agent.Agent {
	h.t.Helper()
	a, _, err := h.adm.Admit(context.Background(), admission.Request{DisplayName: name, Template: "voice-assistant",
		OAuthClient: "n8n-" + strings.ToLower(name), Resource: "https://hm.example.org/mcp", By: audit.Actor{Kind: audit.ActorUser, ID: adminID}})
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

func (h *harness) mandateOf(clientID string) mandate.Info {
	h.t.Helper()
	m, err := h.mandates.ForAgent(context.Background(), clientID)
	if err != nil {
		h.t.Fatal(err)
	}
	return m.Info
}

// putApprover stores an approver directly.
func (h *harness) putApprover(ap approval.Approver) {
	h.t.Helper()
	if err := h.approvers.Put(context.Background(), ap, audit.Actor{Kind: audit.ActorUser, ID: adminID}); err != nil {
		h.t.Fatal(err)
	}
}

// ask opens an approval request as the gateway would and, like it, writes the audit
// entry of the outcome with the request ID. It returns the request ID. A request still
// open when the test ends is dropped with it: its timeout must not report into a test
// that is over (and whose store is closed).
func (h *harness) ask(req approval.Request) (string, chan approval.Result) {
	h.t.Helper()
	done := make(chan approval.Result, 1)
	ctx := h.t.Context()
	go func() {
		res, err := h.approvals.Ask(ctx, req)
		if ctx.Err() != nil {
			close(done)
			return
		}
		if err != nil {
			h.t.Errorf("Ask: %v", err)
			close(done)
			return
		}
		e := audit.Entry{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: req.ClientID, DisplayName: req.Agent},
			Request:    &audit.Request{Time: testStart, Resource: audit.Resource{EntityID: req.EntityID, Area: req.Area}, Action: req.Action},
			Mandate:    &audit.Mandate{ID: "m-voice", Digest: "sha256:" + strings.Repeat("a", 64)},
			Evaluation: &audit.Evaluation{Decision: "ask", Reason: "rule", RuleID: ptr("r-locks")}, ApprovalID: res.ID,
			Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}}
		switch res.Outcome {
		case approval.OutcomeCancelled:
			e.Result.DeniedBy = audit.DeniedByEmergencyStop
		default:
			e.Approval = &audit.Approval{Outcome: res.Outcome, By: res.By, Via: res.Via, At: res.At}
			if res.Outcome == approval.OutcomeApproved {
				e.Result = &audit.Result{Status: audit.StatusExecuted}
			}
		}
		if _, err := h.log.Append(context.Background(), e); err != nil {
			h.t.Errorf("Append: %v", err)
		}
		done <- res
	}()
	for range 500 {
		for _, o := range h.approvals.Open() {
			if o.Request.ClientID == req.ClientID && o.Request.Action == req.Action {
				return o.ID, done
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("request not open")
	return "", nil
}

func unlockRequest(approvers ...string) approval.Request {
	return approval.Request{ClientID: "hm-client:voice-1", Agent: "Voice <b>", EntityID: "lock.front_door", Area: "hall",
		Device: "Haustür\u202e", Action: "unlock", Reason: "[click](https://evil.example) now", Approvers: approvers,
		Params: map[string]any{"code": "1234"}, Critical: true}
}

func lightRequest(approvers ...string) approval.Request {
	return approval.Request{ClientID: "hm-client:voice-1", Agent: "Voice", EntityID: "light.kitchen", Area: "kitchen",
		Device: "Küchenlicht", Action: "turn_on", Approvers: approvers}
}

var errHA = errors.New("home assistant unreachable")

func context_(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithCancel(t.Context())
}

// draft is the voice assistant example as a draft (types.ts MandateDraft).
func draft(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	_ = json.Unmarshal(voiceTemplate(t, nil), &doc)
	out := map[string]any{}
	for _, k := range draftKeys {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}
	return out
}
