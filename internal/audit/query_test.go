// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/untrusted"
)

// clocked returns a log whose clock moves one minute per entry from start.
func clocked(t *testing.T, start time.Time) (*audit.Log, func() time.Time) {
	t.Helper()
	l, _ := newLog(t)
	var mu sync.Mutex
	now := start
	l.SetClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(time.Minute)
		return now
	})
	return l, func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
}

func decision(client, name, entity, area, decision, reason string) audit.Entry {
	return audit.Entry{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: client, DisplayName: name},
		Request:    &audit.Request{Time: time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC), Resource: audit.Resource{EntityID: entity, Area: area}, Action: "turn_on"},
		Evaluation: &audit.Evaluation{Decision: decision, Reason: reason},
		Result:     &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}}
}

var start = time.Date(2026, 10, 3, 6, 0, 0, 0, time.UTC)

// fixture: seq 1–8.
func queryFixture(t *testing.T) *audit.Log {
	t.Helper()
	l, _ := clocked(t, start)
	user := &audit.Actor{Kind: audit.ActorUser, ID: "u1"}
	appendAll(t, l, []audit.Entry{
		{Event: audit.EventAgentRegistered, Actor: user, Agent: &audit.Agent{ClientID: "hm-client:voice-1", DisplayName: "Voice"}}, // 1
		decision("hm-client:voice-1", "Voice", "light.kitchen", "kitchen", "allow", "rule"),                                        // 2
		decision("hm-client:voice-1", "Voice", "lock.front_door", "hall", "ask", "rule"),                                           // 3
		decision("hm-client:n8n-2", "n8n 50%_off", "light.garden", "garden", "deny", "no_match"),                                   // 4
		decision("hm-client:n8n-2", "n8n 50%_off", "switch.pump", "", "deny", "rule"),                                              // 5
		{Event: audit.EventEmergencyStopActivated, Actor: user},                                                                    // 6
		decision("hm-client:voice-1", "Straße", "cover.garage", "garage", "deny", "no_match"),                                      // 7
		{Event: audit.EventAgentRevoked, Actor: user, Agent: &audit.Agent{ClientID: "hm-client:n8n-2", DisplayName: "n8n"}},        // 8
	})
	return l
}

func seqs(p audit.Page) []int64 {
	var out []int64
	for _, e := range p.Entries {
		out = append(out, e.Seq)
	}
	return out
}

func TestQueryFilters(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	tests := []struct {
		name  string
		f     audit.Filter
		want  []int64
		total int
	}{
		{"all", audit.Filter{Limit: 100}, []int64{8, 7, 6, 5, 4, 3, 2, 1}, 8},
		{"agent", audit.Filter{Limit: 100, Agent: "hm-client:n8n-2"}, []int64{8, 5, 4}, 3},
		{"device entity", audit.Filter{Limit: 100, Device: "lock.front_door"}, []int64{3}, 1},
		{"device area", audit.Filter{Limit: 100, Device: "garden"}, []int64{4}, 1},
		{"group decision", audit.Filter{Limit: 100, Group: "decision"}, []int64{7, 5, 4, 3, 2}, 5},
		{"group admin", audit.Filter{Limit: 100, Group: "admin"}, []int64{8, 6, 1}, 3},
		{"event", audit.Filter{Limit: 100, Event: audit.EventEmergencyStopActivated}, []int64{6}, 1},
		{"decision default", audit.Filter{Limit: 100, Decisions: []string{"default"}}, []int64{7, 4}, 2},
		{"decision deny is not default", audit.Filter{Limit: 100, Decisions: []string{"deny"}}, []int64{5}, 1},
		{"decisions", audit.Filter{Limit: 100, Decisions: []string{"allow", "ask"}}, []int64{3, 2}, 2},
		{"since until", audit.Filter{Limit: 100, Since: start.Add(3 * time.Minute), Until: start.Add(6 * time.Minute)}, []int64{5, 4, 3}, 3},
		{"page", audit.Filter{Limit: 3}, []int64{8, 7, 6}, 8},
		{"before", audit.Filter{Limit: 3, Before: 6}, []int64{5, 4, 3}, 8},
		{"before past start", audit.Filter{Limit: 3, Before: 1}, nil, 8},
		{"combined", audit.Filter{Limit: 100, Agent: "hm-client:voice-1", Group: "decision", Decisions: []string{"default", "ask"}}, []int64{7, 3}, 2},
	}
	for _, tc := range tests {
		p, err := l.Query(ctx, tc.f)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(seqs(p), tc.want) || p.Total != tc.total {
			t.Errorf("%s: seqs %v total %d, want %v %d", tc.name, seqs(p), p.Total, tc.want, tc.total)
		}
	}
}

func TestQueryPagination(t *testing.T) {
	l := queryFixture(t)
	var got []int64
	before := int64(0)
	for range 10 {
		p, err := l.Query(context.Background(), audit.Filter{Limit: 3, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, seqs(p)...)
		if p.NextBefore == 0 {
			break
		}
		before = p.NextBefore
	}
	if !slices.Equal(got, []int64{8, 7, 6, 5, 4, 3, 2, 1}) {
		t.Errorf("pages = %v", got)
	}
	// A page that ends exactly at the last entry has no next page.
	p, _ := l.Query(context.Background(), audit.Filter{Limit: 8})
	if p.NextBefore != 0 || len(p.Entries) != 8 {
		t.Errorf("full page: next %d, %d entries", p.NextBefore, len(p.Entries))
	}
}

func search(t *testing.T, l *audit.Log, q string, entities, areas []string) []int64 {
	t.Helper()
	clean, ok := untrusted.CleanSearch(q)
	if !ok {
		t.Fatalf("CleanSearch(%q) refused", q)
	}
	p, err := l.Query(context.Background(), audit.Filter{Limit: 100, Search: untrusted.Fold(clean), SearchEntities: entities, SearchAreas: areas})
	if err != nil {
		t.Fatal(err)
	}
	return seqs(p)
}

func TestQuerySearch(t *testing.T) {
	l := queryFixture(t)
	tests := []struct {
		q              string
		entities, area []string
		want           []int64
	}{
		{"KITCHEN", nil, nil, []int64{2}},                      // entity and area, case folded
		{"front_door", nil, nil, []int64{3}},                   // _ is a plain character
		{"50%", nil, nil, []int64{5, 4}},                       // % is a plain character, agent name
		{"%", nil, nil, []int64{5, 4}},                         // no wildcard
		{"_", nil, nil, []int64{5, 4, 3}},                      // an underscore in an entity ID or agent name only
		{"n8n-2", nil, nil, []int64{8, 5, 4}},                  // client ID, also admin entries
		{"strasse", nil, nil, []int64{7}},                      // ß folds to ss
		{"STRAẞE", nil, nil, []int64{7}},                       // capital sharp s
		{"\u202e", nil, nil, []int64{8, 7, 6, 5, 4, 3, 2, 1}},  // cleaned to empty: no search
		{"pump", []string{"light.garden"}, nil, []int64{5, 4}}, // catalog name matched in memory
		{"zzz", nil, []string{"hall"}, []int64{3}},             // area name matched in memory
		{"zzz", nil, nil, nil},
		{"'; DROP TABLE audit_log; --", nil, nil, nil},
		{"\\", nil, nil, nil},
	}
	for _, tc := range tests {
		clean, _ := untrusted.CleanSearch(tc.q)
		if clean == "" {
			p, _ := l.Query(context.Background(), audit.Filter{Limit: 100})
			if got := seqs(p); !slices.Equal(got, tc.want) {
				t.Errorf("q=%q: %v, want %v", tc.q, got, tc.want)
			}
			continue
		}
		if got := search(t, l, tc.q, tc.entities, tc.area); !slices.Equal(got, tc.want) {
			t.Errorf("q=%q: %v, want %v", tc.q, got, tc.want)
		}
	}
	if p, err := l.Query(context.Background(), audit.Filter{Limit: 1}); err != nil || p.Total != 8 {
		t.Errorf("the log survived the search: %v %v", p, err)
	}
}

// The agent name in the search is the one in the entry, cleaned as the UI shows it: a
// hidden character cannot hide a name from the search, nor smuggle in another.
func TestSearchUsesTheNameOfTheEntry(t *testing.T) {
	l := queryFixture(t)
	if got := search(t, l, "Straße", nil, nil); !slices.Equal(got, []int64{7}) {
		t.Errorf("Straße: %v", got)
	}
	// Entry 7 carries another name, but its client ID still matches.
	if got := search(t, l, "Voice", nil, nil); !slices.Equal(got, []int64{7, 3, 2, 1}) {
		t.Errorf("Voice: %v", got)
	}
	if got := search(t, l, "Voice\u00a0", nil, nil); !slices.Equal(got, []int64{7, 3, 2, 1}) {
		t.Errorf("Voice with a trailing space: %v", got)
	}
}

func TestQueryRejectsBadFilters(t *testing.T) {
	l := queryFixture(t)
	for _, f := range []audit.Filter{{Limit: 0}, {Limit: 101}, {Limit: 10, Group: "other"}} {
		if _, err := l.Query(context.Background(), f); err == nil {
			t.Errorf("Query(%+v) succeeded", f)
		}
	}
}

func TestQueryHonoursTheContext(t *testing.T) {
	l := queryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Query(ctx, audit.Filter{Limit: 10}); err == nil {
		t.Error("Query with a cancelled context succeeded")
	}
}

// Many names from the catalog travel as one JSON parameter: no SQL variable limit
// (32766), and the search finishes well within the query timeout.
func TestSearchWithManyCatalogMatches(t *testing.T) {
	l := queryFixture(t)
	n := 40000
	if raceDetector {
		n = 4000
	}
	entities := make([]string, n)
	for i := range entities {
		entities[i] = fmt.Sprintf("light.l%d", i)
	}
	entities = append(entities, "switch.pump")
	start := time.Now()
	if got := search(t, l, "q", entities, []string{"attic"}); !slices.Equal(got, []int64{5}) {
		t.Errorf("got %v", got)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("search took %s", took)
	}
}

func TestGetAndLastSeqAndAfter(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	e, err := l.Get(ctx, 3)
	if err != nil || e.Seq != 3 || e.Event != audit.EventDecision || !strings.HasPrefix(e.Digest, "sha256:") ||
		!json.Valid(e.Entry) || e.RecordedAt.IsZero() {
		t.Errorf("Get(3) = %+v, %v", e, err)
	}
	if _, err := l.Get(ctx, 99); !errors.Is(err, audit.ErrNotFound) {
		t.Errorf("Get(99) = %v", err)
	}
	if n, err := l.LastSeq(ctx); err != nil || n != 8 {
		t.Errorf("LastSeq = %d, %v", n, err)
	}
	after, err := l.After(ctx, 6, 10)
	if err != nil || len(after) != 2 || after[0].Seq != 7 || after[1].Seq != 8 {
		t.Errorf("After(6) = %v, %v", after, err)
	}
	empty, _ := newLog(t)
	if n, err := empty.LastSeq(ctx); err != nil || n != 0 {
		t.Errorf("empty LastSeq = %d, %v", n, err)
	}
}

func TestApprovalHistory(t *testing.T) {
	l, _ := clocked(t, start)
	ask := func(result audit.Result, appr *audit.Approval) audit.Entry {
		e := decision("hm-client:voice-1", "Voice", "lock.front_door", "hall", "ask", "rule")
		e.Result, e.Approval = &result, appr
		e.Mandate = &audit.Mandate{ID: "m-voice", Digest: "sha256:" + strings.Repeat("a", 64)}
		return e
	}
	at := start.Add(time.Hour)
	appendAll(t, l, []audit.Entry{
		ask(audit.Result{Status: audit.StatusExecuted}, &audit.Approval{Outcome: "approved", By: "u1", Via: "ui", At: at}),                     // 1
		ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, nil),                                                   // 2: no approver
		ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}, nil),                                              // 3: F1
		ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication}, nil),                                             // 4: F1
		decision("hm-client:voice-1", "Voice", "light.kitchen", "kitchen", "allow", "rule"),                                                    // 5
		ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, &audit.Approval{Outcome: "timeout", At: at}),           // 6
		ask(audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}, &audit.Approval{Outcome: "approved", By: "u1", At: at}), // 7
	})
	list, err := l.ApprovalHistory(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, e := range list {
		got = append(got, e.Seq)
	}
	if !slices.Equal(got, []int64{7, 6, 4, 3, 1}) {
		t.Errorf("history = %v", got)
	}
	if list, _ := l.ApprovalHistory(context.Background(), 2); len(list) != 2 {
		t.Errorf("limit: %d", len(list))
	}
}

func TestActivities(t *testing.T) {
	l, _ := clocked(t, start) // entries at 06:01, 06:02, …
	appendAll(t, l, []audit.Entry{
		decision("a", "A", "light.x", "", "allow", "rule"), // 06:01
		decision("a", "A", "light.x", "", "allow", "rule"), // 06:02
		{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: "a"}, Request: &audit.Request{Time: start, Resource: audit.Resource{EntityID: "light.x"}, Action: "turn_on"},
			Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByRateLimit}}, // 06:03, no evaluation
		decision("b", "B", "light.x", "", "deny", "no_match"),                                                            // 06:04
		{Event: audit.EventAgentRevoked, Actor: &audit.Actor{Kind: "user", ID: "u"}, Agent: &audit.Agent{ClientID: "c"}}, // 06:05, no request
	})
	got, err := l.Activities(context.Background(), start.Add(2*time.Minute), start.Add(time.Minute+30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	a, b := got["a"], got["b"]
	if len(got) != 2 || a.Since != 2 || a.Counted != 1 || !a.LastAt.Equal(start.Add(3*time.Minute)) ||
		b.Since != 1 || b.Counted != 1 || !b.LastAt.Equal(start.Add(4*time.Minute)) {
		t.Errorf("activities = %+v", got)
	}
}

func TestIndexSearchBackfills(t *testing.T) {
	l, db := newLog(t)
	appendAll(t, l, []audit.Entry{decision("hm-client:x", "Wohnzimmer-Bot", "light.living", "living", "allow", "rule")})
	if _, err := db.Exec(`DELETE FROM audit_search`); err != nil {
		t.Fatal(err)
	}
	if got := search(t, l, "wohnzimmer", nil, nil); got != nil {
		t.Fatalf("found without index: %v", got)
	}
	n, err := l.IndexSearch(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("IndexSearch = %d, %v", n, err)
	}
	if got := search(t, l, "wohnzimmer", nil, nil); !slices.Equal(got, []int64{1}) {
		t.Errorf("after backfill: %v", got)
	}
	if n, _ := l.IndexSearch(context.Background()); n != 0 {
		t.Errorf("second backfill indexed %d", n)
	}
}

// The search index goes with the entries: truncating the log deletes their rows too.
func TestTruncateDeletesSearchRows(t *testing.T) {
	l, db := clocked(t, start)
	appendAll(t, l, []audit.Entry{
		decision("a", "A", "light.x", "", "allow", "rule"),
		decision("a", "A", "light.y", "", "allow", "rule"),
		decision("a", "A", "light.z", "", "allow", "rule"),
	})
	if _, err := l.Truncate(context.Background(), start.Add(3*time.Minute), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil {
		t.Fatal(err)
	}
	_ = db
	if got := search(t, l, "light", nil, nil); !slices.Equal(got, []int64{3}) {
		t.Errorf("after truncate: %v", got)
	}
}

func TestOnCommit(t *testing.T) {
	l, _ := newLog(t)
	var mu sync.Mutex
	var got []string
	l.OnCommit(func(seq int64, e audit.Entry) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, fmt.Sprintf("%d:%s:%s", seq, e.Event, e.ApprovalID))
	})
	e := decision("a", "A", "light.x", "", "ask", "rule")
	e.ApprovalID = "req-1"
	if _, err := l.Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := l.WithEntry(context.Background(), e, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := l.WithEntry(context.Background(), e, func() error { return errors.New("failed") }); err == nil {
		t.Fatal("failed action committed")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, []string{"1:decision:req-1", "2:decision:req-1"}) {
		t.Errorf("hook calls = %v", got)
	}
	// The ID never reaches the stored entry.
	stored, _ := l.Get(context.Background(), 1)
	if strings.Contains(string(stored.Entry), "req-1") {
		t.Errorf("approval ID stored: %s", stored.Entry)
	}
}

func TestCheckCounts(t *testing.T) {
	l := queryFixture(t)
	r, n, err := l.Check(context.Background())
	if err != nil || !r.Valid || n != 8 {
		t.Errorf("Check = %+v, %d, %v", r, n, err)
	}
}

// Every read reports a database failure instead of an empty result.
func TestReadsReportDatabaseErrors(t *testing.T) {
	l, db := newLog(t)
	appendAll(t, l, []audit.Entry{decision("a", "A", "light.x", "", "allow", "rule")})
	_ = db.Close()
	ctx := context.Background()
	if _, err := l.Query(ctx, audit.Filter{Limit: 10}); err == nil {
		t.Error("Query succeeded")
	}
	if _, err := l.Get(ctx, 1); err == nil || errors.Is(err, audit.ErrNotFound) {
		t.Errorf("Get = %v", err)
	}
	if _, err := l.LastSeq(ctx); err == nil {
		t.Error("LastSeq succeeded")
	}
	if _, err := l.After(ctx, 0, 1); err == nil {
		t.Error("After succeeded")
	}
	if _, err := l.ApprovalHistory(ctx, 1); err == nil {
		t.Error("ApprovalHistory succeeded")
	}
	if _, err := l.Activities(ctx, start, start); err == nil {
		t.Error("Activities succeeded")
	}
	if _, err := l.IndexSearch(ctx); err == nil {
		t.Error("IndexSearch succeeded")
	}
	if _, _, err := l.Check(ctx); err == nil {
		t.Error("Check succeeded")
	}
}

// An entry whose search text cannot be written is not written at all.
func TestSearchRowFailureRollsBackTheEntry(t *testing.T) {
	l, db := newLog(t)
	if _, err := db.Exec(`CREATE TRIGGER no_search BEFORE INSERT ON audit_search BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(context.Background(), decision("a", "A", "light.x", "", "allow", "rule")); err == nil {
		t.Fatal("Append succeeded")
	}
	if n, _ := l.LastSeq(context.Background()); n != 0 {
		t.Errorf("entry %d written without its search text", n)
	}
}

func TestIndexSearchErrors(t *testing.T) {
	l, db := newLog(t)
	appendAll(t, l, []audit.Entry{decision("a", "A", "light.x", "", "allow", "rule")})
	for _, stmt := range []string{`DELETE FROM audit_search`,
		`CREATE TRIGGER no_search BEFORE INSERT ON audit_search BEGIN SELECT RAISE(ABORT, 'refused'); END`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.IndexSearch(context.Background()); err == nil {
		t.Error("IndexSearch succeeded with a failing insert")
	}
	if _, err := db.Exec(`DROP TABLE audit_search`); err != nil {
		t.Fatal(err)
	}
	if _, err := l.IndexSearch(context.Background()); err == nil {
		t.Error("IndexSearch succeeded without its table")
	}
	if _, err := l.Query(context.Background(), audit.Filter{Limit: 1, Search: "x"}); err == nil {
		t.Error("search succeeded without its table")
	}
}

func TestActionErrorText(t *testing.T) {
	err := &audit.ActionError{Err: errors.New("boom")}
	if err.Error() != "audit: action failed: boom" || !errors.Is(err, err.Err) {
		t.Errorf("ActionError = %q", err.Error())
	}
}

// Rows that cannot be read (here: a view with NULLs in place of the table, as a damaged
// database could deliver) are an error, never a silently shorter result.
func TestUnreadableRowsAreErrors(t *testing.T) {
	l, db := newLog(t)
	appendAll(t, l, []audit.Entry{decision("a", "A", "light.x", "", "allow", "rule")})
	for _, stmt := range []string{
		`DELETE FROM audit_search`,
		`ALTER TABLE audit_log RENAME TO audit_log_real`,
		`CREATE VIEW audit_log AS SELECT seq, NULL AS recorded_at, event, NULL AS entry, digest, client_id, entity_id, area, decision FROM audit_log_real`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	ctx := context.Background()
	if _, err := l.Query(ctx, audit.Filter{Limit: 10}); err == nil {
		t.Error("Query succeeded")
	}
	if _, err := l.Get(ctx, 1); err == nil || errors.Is(err, audit.ErrNotFound) {
		t.Errorf("Get = %v", err)
	}
	if _, err := l.Activities(ctx, start, start); err == nil {
		t.Error("Activities succeeded")
	}
	if _, err := l.IndexSearch(ctx); err == nil {
		t.Error("IndexSearch succeeded")
	}
}

// RequestsSince feeds the rate limiter after a restart (SPEC-v0 section 11.2): every
// request that reached the evaluation counts, refusals by the rate limit or the
// emergency stop do not.
func TestRequestsSince(t *testing.T) {
	l, now := clocked(t, start)
	appendAll(t, l, []audit.Entry{
		decision("hm-client:voice-1", "Voice", "light.kitchen", "kitchen", "allow", "rule"),
		decision("hm-client:n8n-2", "n8n", "light.garden", "garden", "deny", "no_match"),
		{Event: audit.EventEmergencyStopActivated, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "u1"}},
		{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: "hm-client:voice-1"},
			Request: &audit.Request{Time: start, Resource: audit.Resource{EntityID: "light.kitchen"}, Action: "turn_on"},
			Result:  &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByRateLimit}},
		{Event: audit.EventDecision, Agent: &audit.Agent{ClientID: "hm-client:voice-1"},
			Request: &audit.Request{Time: start, Resource: audit.Resource{EntityID: "light.kitchen"}, Action: "turn_on"},
			Result:  &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}},
		decision("hm-client:voice-1", "Voice", "lock.front_door", "hall", "ask", "rule"),
	})
	got, err := l.RequestsSince(context.Background(), start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got["hm-client:voice-1"]) != 2 || len(got["hm-client:n8n-2"]) != 1 {
		t.Fatalf("RequestsSince = %v", got)
	}
	if first := got["hm-client:voice-1"][0]; first.Before(start) || first.After(now()) {
		t.Errorf("time %v outside the log", first)
	}
	later, err := l.RequestsSince(context.Background(), now().Add(time.Hour))
	if err != nil || len(later) != 0 {
		t.Errorf("RequestsSince after the last entry = %v, %v", later, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.RequestsSince(ctx, start); err == nil {
		t.Error("RequestsSince with a cancelled context succeeded")
	}
}

// Only a human or the system deletes old entries, never an agent (SPEC-v0 section 9.1).
func TestTruncateRefusesAnAgentAsActor(t *testing.T) {
	l := queryFixture(t)
	ctx := context.Background()
	before, _ := l.LastSeq(ctx)
	removed, err := l.Truncate(ctx, start.Add(24*time.Hour), audit.Actor{Kind: audit.ActorAgent, ID: "hm-client:voice-1"})
	if !errors.Is(err, audit.ErrInvalidEntry) || removed != 0 {
		t.Fatalf("Truncate by an agent = %d, %v; want ErrInvalidEntry", removed, err)
	}
	if after, _ := l.LastSeq(ctx); after != before {
		t.Errorf("last seq changed from %d to %d", before, after)
	}
	if r, err := l.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("log after the refused truncation: %+v, %v", r, err)
	}
	if removed, err := l.Truncate(ctx, start.Add(24*time.Hour), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil || removed == 0 {
		t.Errorf("Truncate by the system = %d, %v", removed, err)
	}
}
