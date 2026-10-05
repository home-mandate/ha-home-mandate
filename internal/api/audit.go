// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/untrusted"
)

const (
	defaultPage = 50
	maxFilterID = 512
)

var (
	auditEvents = []string{audit.EventDecision, audit.EventMandateCreated, audit.EventMandateUpdated, audit.EventMandateRevoked,
		audit.EventAgentRegistered, audit.EventAgentRevoked, audit.EventEmergencyStopActivated, audit.EventEmergencyStopReleased,
		audit.EventAuthRejected, audit.EventLogTruncated, audit.EventLogCheckpoint, audit.EventDirectoryChanged}
	auditDecisions = []string{"allow", "ask", "deny", "default"}
	// queryKeys are the parameters of GET api/audit; every one but decision at most once.
	queryKeys = []string{"before", "limit", "since", "until", "agent", "device", "q", "group", "event", "decision"}
)

type wireAuditPage struct {
	Entries    []map[string]any `json:"entries"`
	NextBefore *int64           `json:"next_before"`
	Total      int              `json:"total"`
}

// auditFilter reads the query of GET api/audit. Anything unknown, repeated or malformed
// is invalid_input naming only the parameter, never its value.
func (s *Server) auditFilter(raw string) (audit.Filter, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return audit.Filter{}, fail(codeInvalidInput)
	}
	f := audit.Filter{Limit: defaultPage}
	for key, values := range q {
		if !slices.Contains(queryKeys, key) {
			return audit.Filter{}, fail(codeInvalidInput)
		}
		if key != "decision" && len(values) != 1 {
			return audit.Filter{}, failField(codeInvalidInput, "/"+key)
		}
	}
	for _, step := range []func(url.Values, *audit.Filter) error{parsePage, parsePeriod, parseScope, s.parseSearch} {
		if err := step(q, &f); err != nil {
			return audit.Filter{}, err
		}
	}
	return f, nil
}

func bad(key string) error { return failField(codeInvalidInput, "/"+key) }

// parsePage reads before and limit.
func parsePage(q url.Values, f *audit.Filter) error {
	if q.Has("before") {
		n, err := strconv.ParseInt(q.Get("before"), 10, 64)
		if err != nil || n < 1 {
			return bad("before")
		}
		f.Before = n
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 100 {
			return bad("limit")
		}
		f.Limit = n
	}
	return nil
}

// parsePeriod reads since and until.
func parsePeriod(q url.Values, f *audit.Filter) error {
	for _, key := range []string{"since", "until"} {
		if !q.Has(key) {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, q.Get(key))
		if err != nil {
			return bad(key)
		}
		if key == "since" {
			f.Since = t
		} else {
			f.Until = t
		}
	}
	return nil
}

// parseScope reads agent, device, group, event and decision.
func parseScope(q url.Values, f *audit.Filter) error {
	for _, key := range []string{"agent", "device"} {
		if v := q.Get(key); q.Has(key) && (v == "" || len(v) > maxFilterID) {
			return bad(key)
		}
	}
	f.Agent, f.Device = q.Get("agent"), q.Get("device")
	switch g := q.Get("group"); g {
	case "", "decision", "admin":
		f.Group = g
	default:
		return bad("group")
	}
	if e := q.Get("event"); e != "" && !slices.Contains(auditEvents, e) {
		return bad("event")
	}
	f.Event = q.Get("event")
	for _, d := range q["decision"] {
		if !slices.Contains(auditDecisions, d) {
			return bad("decision")
		}
		if !slices.Contains(f.Decisions, d) {
			f.Decisions = append(f.Decisions, d)
		}
	}
	return nil
}

// parseSearch reads q: cleaned like the UI does, folded, with the catalog's matches.
func (s *Server) parseSearch(q url.Values, f *audit.Filter) error {
	if !q.Has("q") {
		return nil
	}
	clean, ok := untrusted.CleanSearch(q.Get("q"))
	if !ok {
		return bad("q")
	}
	if clean != "" {
		f.Search = untrusted.Fold(clean)
		f.SearchEntities, f.SearchAreas = s.catalogMatches(f.Search)
	}
	return nil
}

// catalogMatches returns the devices and areas whose name, as the UI shows it, contains
// the folded search text.
func (s *Server) catalogMatches(needle string) (entities, areas []string) {
	has := func(name string) bool {
		return strings.Contains(untrusted.Fold(untrusted.Clean(name, untrusted.Max)), needle)
	}
	for _, d := range s.cfg.Catalog.All() {
		if has(d.Name()) {
			entities = append(entities, d.EntityID)
		}
	}
	for _, a := range s.cfg.Catalog.Areas() {
		if has(a.Name) {
			areas = append(areas, a.ID)
		}
	}
	return entities, areas
}

func (s *Server) getAudit(r *request) (any, error) {
	f, err := s.auditFilter(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	page, err := s.cfg.Log.Query(r.Context(), f)
	if err != nil {
		return nil, err
	}
	out := wireAuditPage{Entries: make([]map[string]any, 0, len(page.Entries)), Total: page.Total}
	if page.NextBefore > 0 {
		out.NextBefore = &page.NextBefore
	}
	versions := versionIndex{s: s, byMandate: map[string][]mandate.Version{}}
	for _, e := range page.Entries {
		entry, err := s.presentEntry(r.Context(), e, &versions)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

// presentEntry is a stored entry as the UI shows it: with its digest, the names of the
// people in it and the number of the mandate version (decision F4). These additions are
// not part of the entry and its chain; type and principal are left out.
func (s *Server) presentEntry(ctx context.Context, e audit.Stored, versions *versionIndex) (map[string]any, error) {
	var entry map[string]any
	if err := json.Unmarshal(e.Entry, &entry); err != nil {
		return nil, err
	}
	delete(entry, "type")
	delete(entry, "principal")
	entry["digest"] = e.Digest
	if _, ok := entry["prev"]; !ok {
		entry["prev"] = nil
	}
	if actor, ok := entry["actor"].(map[string]any); ok {
		if id, _ := actor["id"].(string); id != "" {
			if name := s.users.name(ctx, id); name != nil {
				actor["name"] = *name
			}
		}
	}
	if appr, ok := entry["approval"].(map[string]any); ok {
		if by, _ := appr["by"].(string); by != "" {
			if name := s.users.name(ctx, by); name != nil {
				appr["by_name"] = *name
			}
		}
	}
	if m, ok := entry["mandate"].(map[string]any); ok {
		id, _ := m["id"].(string)
		digest, _ := m["digest"].(string)
		if n := versions.number(ctx, id, digest, e.RecordedAt); n > 0 {
			m["version"] = n
		}
	}
	return entry, nil
}

// versionIndex finds the number of the mandate version an entry refers to: the newest
// version with that digest stored no later than the entry (a digest repeats when a
// version restores an earlier one).
type versionIndex struct {
	s         *Server
	byMandate map[string][]mandate.Version
}

func (v *versionIndex) number(ctx context.Context, id, digest string, at time.Time) int {
	if id == "" || digest == "" {
		return 0
	}
	list, ok := v.byMandate[id]
	if !ok {
		list, _ = v.s.cfg.Mandates.Versions(ctx, id) // a mandate without versions has no number
		v.byMandate[id] = list
	}
	found := 0
	for _, version := range list {
		if version.Digest == digest && !version.CreatedAt.After(at) {
			found = version.Number
		}
	}
	if found == 0 {
		for _, version := range list {
			if version.Digest == digest {
				found = version.Number
			}
		}
	}
	return found
}
