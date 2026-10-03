// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

func (h *harness) audit(query string) (wireAuditPage, result) {
	h.t.Helper()
	r := h.do(http.MethodGet, "/api/audit"+query, nil)
	var page wireAuditPage
	if r.code == http.StatusOK {
		r.json(h.t, &page)
	}
	return page, r
}

func seqsOf(p wireAuditPage) []int64 {
	var out []int64
	for _, e := range p.Entries {
		out = append(out, int64(e["seq"].(float64)))
	}
	return out
}

func TestAuditQueryValidation(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct{ query, field string }{
		{"?limit=0", "/limit"}, {"?limit=101", "/limit"}, {"?limit=x", "/limit"}, {"?limit=1&limit=2", "/limit"},
		{"?before=0", "/before"}, {"?before=-1", "/before"}, {"?before=1.5", "/before"},
		{"?since=yesterday", "/since"}, {"?until=2026-13-01T00:00:00Z", "/until"},
		{"?agent=", "/agent"}, {"?agent=" + strings.Repeat("a", 513), "/agent"}, {"?device=", "/device"},
		{"?group=all", "/group"}, {"?event=decision%3B", "/event"}, {"?decision=maybe", "/decision"},
		{"?q=" + strings.Repeat("x", 101), "/q"}, {"?q=%FF", "/q"}, {"?q=a&q=b", "/q"},
		{"?admin=1", ""}, {"?%zz", ""},
	} {
		_, r := h.audit(tc.query)
		if r.code != http.StatusBadRequest || r.errCode() != codeInvalidInput || r.field() != tc.field {
			t.Errorf("%s = %d %s", tc.query, r.code, r.body)
		}
		// The value never comes back in the error.
		if strings.Contains(string(r.body), "xxxxxxxx") || strings.Contains(string(r.body), "maybe") {
			t.Errorf("%s: value in the answer %s", tc.query, r.body)
		}
	}
}

func TestAuditQuery(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	voice := h.admit("Voice") // 1 agent.registered, 2 mandate.created
	m := h.mandateOf(voice.ClientID)
	e := decisionAt(voice.ClientID, "Voice", true)
	e.Mandate = &audit.Mandate{ID: m.ID, Digest: m.Digest}
	_, _ = h.log.Append(ctx, e) // 3
	e2 := decisionAt("hm-client:other", "Garten 50%_off", true)
	e2.Request.Resource = audit.Resource{EntityID: "lock.front_door", Area: "hall"}
	_, _ = h.log.Append(ctx, e2)                                                                // 4
	_, _ = h.agents.SetEmergencyStop(ctx, true, audit.Actor{Kind: audit.ActorUser, ID: annaID}) // 5

	page, _ := h.audit("")
	if page.Total != 5 || len(page.Entries) != 5 || page.NextBefore != nil {
		t.Fatalf("page = %+v", page)
	}
	newest := page.Entries[0]
	if newest["event"] != "emergency_stop.activated" || newest["actor"].(map[string]any)["name"] != "Anna" ||
		!strings.HasPrefix(newest["digest"].(string), "sha256:") || newest["prev"] == nil || newest["type"] != nil || newest["principal"] != nil {
		t.Errorf("newest = %v", newest)
	}
	first := page.Entries[4]
	if first["prev"] != nil || first["seq"].(float64) != 1 {
		t.Errorf("first = %v", first)
	}
	decision := page.Entries[2]
	if decision["mandate"].(map[string]any)["version"].(float64) != 1 {
		t.Errorf("version of the mandate = %v", decision["mandate"])
	}
	for _, tc := range []struct {
		query string
		want  string
		total int
	}{
		{"?limit=2", "5,4", 5},
		{"?limit=2&before=4", "3,2", 5},
		{"?agent=" + url.QueryEscape(voice.ClientID), "3,1", 2},
		{"?device=hall", "4", 1},
		{"?device=light.kitchen", "3", 1},
		{"?group=decision", "4,3", 2},
		{"?group=admin", "5,2,1", 3},
		{"?event=mandate.created", "2", 1},
		{"?decision=default&decision=default", "4,3", 2},
		{"?decision=allow", "", 0},
		{"?q=K%C3%BCchen", "3", 1},       // catalog name: Küchenlicht
		{"?q=flur", "4", 1},              // area name in the catalog
		{"?q=50%25_", "4", 1},            // literal % and _
		{"?q=%E2%80%AE", "5,4,3,2,1", 5}, // cleaned to nothing: no search
		{"?q=voice&group=admin", "1", 1},
		{"?since=" + url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339)), "", 0},
		{"?until=" + url.QueryEscape(time.Now().Add(-24*time.Hour).Format(time.RFC3339)), "", 0},
	} {
		p, r := h.audit(tc.query)
		got := fmtSeqs(seqsOf(p))
		if r.code != http.StatusOK || got != tc.want || p.Total != tc.total {
			t.Errorf("%s = %d %q total %d; want %q %d", tc.query, r.code, got, p.Total, tc.want, tc.total)
		}
	}
	p, _ := h.audit("?limit=2")
	if p.NextBefore == nil || *p.NextBefore != 4 {
		t.Errorf("next_before = %v", p.NextBefore)
	}
}

func fmtSeqs(s []int64) string {
	parts := make([]string, len(s))
	for i, n := range s {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ",")
}

func TestVersionIndex(t *testing.T) {
	h := newHarness(t)
	voice := h.admit("Voice")
	m := h.mandateOf(voice.ClientID)
	v := versionIndex{s: h.srv, byMandate: map[string][]mandate.Version{}}
	if n := v.number(context.Background(), m.ID, m.Digest, time.Now()); n != 1 {
		t.Errorf("number = %d", n)
	}
	// Recorded before the version existed (clock skew): the newest with that digest.
	if n := v.number(context.Background(), m.ID, m.Digest, time.Time{}); n != 1 {
		t.Errorf("number before = %d", n)
	}
	if n := v.number(context.Background(), m.ID, "sha256:none", time.Now()); n != 0 {
		t.Errorf("unknown digest = %d", n)
	}
	if n := v.number(context.Background(), "", m.Digest, time.Now()); n != 0 {
		t.Errorf("no mandate = %d", n)
	}
}
