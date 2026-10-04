// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound means there is no entry with that seq.
var ErrNotFound = errors.New("audit: entry not found")

// queryTimeout bounds every read of the UI, so that a search over a large log can never
// hold the database (TESTING.md section 4, audit log).
const queryTimeout = 5 * time.Second

// Filter selects entries of the audit log for the UI. Every value is passed to SQLite as
// a bound parameter; nothing is put into the SQL text.
type Filter struct {
	// Before returns entries with a smaller seq; 0 means the newest.
	Before int64
	// Limit is the page size, 1 to 100.
	Limit        int
	Since, Until time.Time // zero: open
	Agent        string    // client ID
	Device       string    // exact entity_id or area
	// Group is "decision" (requests) or "admin" (everything else); "" is both.
	Group string
	Event string
	// Decisions are allow, ask, deny and default (reason no_match); none means all.
	Decisions []string
	// Search, if set, is a folded search text (internal/untrusted): an entry matches
	// when its search text contains it, or its entity or area is one of SearchEntities or
	// SearchAreas (devices and areas whose name in the current catalog matches).
	Search         string
	SearchEntities []string
	SearchAreas    []string
}

// Stored is an entry as written: its canonical JSON and digest.
type Stored struct {
	Seq        int64
	RecordedAt time.Time
	Event      string
	Entry      json.RawMessage
	Digest     string
}

// Page is one page of entries, newest first. NextBefore is 0 on the last page; Total
// counts all matches of the filter, regardless of Before.
type Page struct {
	Entries    []Stored
	NextBefore int64
	Total      int
}

// Query returns a page of entries matching f.
func (l *Log) Query(ctx context.Context, f Filter) (Page, error) {
	if f.Limit < 1 || f.Limit > 100 {
		return Page{}, fmt.Errorf("audit: limit %d out of range", f.Limit)
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	where, args, err := f.where()
	if err != nil {
		return Page{}, err
	}
	var page Page
	if err := l.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log a WHERE `+where, args...).Scan(&page.Total); err != nil {
		return Page{}, fmt.Errorf("audit: count: %w", err)
	}
	cursor := where
	if f.Before > 0 {
		cursor += ` AND a.seq < ?`
		args = append(args, f.Before)
	}
	// One more than the page tells whether there is a next page.
	rows, err := l.db.QueryContext(ctx, `SELECT a.seq, a.recorded_at, a.event, a.entry, a.digest FROM audit_log a WHERE `+cursor+
		` ORDER BY a.seq DESC LIMIT ?`, append(args, f.Limit+1)...)
	if err != nil {
		return Page{}, fmt.Errorf("audit: query: %w", err)
	}
	entries, err := scanStored(rows)
	if err != nil {
		return Page{}, err
	}
	if len(entries) > f.Limit {
		entries = entries[:f.Limit]
		page.NextBefore = entries[len(entries)-1].Seq
	}
	page.Entries = entries
	return page, nil
}

// where builds the condition of f with placeholders only.
func (f Filter) where() (string, []any, error) {
	conds := []string{"1"}
	var args []any
	add := func(cond string, values ...any) {
		conds = append(conds, cond)
		args = append(args, values...)
	}
	if !f.Since.IsZero() {
		add(`a.recorded_at >= ?`, f.Since.UTC().Format(timeFormat))
	}
	if !f.Until.IsZero() {
		add(`a.recorded_at < ?`, f.Until.UTC().Format(timeFormat))
	}
	if f.Agent != "" {
		add(`a.client_id = ?`, f.Agent)
	}
	if f.Device != "" {
		add(`(a.entity_id = ? OR a.area = ?)`, f.Device, f.Device)
	}
	switch f.Group {
	case "":
	case "decision":
		add(`a.event = ?`, EventDecision)
	case "admin":
		add(`a.event <> ?`, EventDecision)
	default:
		return "", nil, fmt.Errorf("audit: unknown group %q", f.Group)
	}
	if f.Event != "" {
		add(`a.event = ?`, f.Event)
	}
	if len(f.Decisions) > 0 {
		add(`a.decision IN (SELECT value FROM json_each(?))`, jsonList(f.Decisions))
	}
	if f.Search != "" {
		// json_each instead of one placeholder per name: no limit on SQL variables.
		// instr, not LIKE: % and _ in the text are plain characters.
		add(`(a.seq IN (SELECT seq FROM audit_search WHERE instr(text, ?) > 0)
			OR a.entity_id IN (SELECT value FROM json_each(?)) OR a.area IN (SELECT value FROM json_each(?)))`,
			f.Search, jsonList(f.SearchEntities), jsonList(f.SearchAreas))
	}
	return strings.Join(conds, " AND "), args, nil
}

// jsonList encodes strings as a JSON array; encoding strings cannot fail.
func jsonList(list []string) string {
	if list == nil {
		list = []string{}
	}
	data, _ := json.Marshal(list)
	return string(data)
}

func scanStored(rows *sql.Rows) ([]Stored, error) {
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var s Stored
		var recorded, entry string
		if err := rows.Scan(&s.Seq, &recorded, &s.Event, &entry, &s.Digest); err != nil {
			return nil, fmt.Errorf("audit: read: %w", err)
		}
		s.RecordedAt, _ = time.Parse(timeFormat, recorded)
		s.Entry = json.RawMessage(entry)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: read: %w", err)
	}
	return out, nil
}

// Get returns the entry with seq.
func (l *Log) Get(ctx context.Context, seq int64) (Stored, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT seq, recorded_at, event, entry, digest FROM audit_log WHERE seq = ?`, seq)
	if err != nil {
		return Stored{}, fmt.Errorf("audit: read: %w", err)
	}
	list, err := scanStored(rows)
	if err != nil {
		return Stored{}, err
	}
	if len(list) == 0 {
		return Stored{}, ErrNotFound
	}
	return list[0], nil
}

// LastSeq returns the seq of the newest entry, 0 for an empty log.
func (l *Log) LastSeq(ctx context.Context) (int64, error) {
	var seq sql.NullInt64
	if err := l.db.QueryRowContext(ctx, `SELECT max(seq) FROM audit_log`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("audit: read: %w", err)
	}
	return seq.Int64, nil
}

// After returns the entries after seq, oldest first, at most limit.
func (l *Log) After(ctx context.Context, seq int64, limit int) ([]Stored, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT seq, recorded_at, event, entry, digest FROM audit_log WHERE seq > ? ORDER BY seq LIMIT ?`,
		seq, limit)
	if err != nil {
		return nil, fmt.Errorf("audit: read: %w", err)
	}
	return scanStored(rows)
}

// ApprovalHistory returns the newest decision entries that ended an approval request,
// newest first: those with an approval, and those an emergency stop or a revocation
// ended before an answer (decision F1: no approval, denied by emergency_stop or
// authentication). Asks that never reached a human (no approver, too many pending) are
// not in it.
func (l *Log) ApprovalHistory(ctx context.Context, limit int) ([]Stored, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := l.db.QueryContext(ctx, `SELECT seq, recorded_at, event, entry, digest FROM audit_log
		WHERE event = ? AND json_extract(entry, '$.evaluation.decision') = 'ask'
		  AND (json_extract(entry, '$.approval') IS NOT NULL
		       OR json_extract(entry, '$.result.denied_by') IN (?, ?))
		ORDER BY seq DESC LIMIT ?`, EventDecision, DeniedByEmergencyStop, DeniedByAuthentication, limit)
	if err != nil {
		return nil, fmt.Errorf("audit: approval history: %w", err)
	}
	return scanStored(rows)
}

// Activity is what the log tells about one agent's requests.
type Activity struct {
	LastAt time.Time // zero without any request
	// Since is the number of requests since the given start (the household day).
	Since int
	// Counted is the number of requests since the given hour start that went through
	// the rate limit (those with an evaluation).
	Counted int
}

// Activities returns the activity of every agent with requests in the log: requests
// since dayStart and requests counted against the rate limit since hourStart.
func (l *Log) Activities(ctx context.Context, dayStart, hourStart time.Time) (map[string]Activity, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	day, hour := dayStart.UTC().Format(timeFormat), hourStart.UTC().Format(timeFormat)
	rows, err := l.db.QueryContext(ctx, `SELECT client_id, max(recorded_at),
			sum(recorded_at >= ?), sum(recorded_at >= ? AND decision IS NOT NULL)
		FROM audit_log WHERE event = ? AND client_id IS NOT NULL GROUP BY client_id`, day, hour, EventDecision)
	if err != nil {
		return nil, fmt.Errorf("audit: activity: %w", err)
	}
	defer rows.Close()
	out := map[string]Activity{}
	for rows.Next() {
		var id, last string
		var a Activity
		if err := rows.Scan(&id, &last, &a.Since, &a.Counted); err != nil {
			return nil, fmt.Errorf("audit: activity: %w", err)
		}
		a.LastAt, _ = time.Parse(timeFormat, last)
		out[id] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: activity: %w", err)
	}
	return out, nil
}

// IndexSearch writes the search text of entries that have none, e.g. entries written
// before the search existed. It returns how many it indexed.
func (l *Log) IndexSearch(ctx context.Context) (int, error) {
	n := 0
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT a.seq, a.entry FROM audit_log a
			WHERE NOT EXISTS (SELECT 1 FROM audit_search s WHERE s.seq = a.seq)`)
		if err != nil {
			return fmt.Errorf("audit: index search: %w", err)
		}
		type missing struct {
			seq  int64
			text string
		}
		var list []missing
		for rows.Next() {
			var seq int64
			var entry string
			if err := rows.Scan(&seq, &entry); err != nil {
				_ = rows.Close()
				return fmt.Errorf("audit: index search: %w", err)
			}
			var e struct {
				Agent   *Agent `json:"agent"`
				Request *struct {
					Resource Resource `json:"resource"`
				} `json:"request"`
			}
			_ = json.Unmarshal([]byte(entry), &e) // a stored entry is valid JSON; Verify checks the rest
			var r *Request
			if e.Request != nil {
				r = &Request{Resource: e.Request.Resource}
			}
			list = append(list, missing{seq, searchText(e.Agent, r)})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("audit: index search: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("audit: index search: %w", err)
		}
		for _, m := range list {
			if _, err := tx.ExecContext(ctx, `INSERT INTO audit_search (seq, text) VALUES (?, ?)`, m.seq, m.text); err != nil {
				return fmt.Errorf("audit: index search: %w", err)
			}
		}
		n = len(list)
		return nil
	})
	return n, err
}

// RequestsSince returns, per agent, when its requests since a point in time reached the
// evaluation: the decision entries without the refusals of the rate limit and the
// emergency stop. After a restart the rate limiter is filled with them, so that a crash
// does not hand every agent a fresh limit (SPEC-v0 section 11.2). Requests that leave
// no entry of their own, such as listing devices, are not included.
func (l *Log) RequestsSince(ctx context.Context, since time.Time) (map[string][]time.Time, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT json_extract(entry, '$.agent.client_id'), recorded_at FROM audit_log
		WHERE event = ? AND recorded_at >= ?
		AND coalesce(json_extract(entry, '$.result.denied_by'), '') NOT IN (?, ?) ORDER BY seq`,
		EventDecision, since.UTC().Format(timeFormat), DeniedByRateLimit, DeniedByEmergencyStop)
	if err != nil {
		return nil, fmt.Errorf("audit: read: %w", err)
	}
	defer rows.Close()
	out := map[string][]time.Time{}
	for rows.Next() {
		var clientID sql.NullString
		var recordedAt string
		if err := rows.Scan(&clientID, &recordedAt); err != nil {
			return nil, fmt.Errorf("audit: read: %w", err)
		}
		at, err := time.Parse(timeFormat, recordedAt)
		if err != nil || !clientID.Valid {
			continue
		}
		out[clientID.String] = append(out[clientID.String], at)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: read: %w", err)
	}
	return out, nil
}
