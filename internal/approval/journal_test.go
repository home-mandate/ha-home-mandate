// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

// Tests of the approval journal (issue #27, SPEC-v0 section 11.1 items 8 and 9).

type cleared struct{ service, tag string }

// fakeClearer records removed notifications.
type fakeClearer struct {
	mu   sync.Mutex
	done []cleared
	err  error
}

func (f *fakeClearer) ClearNotification(_ context.Context, service, tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = append(f.done, cleared{service, tag})
	return f.err
}

func (f *fakeClearer) list() []cleared {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.done)
}

type journalEnv struct {
	env
	db      *sql.DB
	log     *audit.Log
	journal *Journal
	clearer *fakeClearer
	logs    *safeBuffer
	now     time.Time
}

func newJournalEnv(t *testing.T) *journalEnv {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), "household:hm-0123456789ab")
	approvers := NewApprovers(st.DB(), log)
	for _, a := range []Approver{{UserID: u1, Devices: phones("mobile_app_markus", "mobile_app_tablet"), Language: "de"},
		{UserID: u2, Devices: phones("mobile_app_anna")}} {
		if err := approvers.Put(context.Background(), a, changer); err != nil {
			t.Fatal(err)
		}
	}
	je := &journalEnv{db: st.DB(), log: log, clearer: &fakeClearer{}, logs: &safeBuffer{},
		now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	logger := slog.New(slog.NewTextHandler(je.logs, nil))
	je.journal = NewJournal(st.DB(), logger)
	je.journal.SetClock(func() time.Time { return je.now })
	n := newFakeNotifier()
	svc := New(Config{Approvers: approvers, Notifier: n, Language: func() i18n.Lang { return i18n.EN }, MaxTimeout: time.Minute,
		Journal: je.journal, Clearer: je.clearer, Logger: logger, Now: func() time.Time { return je.now }})
	je.env = env{svc: svc, notifier: n, approvers: approvers}
	return je
}

// journaled is a request with what the gateway hands the journal.
func journaled() Request {
	req := request()
	req.EntityID, req.Approvers = "lock.front_door", []string{u1, u2}
	req.Record = &Record{ParamsDigest: "sha256:" + strings.Repeat("b", 64), Entry: audit.Entry{
		Agent: &audit.Agent{ClientID: req.ClientID, DisplayName: req.Agent},
		Request: &audit.Request{Time: time.Date(2026, 10, 10, 11, 59, 0, 0, time.UTC), Timezone: "Europe/Berlin",
			Resource: audit.Resource{EntityID: "lock.front_door", Category: "lock"}, Action: "unlock"},
		Mandate:    &audit.Mandate{ID: "m-voice", Digest: "sha256:" + strings.Repeat("a", 64)},
		Evaluation: &audit.Evaluation{Decision: "ask", Reason: "rule", RuleID: ptrTo("r-locks"), ApprovalTimeout: "PT1M"},
	}}
	return req
}

func ptrTo[T any](v T) *T { return &v }

type journalRow struct {
	ID, ClientID, EntityID, Action, Digest, State, Tag, Notified, Decision, Outcome, Cause, By, Via, Notice string
	Result                                                                                                  sql.NullString
}

func (je *journalEnv) rows(t *testing.T) []journalRow {
	t.Helper()
	rs, err := je.db.Query(`SELECT id, client_id, entity_id, action, params_digest, state, tag, notified, decision, outcome, cause,
		answered_by, answered_via, notice, result FROM approval_journal ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []journalRow
	for rs.Next() {
		var r journalRow
		if err := rs.Scan(&r.ID, &r.ClientID, &r.EntityID, &r.Action, &r.Digest, &r.State, &r.Tag, &r.Notified, &r.Decision,
			&r.Outcome, &r.Cause, &r.By, &r.Via, &r.Notice, &r.Result); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A request is in the journal before the first notification leaves, with a tag that is
// neither its ID nor the nonce, the devices asked and no secret.
func TestRequestIsJournaledBeforeAnyoneIsNotified(t *testing.T) {
	je := newJournalEnv(t)
	var before []journalRow
	je.notifier.before = func(string) {
		if before == nil {
			before = je.rows(t)
		}
	}
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	nonce := nonceOf(t, s)
	je.svc.HandleEvent(event("HM_APPROVE_"+nonce, u2))
	a := wait(t, ch)
	if a.err != nil || a.res.Outcome != OutcomeApproved {
		t.Fatalf("result = %+v, %v", a.res, a.err)
	}
	if len(before) != 1 || before[0].ID != a.res.ID || before[0].State != stateOpen || before[0].ClientID != "hm-client:voice" ||
		before[0].EntityID != "lock.front_door" || before[0].Action != "unlock" || !strings.HasPrefix(before[0].Digest, "sha256:") {
		t.Fatalf("journal before the first notification = %+v", before)
	}
	row := before[0]
	if s.n.Tag != row.Tag || !strings.HasPrefix(row.Tag, tagPrefix) || strings.Contains(row.Tag, nonce) || strings.Contains(row.Tag, row.ID) {
		t.Errorf("tag %q, notification tag %q", row.Tag, s.n.Tag)
	}
	var notified []Notified
	if err := json.Unmarshal([]byte(row.Notified), &notified); err != nil {
		t.Fatal(err)
	}
	want := []Notified{{u1, "mobile_app_markus", "de"}, {u1, "mobile_app_tablet", "de"}, {u2, "mobile_app_anna", "en"}}
	if !slices.Equal(notified, want) {
		t.Errorf("notified = %+v", notified)
	}
	var stored string
	_ = je.db.QueryRow(`SELECT group_concat(id || client_id || entity_id || action || params_digest || tag || notified || decision) FROM approval_journal`).Scan(&stored)
	if strings.Contains(stored, nonce) || strings.Contains(stored, hashKey(nonce)) {
		t.Error("the journal holds the nonce")
	}
	// The request ends in the journal with the audit entry (the gateway's part), not here.
	if rows := je.rows(t); rows[0].State != stateOpen {
		t.Errorf("state after the answer = %s", rows[0].State)
	}
}

type failingJournal struct{}

func (failingJournal) Open(context.Context, Opened) error { return errors.New("disk full") }

func (failingJournal) Delivered(context.Context, string, []Notified) error { return nil }

// Fail closed: a request the journal cannot take is denied and nobody is notified.
func TestRequestIsDeniedWithoutTheJournal(t *testing.T) {
	je := newJournalEnv(t)
	je.svc.cfg.Journal = failingJournal{}
	res, err := je.svc.Ask(context.Background(), journaled())
	if !errors.Is(err, ErrJournal) || res.ID != "" {
		t.Errorf("Ask = %+v, %v", res, err)
	}
	if n := je.notifier.count(); n != 0 || len(je.svc.Open()) != 0 {
		t.Errorf("%d notifications, open %v", n, je.svc.Open())
	}
	if !strings.Contains(je.logs.String(), "not entered in the journal") {
		t.Errorf("log = %s", je.logs.String())
	}
	je.svc.cfg.Journal = je.journal
	req := journaled()
	req.Record = nil
	if _, err := je.svc.Ask(context.Background(), req); !errors.Is(err, ErrJournal) {
		t.Errorf("without a record: %v", err)
	}
}

// However a request ends, its notifications are removed from the devices it reached
// (and only those), by its tag.
func TestNotificationsAreClearedHoweverTheRequestEnds(t *testing.T) {
	for name, end := range map[string]func(je *journalEnv, nonce string){
		"answered":       func(je *journalEnv, nonce string) { je.svc.HandleEvent(event("HM_DENY_"+nonce, u2)) },
		"invalid answer": func(je *journalEnv, nonce string) { je.svc.HandleEvent(event("HM_APPROVE_"+nonce, u3)) },
		"revoked":        func(je *journalEnv, _ string) { je.svc.CancelAgent("hm-client:voice") },
		"emergency stop": func(je *journalEnv, _ string) { je.svc.CancelAll() },
		"timeout":        func(*journalEnv, string) {},
	} {
		t.Run(name, func(t *testing.T) {
			je := newJournalEnv(t)
			je.svc.cfg.Now = time.Now
			je.notifier.setFail("mobile_app_tablet", true)
			req := journaled()
			req.Timeout = 200 * time.Millisecond
			ch := je.ask(req)
			s := je.notifier.next(t)
			_ = je.notifier.next(t)
			end(je, nonceOf(t, s))
			a := wait(t, ch)
			je.svc.background.Wait()
			want := []cleared{{"mobile_app_markus", s.n.Tag}, {"mobile_app_anna", s.n.Tag}}
			if got := je.clearer.list(); !slices.Equal(got, want) {
				t.Errorf("%s (%s): cleared %+v", name, a.res.Outcome, got)
			}
		})
	}
}

func TestClearingFailuresAreLogged(t *testing.T) {
	je := newJournalEnv(t)
	je.clearer.err = errors.New("disconnected")
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	je.svc.HandleEvent(event("HM_APPROVE_"+nonceOf(t, s), u1))
	wait(t, ch)
	je.svc.background.Wait()
	if !strings.Contains(je.logs.String(), "notification of an ended approval request not removed") {
		t.Errorf("log = %s", je.logs.String())
	}
}

// A request that reached nobody is in the journal; the error names it so that its
// denial ends it there.
func TestUndeliveredRequestNamesItsJournalEntry(t *testing.T) {
	je := newJournalEnv(t)
	for _, s := range []string{"mobile_app_markus", "mobile_app_tablet", "mobile_app_anna"} {
		je.notifier.setFail(s, true)
	}
	res, err := je.svc.Ask(context.Background(), journaled())
	if !errors.Is(err, ErrNoApprover) || res.ID == "" {
		t.Fatalf("Ask = %+v, %v", res, err)
	}
	if rows := je.rows(t); len(rows) != 1 || rows[0].ID != res.ID {
		t.Errorf("journal = %+v", rows)
	}
	je.svc.background.Wait()
	if got := je.clearer.list(); len(got) != 0 {
		t.Errorf("cleared undelivered notifications: %+v", got)
	}
}

// open enters a request directly, as Ask does.
func (je *journalEnv) open(t *testing.T, id string) Opened {
	t.Helper()
	req := journaled()
	req.Record.AgentRef = "apr_" + id
	o := Opened{ID: id, Tag: tagPrefix + id, Request: req, Created: je.now, Expires: je.now.Add(time.Minute),
		Notified: []Notified{{u1, "mobile_app_markus", "de"}, {u2, "mobile_app_anna", "en"}}}
	if err := je.journal.Open(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	return o
}

func (je *journalEnv) entries(t *testing.T) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := je.log.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for line := range strings.Lines(buf.String()) {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e["event"] == audit.EventDecision {
			out = append(out, e)
		}
	}
	return out
}

func field(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

var (
	idOpen      = strings.Repeat("1", 32)
	idExecuting = strings.Repeat("2", 32)
	idEnded     = strings.Repeat("3", 32)
)

// SPEC-v0 section 11.1 item 9: after a restart a waiting request is cancelled/interrupted,
// an execution under way failed/outcome_unknown with the answer that confirmed it, an
// ended one is left alone; nothing is reopened, and a second run writes nothing.
func TestRecoverEndsWhatTheLastProcessLeft(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	je.open(t, idOpen)
	je.open(t, idExecuting)
	je.open(t, idEnded)
	answered := je.now.Add(20 * time.Second)
	if err := je.journal.Executing(ctx, idExecuting, audit.Approval{Outcome: OutcomeApproved, By: u1, Via: ViaPush, At: answered}); err != nil {
		t.Fatal(err)
	}
	result := audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
	e := journaled().Record.Entry
	e.Event, e.Result, e.Approval = audit.EventDecision, &result, &audit.Approval{Outcome: OutcomeRejected, By: u2, Via: ViaUI, At: answered}
	e.Also = je.journal.End(idEnded, e.Approval, result)
	if _, err := je.log.Append(ctx, e); err != nil {
		t.Fatal(err)
	}

	je.now = je.now.Add(time.Hour) // the restart
	got, err := je.journal.Recover(ctx, je.log)
	if err != nil || got != (Recovered{Interrupted: 1, Unknown: 1}) {
		t.Fatalf("Recover = %+v, %v", got, err)
	}
	entries := je.entries(t)
	if len(entries) != 3 {
		t.Fatalf("%d decision entries", len(entries))
	}
	interrupted, unknown := entries[1], entries[2]
	if field(interrupted, "approval", "outcome") != "cancelled" || field(interrupted, "approval", "cause") != "interrupted" ||
		field(interrupted, "approval", "at") != "2026-10-10T13:00:00.000Z" || field(interrupted, "approval", "by") != nil ||
		field(interrupted, "result", "status") != "denied" || field(interrupted, "result", "denied_by") != "approval" ||
		field(interrupted, "request", "resource", "entity_id") != "lock.front_door" || field(interrupted, "mandate", "id") != "m-voice" ||
		field(interrupted, "evaluation", "decision") != "ask" {
		t.Errorf("interrupted = %v", interrupted)
	}
	if field(unknown, "approval", "outcome") != "approved" || field(unknown, "approval", "by") != u1 || field(unknown, "approval", "via") != "push" ||
		field(unknown, "approval", "at") != "2026-10-10T12:00:20.000Z" || field(unknown, "result", "status") != "failed" ||
		field(unknown, "result", "error") != "outcome_unknown" {
		t.Errorf("outcome unknown = %v", unknown)
	}
	rows := je.rows(t)
	for _, r := range rows {
		if r.State != stateEnded {
			t.Errorf("%s: state %s", r.ID, r.State)
		}
	}
	if rows[0].Notice != noticeInterrupted || rows[0].Outcome != OutcomeCancelled || rows[0].Cause != audit.CauseInterrupted ||
		rows[1].Notice != noticeUnknown || rows[1].Outcome != OutcomeApproved || rows[2].Notice != "" || rows[2].Outcome != OutcomeRejected {
		t.Errorf("rows = %+v", rows)
	}
	if got, err := je.journal.Recover(ctx, je.log); err != nil || got != (Recovered{}) || len(je.entries(t)) != 3 {
		t.Errorf("second Recover = %+v, %v, %d entries", got, err, len(je.entries(t)))
	}
	if r, err := je.log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

// failingAppender fails after n entries, like a crash in the middle of Recover.
type failingAppender struct {
	log *audit.Log
	n   int
}

func (f *failingAppender) AppendTx(ctx context.Context, tx *sql.Tx, e audit.Entry) (int64, error) {
	if f.n == 0 {
		return 0, errors.New("crash")
	}
	f.n--
	return f.log.AppendTx(ctx, tx, e)
}

// Each entry commits with the end of its request: a Recover that stops halfway and runs
// again writes every entry exactly once.
func TestRecoverIsIdempotentAfterACrash(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	je.open(t, idOpen)
	je.open(t, idExecuting)
	if _, err := je.journal.Recover(ctx, &failingAppender{log: je.log, n: 1}); err == nil {
		t.Fatal("crash not reported")
	}
	if n := len(je.entries(t)); n != 1 {
		t.Fatalf("%d entries after the crash", n)
	}
	if got, err := je.journal.Recover(ctx, je.log); err != nil || got != (Recovered{Interrupted: 1}) {
		t.Errorf("Recover = %+v, %v", got, err)
	}
	if n := len(je.entries(t)); n != 2 {
		t.Errorf("%d entries", n)
	}
}

// A row whose stored entry is unreadable or refused by the schema gets a minimal entry
// from its columns (agent, time, resource, action; no evaluation or approval); only if
// even that is refused does it end without one, loudly, so it cannot block the start.
func TestRecoverWritesAMinimalEntryForACorruptRow(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	idBroken := strings.Repeat("4", 32)
	for _, id := range []string{idOpen, idExecuting, idEnded, idBroken} {
		je.open(t, id)
	}
	for _, stmt := range []string{
		`UPDATE approval_journal SET decision = 'garbage' WHERE id = '` + idOpen + `'`,
		`UPDATE approval_journal SET state = 'executing', answered_at = 'never' WHERE id = '` + idExecuting + `'`,
		`UPDATE approval_journal SET decision = '{"agent":{"client_id":""}}' WHERE id = '` + idEnded + `'`, // refused by the schema
		`UPDATE approval_journal SET decision = 'garbage', client_id = '' WHERE id = '` + idBroken + `'`,
	} {
		if _, err := je.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := je.journal.Recover(ctx, je.log); err != nil || got != (Recovered{Interrupted: 3, Unknown: 1}) {
		t.Fatalf("Recover = %+v, %v", got, err)
	}
	entries := je.entries(t)
	if len(entries) != 3 {
		t.Fatalf("%d entries: %v", len(entries), entries)
	}
	for i, want := range []struct{ status, deniedBy, err string }{
		{"denied", "approval", "approval_interrupted"}, {"failed", "", "outcome_unknown"}, {"denied", "approval", "approval_interrupted"},
	} {
		e := entries[i]
		if field(e, "result", "status") != want.status || field(e, "result", "error") != want.err ||
			(want.deniedBy != "" && field(e, "result", "denied_by") != want.deniedBy) || field(e, "approval") != nil ||
			field(e, "evaluation") != nil || field(e, "agent", "client_id") != "hm-client:voice" ||
			field(e, "request", "resource", "entity_id") != "lock.front_door" || field(e, "request", "action") != "unlock" ||
			field(e, "request", "time") != "2026-10-10T12:00:00.000Z" {
			t.Errorf("entry %d = %v", i, e)
		}
	}
	for _, r := range je.rows(t) {
		if r.State != stateEnded || r.Notice == "" {
			t.Errorf("row = %+v", r)
		}
		if r.ID == idBroken && !strings.Contains(r.Result.String, "unrecorded") {
			t.Errorf("unrecordable row = %+v", r)
		}
	}
	if logs := je.logs.String(); !strings.Contains(logs, "recorded with a minimal entry") || !strings.Contains(logs, "cannot be recorded") {
		t.Errorf("log = %s", logs)
	}
	if r, err := je.log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

// After a restart the approvers' notifications are replaced, under the same tag and
// without buttons, in each approver's language, once.
func TestAnnounceReplacesTheNotificationsOnce(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	je.open(t, idOpen)
	je.open(t, idExecuting)
	if err := je.journal.Executing(ctx, idExecuting, audit.Approval{Outcome: OutcomeApproved, By: u1, Via: ViaPush, At: je.now}); err != nil {
		t.Fatal(err)
	}
	if _, err := je.journal.Recover(ctx, je.log); err != nil {
		t.Fatal(err)
	}
	je.notifier.setFail("mobile_app_anna", true)
	if n, err := je.journal.Announce(ctx, je.notifier); err != nil || n != 2 {
		t.Fatalf("Announce = %d, %v", n, err)
	}
	if !strings.Contains(je.logs.String(), "notice after a restart not delivered") {
		t.Errorf("log = %s", je.logs.String())
	}
	sent := []sent{je.notifier.next(t), je.notifier.next(t)}
	if sent[0].service != "mobile_app_markus" || sent[0].n.Tag != tagPrefix+idOpen || len(sent[0].n.Actions) != 0 ||
		sent[0].n.Title != "Freigabe-Anfrage beendet" || !strings.Contains(sent[0].n.Message, "„Front door“") ||
		!strings.Contains(sent[0].n.Message, "nichts ausgeführt") {
		t.Errorf("interrupted notice = %+v", sent[0])
	}
	if sent[1].n.Tag != tagPrefix+idExecuting || sent[1].n.Title != "Bitte prüfen: Front door" || len(sent[1].n.Actions) != 0 {
		t.Errorf("unknown notice = %+v", sent[1])
	}
	if n, err := je.journal.Announce(ctx, je.notifier); err != nil || n != 0 {
		t.Errorf("second Announce = %d, %v", n, err)
	}
}

// A notice that reached no device is owed until it reaches one.
func TestAnnounceKeepsANoticeThatReachedNobody(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	je.open(t, idOpen)
	if _, err := je.journal.Recover(ctx, je.log); err != nil {
		t.Fatal(err)
	}
	je.notifier.setFail("mobile_app_markus", true)
	je.notifier.setFail("mobile_app_anna", true)
	if _, err := je.journal.Announce(ctx, je.notifier); err != nil {
		t.Fatal(err)
	}
	if rows := je.rows(t); rows[0].Notice != noticeInterrupted {
		t.Errorf("notice = %q", rows[0].Notice)
	}
	je.notifier.setFail("mobile_app_anna", false)
	if _, err := je.journal.Announce(ctx, je.notifier); err != nil {
		t.Fatal(err)
	}
	if rows := je.rows(t); rows[0].Notice != "" {
		t.Errorf("notice after delivery = %q", rows[0].Notice)
	}
}

func TestNoticeTexts(t *testing.T) {
	d := decision{Agent: &audit.Agent{DisplayName: "Voice‮ [x](y)"}, Request: &audit.Request{Resource: audit.Resource{EntityID: "lock.front_door"}, Action: "unlock"}}
	n := buildNotice(i18n.EN, noticeInterrupted, d, "hm_request_x")
	if n.Title != "Approval request ended" || !strings.Contains(n.Message, "Voice x y") || !strings.Contains(n.Message, "“lock.front_door”") ||
		!strings.Contains(n.Message, "Nothing was executed") || n.Tag != "hm_request_x" {
		t.Errorf("interrupted = %+v", n)
	}
	n = buildNotice(i18n.EN, noticeUnknown, decision{}, "hm_request_x")
	if n.Title != "Please check: " || !strings.Contains(n.Message, "will not be repeated") {
		t.Errorf("unknown without context = %+v", n)
	}
}

func TestExecutingOnlyForOpenRequests(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	a := audit.Approval{Outcome: OutcomeApproved, By: u1, Via: ViaUI, At: je.now}
	if err := je.journal.Executing(ctx, idOpen, a); !errors.Is(err, ErrJournal) {
		t.Errorf("unknown request: %v", err)
	}
	je.open(t, idOpen)
	if err := je.journal.Executing(ctx, idOpen, a); err != nil {
		t.Fatal(err)
	}
	if err := je.journal.Executing(ctx, idOpen, a); !errors.Is(err, ErrJournal) {
		t.Errorf("twice: %v", err)
	}
	if rows := je.rows(t); rows[0].State != stateExecuting || rows[0].By != u1 || rows[0].Via != ViaUI {
		t.Errorf("row = %+v", rows[0])
	}
	// Ending a request that is not in the journal never loses the entry.
	e := journaled().Record.Entry
	e.Event, e.Result = audit.EventDecision, &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
	e.Also = je.journal.End(strings.Repeat("9", 32), nil, *e.Result)
	if _, err := je.log.Append(ctx, e); err != nil {
		t.Errorf("entry for an unknown request: %v", err)
	}
}

// Ended requests stay a day, then go; opening a request also clears them out.
func TestEndedRequestsAreDeletedAfterADay(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	je.open(t, idOpen)
	je.open(t, idEnded)
	if err := je.journal.inTx(ctx, func(tx *sql.Tx) error {
		return je.journal.end(ctx, tx, idEnded, nil, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, "")
	}); err != nil {
		t.Fatal(err)
	}
	je.now = je.now.Add(keepEnded - time.Second)
	if n, err := je.journal.Expire(ctx); err != nil || n != 0 {
		t.Errorf("within a day: %d, %v", n, err)
	}
	je.now = je.now.Add(2 * time.Second)
	je.open(t, idExecuting)
	if rows := je.rows(t); len(rows) != 2 || rows[0].ID != idOpen || rows[1].ID != idExecuting {
		t.Errorf("rows = %+v", rows) // the open one stays however old
	}
	if n, err := je.journal.Expire(ctx); err != nil || n != 0 {
		t.Errorf("again: %d, %v", n, err)
	}
}

func TestJournalReportsDatabaseErrors(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	je.open(t, idOpen)
	if err := je.db.Close(); err != nil {
		t.Fatal(err)
	}
	o := Opened{ID: idExecuting, Request: journaled()}
	if err := je.journal.Open(ctx, o); !errors.Is(err, ErrJournal) {
		t.Errorf("Open: %v", err)
	}
	if err := je.journal.Executing(ctx, idOpen, audit.Approval{}); !errors.Is(err, ErrJournal) {
		t.Errorf("Executing: %v", err)
	}
	if err := je.journal.Delivered(ctx, idOpen, nil); !errors.Is(err, ErrJournal) {
		t.Errorf("Delivered: %v", err)
	}
	if err := je.journal.EndNow(ctx, idOpen, nil, audit.Result{Status: audit.StatusDenied}); !errors.Is(err, ErrJournal) {
		t.Errorf("EndNow: %v", err)
	}
	if _, err := je.journal.Recover(ctx, je.log); !errors.Is(err, ErrJournal) {
		t.Errorf("Recover: %v", err)
	}
	if _, err := je.journal.Announce(ctx, je.notifier); !errors.Is(err, ErrJournal) {
		t.Errorf("Announce: %v", err)
	}
	if _, err := je.journal.Expire(ctx); !errors.Is(err, ErrJournal) {
		t.Errorf("Expire: %v", err)
	}
}

var _ Clearer = (*ha.Client)(nil)

// After the delivery the journal lists only the devices the request reached, so that a
// notice after a restart goes to no device that never had the request.
func TestJournalListsOnlyTheDevicesReached(t *testing.T) {
	je := newJournalEnv(t)
	je.notifier.setFail("mobile_app_tablet", true)
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	_ = je.notifier.next(t)
	waitOpen := func() bool { return len(je.svc.Open()) == 1 }
	for deadline := time.Now().Add(5 * time.Second); !waitOpen(); {
		if time.Now().After(deadline) {
			t.Fatal("not open")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var notified []Notified
	if err := json.Unmarshal([]byte(je.rows(t)[0].Notified), &notified); err != nil {
		t.Fatal(err)
	}
	want := []Notified{{u1, "mobile_app_markus", "de"}, {u2, "mobile_app_anna", "en"}}
	if !slices.Equal(notified, want) {
		t.Errorf("notified = %+v", notified)
	}
	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u2))
	wait(t, ch)
}

func TestAFailedUpdateOfTheDevicesIsLogged(t *testing.T) {
	je := newJournalEnv(t)
	je.svc.cfg.Journal = staleJournal{je.journal}
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u2))
	if a := wait(t, ch); a.err != nil || a.res.Outcome != OutcomeRejected {
		t.Errorf("result = %+v, %v", a.res, a.err)
	}
	if !strings.Contains(je.logs.String(), "devices reached not recorded in the journal") {
		t.Errorf("log = %s", je.logs.String())
	}
}

// staleJournal cannot record the devices reached.
type staleJournal struct{ *Journal }

func (staleJournal) Delivered(context.Context, string, []Notified) error {
	return errors.New("database is locked")
}

// slowClearer blocks every removal until released.
type slowClearer struct {
	release chan struct{}
	fakeClearer
}

func (s *slowClearer) ClearNotification(ctx context.Context, service, tag string) error {
	<-s.release
	return s.fakeClearer.ClearNotification(ctx, service, tag)
}

// On shutdown the removals of notifications of ended requests are waited for, bounded.
func TestDrainWaitsForRemovalsBounded(t *testing.T) {
	je := newJournalEnv(t)
	slow := &slowClearer{release: make(chan struct{})}
	je.svc.cfg.Clearer = slow
	ch := je.ask(journaled())
	s := je.notifier.next(t)
	je.svc.HandleEvent(event("HM_DENY_"+nonceOf(t, s), u2))
	wait(t, ch)
	if je.svc.Drain(50 * time.Millisecond) {
		t.Error("Drain reported done while removals were pending")
	}
	close(slow.release)
	if !je.svc.Drain(5*time.Second) || len(slow.list()) == 0 {
		t.Errorf("Drain after release: cleared %v", slow.list())
	}
	if !New(Config{}).Drain(time.Millisecond) {
		t.Error("Drain without anything pending")
	}
}

// An agent finds its request by the reference it was given, and only its own; after the
// end the outcome is there, after a day nothing.
func TestLookupByTheAgentsReference(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	o := je.open(t, idOpen)
	ref := o.Request.Record.AgentRef
	got, ok, err := je.journal.Lookup(ctx, ref, "hm-client:voice")
	if err != nil || !ok || got.State != stateOpen || !got.Expires.Equal(o.Expires) {
		t.Fatalf("Lookup = %+v, %v, %v", got, ok, err)
	}
	for name, args := range map[string][2]string{
		"other agent": {ref, "hm-client:other"}, "unknown": {"apr_" + strings.Repeat("0", 32), "hm-client:voice"},
		"request ID": {idOpen, "hm-client:voice"}, "tag": {o.Tag, "hm-client:voice"}, "empty": {"", "hm-client:voice"},
	} {
		if _, ok, err := je.journal.Lookup(ctx, args[0], args[1]); ok || err != nil {
			t.Errorf("%s: found (%v)", name, err)
		}
	}
	result := audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
	e := journaled().Record.Entry
	e.Event, e.Result, e.Approval = audit.EventDecision, &result, &audit.Approval{Outcome: OutcomeRejected, By: u2, Via: ViaUI, At: je.now}
	e.Also = je.journal.End(idOpen, e.Approval, result)
	if _, err := je.log.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, ok, err = je.journal.Lookup(ctx, ref, "hm-client:voice")
	if err != nil || !ok || got.State != stateEnded || got.Outcome != OutcomeRejected || got.Result != result {
		t.Errorf("after the end = %+v, %v, %v", got, ok, err)
	}
	je.now = je.now.Add(keepEnded + time.Minute)
	if _, err := je.journal.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := je.journal.Lookup(ctx, ref, "hm-client:voice"); ok {
		t.Error("found after a day")
	}
}

// EndNow ends a request on its own; Lookup reports what it cannot read as an error,
// never as a request that does not exist.
func TestEndNowAndUnreadableLookups(t *testing.T) {
	je := newJournalEnv(t)
	ctx := context.Background()
	o := je.open(t, idOpen)
	ref := o.Request.Record.AgentRef
	result := audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}
	if err := je.journal.EndNow(ctx, idOpen, &audit.Approval{Outcome: OutcomeTimeout, At: je.now}, result); err != nil {
		t.Fatal(err)
	}
	if st, ok, err := je.journal.Lookup(ctx, ref, "hm-client:voice"); err != nil || !ok || !st.Ended() || st.Outcome != OutcomeTimeout {
		t.Errorf("after EndNow = %+v, %v, %v", st, ok, err)
	}
	for _, stmt := range []string{`UPDATE approval_journal SET result = 'garbage'`, `UPDATE approval_journal SET expires_at = 'never'`} {
		if _, err := je.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := je.journal.Lookup(ctx, ref, "hm-client:voice"); ok || !errors.Is(err, ErrJournal) {
			t.Errorf("%s: %v, %v", stmt, ok, err)
		}
	}
	_ = je.db.Close()
	if _, _, err := je.journal.Lookup(ctx, ref, "hm-client:voice"); !errors.Is(err, ErrJournal) {
		t.Errorf("closed database: %v", err)
	}
}
