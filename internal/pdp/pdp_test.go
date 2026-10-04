// SPDX-License-Identifier: AGPL-3.0-or-later

package pdp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mandatespec "github.com/mandate-spec/mandate-spec"
	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

// fakeMandates returns the same candidates for every agent.
type fakeMandates struct {
	candidates []mandate.Candidate
	err        error
}

func (f fakeMandates) Candidates(context.Context, string) ([]mandate.Candidate, error) {
	return f.candidates, f.err
}

// stored is a candidate of the document with its digest (empty if it does not parse).
func stored(doc []byte, info mandate.Info) mandate.Candidate {
	if m, err := evaluator.Parse(doc); err == nil && info.Digest == "" {
		info.Digest = m.Digest()
	}
	return mandate.Candidate{Info: info, Stored: evaluator.NewStored(doc, evaluator.StatusActive)}
}

type fakeCatalog map[string]catalog.Device

func (f fakeCatalog) Lookup(id string) (catalog.Device, bool) {
	d, ok := f[id]
	return d, ok
}

type conformanceCase struct {
	ID            string          `json:"id"`
	Mandate       string          `json:"mandate"`
	MandateInline json.RawMessage `json:"mandate_inline"`
	RawResource   struct {
		EntityID string `json:"entity_id"`
		Category string `json:"category"`
		Area     string `json:"area"`
		Critical bool   `json:"critical"`
	} `json:"resource"`
	Action          string                 `json:"action"`
	Parameters      map[string]json.Number `json:"parameters"`
	Time            string                 `json:"time"`
	Timezone        string                 `json:"timezone"`
	Revoked         bool                   `json:"revoked"`
	Expected        string                 `json:"expected"`
	Reason          string                 `json:"reason"`
	RuleID          *string                `json:"rule_id"`
	ApprovalTimeout string                 `json:"approval_timeout"`
	Why             string                 `json:"why"`
}

func loadCases(t *testing.T) []conformanceCase {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), mandatespec.CasesPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []conformanceCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no cases")
	}
	return file.Cases
}

// pdpFor builds a PDP whose sources answer with the inputs of the case: the catalog
// knows the resource, the clock returns the case time, the household zone is the case
// zone, and the mandate store has the case mandate as the agent's only candidate.
func pdpFor(t *testing.T, c conformanceCase) (*PDP, string) {
	t.Helper()
	doc := []byte(c.MandateInline)
	if c.Mandate != "" {
		var err error
		if doc, err = fs.ReadFile(mandatespec.FS(), c.Mandate); err != nil {
			t.Fatal(err)
		}
	}
	var meta struct {
		Principal string `json:"principal"`
		Agent     struct {
			ClientID string `json:"client_id"`
		} `json:"agent"`
	}
	_ = json.Unmarshal(doc, &meta)
	at, err := time.Parse(time.RFC3339, c.Time)
	if err != nil {
		t.Fatalf("%s: time %q: %v", c.ID, c.Time, err)
	}
	r := c.RawResource
	p := New(Config{
		Principal: meta.Principal,
		Mandates:  fakeMandates{candidates: []mandate.Candidate{stored(doc, mandate.Info{})}},
		Catalog:   catalogFor(r.EntityID, r.Category, r.Area, r.Critical),
		TimeZone:  func() string { return c.Timezone },
		Now:       func() time.Time { return at },
	})
	return p, meta.Agent.ClientID
}

// catalogFor knows the resource of a case; a case without category stands for a
// resource the directory does not contain (SPEC-v0 section 4).
func catalogFor(entityID, category, area string, critical bool) fakeCatalog {
	if category == "" {
		return fakeCatalog{}
	}
	return fakeCatalog{entityID: {EntityID: entityID, Category: category, Area: area, Critical: critical}}
}

func post(t *testing.T, url string, body any) (*http.Response, Response) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url+"/access/v1/evaluation", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// Every conformance case of mandate-spec runs against the AuthZEN endpoint over HTTP.
// A revoked mandate is never a candidate of the selection (SPEC-v0 section 4.3); those
// cases belong to the evaluator class, the selection cases cover them for the PDP.
func TestConformanceCasesOverHTTP(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.Revoked {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			p, clientID := pdpFor(t, c)
			srv := httptest.NewServer(p.Handler())
			defer srv.Close()
			resp, got := post(t, srv.URL, Request{
				Subject:  Subject{Type: "agent", ID: clientID, Properties: SubjectProperties{Principal: p.cfg.Principal}},
				Action:   Action{Name: c.Action, Properties: c.Parameters},
				Resource: Resource{Type: c.RawResource.Category, ID: c.RawResource.EntityID, Properties: ResourceProperties{Area: c.RawResource.Area}},
				Context:  RequestContext{Time: c.Time},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if got.Decision != (c.Expected == "allow") || got.Context.Outcome != c.Expected || got.Context.Reason != c.Reason {
				t.Errorf("%s (%s): got %+v, want %s/%s", c.ID, c.Why, got, c.Expected, c.Reason)
			}
			wantRule := ""
			if c.RuleID != nil {
				wantRule = *c.RuleID
			}
			if got.Context.RuleID != wantRule || got.Context.ApprovalTimeout != c.ApprovalTimeout {
				t.Errorf("%s: rule %q timeout %q, want %q %q", c.ID, got.Context.RuleID, got.Context.ApprovalTimeout, wantRule, c.ApprovalTimeout)
			}
			if c.Reason != "invalid_mandate" && !strings.HasPrefix(got.Context.MandateDigest, "sha256:") {
				t.Errorf("%s: mandate_digest missing", c.ID)
			}
		})
	}
}

func voice(t *testing.T) (Config, string) {
	t.Helper()
	doc, _ := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	m, err := evaluator.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Principal: "household:hm-7f3a",
		Mandates: fakeMandates{candidates: []mandate.Candidate{{Stored: evaluator.NewStored(doc, evaluator.StatusActive),
			Info: mandate.Info{ID: "m-voice-assistant", Digest: m.Digest(), MaxActionsPerHour: 60}}}},
		Catalog: fakeCatalog{
			"light.kitchen": {EntityID: "light.kitchen", Category: "light", Area: "kitchen"},
			"camera.porch":  {EntityID: "camera.porch", Category: "camera", Area: "porch"},
			"lock.front":    {EntityID: "lock.front", Category: "lock", Area: "hallway"},
		},
		TimeZone: func() string { return "Europe/Berlin" },
		Now:      func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
	}, "hm-client:voice-7c21e9a4"
}

func TestInputsComeFromThePEPNotTheRequest(t *testing.T) {
	cfg, clientID := voice(t)
	p := New(cfg)
	// The agent claims the camera is a light in the kitchen; the catalog says camera.
	got := p.Evaluate(context.Background(), Request{
		Subject:  Subject{Type: "agent", ID: clientID, Properties: SubjectProperties{Principal: cfg.Principal}},
		Action:   Action{Name: "turn_on"},
		Resource: Resource{Type: "light", ID: "camera.porch", Properties: ResourceProperties{Area: "kitchen"}},
		Context:  RequestContext{Time: "2026-01-01T00:00:00Z"},
	})
	if got.Decision || got.Context.Reason != "unknown_action" {
		t.Errorf("Evaluate = %+v, want deny unknown_action (turn_on is no camera action)", got)
	}
}

func TestDecideReportsTheResolvedInputs(t *testing.T) {
	cfg, clientID := voice(t)
	d, err := New(cfg).Decide(context.Background(), clientID, "lock.front", "unlock", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Result.Decision != evaluator.Ask || d.Resource.Category != "lock" || d.Resource.Area != "hallway" ||
		d.TimeZone != "Europe/Berlin" || !d.Time.Equal(cfg.Now()) || d.MandateID != "m-voice-assistant" ||
		d.MaxActionsPerHour != 60 || d.Status != evaluator.StatusActive || !d.Known {
		t.Errorf("Decide = %+v", d)
	}
}

func TestUnknownEntityIsDenied(t *testing.T) {
	cfg, clientID := voice(t)
	d, err := New(cfg).Decide(context.Background(), clientID, "light.unknown", "turn_on", nil)
	if err != nil || d.Result.Decision != evaluator.Deny || d.Result.Reason != evaluator.ReasonUnknownResource || d.Known {
		t.Errorf("Decide = %+v, %v; want deny with unknown_resource for an unknown entity", d, err)
	}
	// Another spelling of a known entity is another, unknown resource (SPEC-v0 section 3.4).
	d, _ = New(cfg).Decide(context.Background(), clientID, "Light.kitchen", "turn_on", nil)
	if d.Result.Decision != evaluator.Deny || d.Result.Reason != evaluator.ReasonUnknownResource {
		t.Errorf("Decide for another spelling = %+v, want unknown_resource", d.Result)
	}
}

func TestMissingMandateIsDenied(t *testing.T) {
	cfg, clientID := voice(t)
	cfg.Mandates = fakeMandates{}
	d, err := New(cfg).Decide(context.Background(), clientID, "light.kitchen", "turn_on", nil)
	if err != nil || d.Result.Decision != evaluator.Deny || d.Result.Reason != evaluator.ReasonNoMandate {
		t.Errorf("Decide = %+v, %v", d, err)
	}
	cfg.Mandates = fakeMandates{err: errors.New("database gone")}
	d, err = New(cfg).Decide(context.Background(), clientID, "light.kitchen", "turn_on", nil)
	if err == nil || d.Result.Decision != evaluator.Deny {
		t.Errorf("Decide with a store error = %+v, %v; want deny and the error", d, err)
	}
	if got := New(cfg).Evaluate(context.Background(), Request{
		Subject: Subject{Type: "agent", ID: clientID, Properties: SubjectProperties{Principal: cfg.Principal}},
		Action:  Action{Name: "turn_on"}, Resource: Resource{ID: "light.kitchen"},
	}); got.Decision || got.Context.Outcome != "deny" {
		t.Errorf("Evaluate with a store error = %+v", got)
	}
}

func TestEvaluateChecksSubject(t *testing.T) {
	cfg, clientID := voice(t)
	p := New(cfg)
	for name, tt := range map[string]struct {
		subject Subject
		reason  string
	}{
		"other household": {Subject{Type: "agent", ID: clientID, Properties: SubjectProperties{Principal: "household:other"}}, "no_mandate"},
		"no id":           {Subject{Type: "agent", Properties: SubjectProperties{Principal: cfg.Principal}}, "no_mandate"},
		"not an agent":    {Subject{Type: "user", ID: clientID, Properties: SubjectProperties{Principal: cfg.Principal}}, "invalid_request"},
	} {
		got := p.Evaluate(context.Background(), Request{Subject: tt.subject, Action: Action{Name: "turn_on"}, Resource: Resource{ID: "light.kitchen"}})
		if got.Decision || got.Context.Outcome != "deny" || got.Context.Reason != tt.reason || got.Context.MandateDigest != "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestHandlerRejectsBadRequests(t *testing.T) {
	cfg, _ := voice(t)
	srv := httptest.NewServer(New(cfg).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/access/v1/evaluation")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", resp.StatusCode)
	}
	for name, body := range map[string]string{
		"malformed":  `{`,
		"two values": `{} {}`,
		"too large":  `{"subject":{"type":"agent","id":"` + strings.Repeat("a", 70000) + `"}}`,
	} {
		resp, err := http.Post(srv.URL+"/access/v1/evaluation", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
		}
	}
	resp, err = http.Post(srv.URL+"/other", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("other path: %d", resp.StatusCode)
	}
}

func TestListenOnlyOnLoopback(t *testing.T) {
	cfg, _ := voice(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := New(cfg).ListenAndServe(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("addr = %s", addr)
	}
	for _, bad := range []string{"0.0.0.0:0", ":0", "nonsense"} {
		if _, err := New(cfg).ListenAndServe(ctx, bad); err == nil {
			t.Errorf("listening on %q was allowed", bad)
		}
	}
	resp, out := post(t, "http://"+addr, Request{Subject: Subject{Type: "agent", ID: "x"}, Resource: Resource{ID: "light.kitchen"}, Action: Action{Name: "read"}})
	if resp.StatusCode != http.StatusOK || out.Decision {
		t.Errorf("loopback endpoint: %d %+v", resp.StatusCode, out)
	}
}

func TestAdditionalAuthZENMembersAreIgnored(t *testing.T) {
	cfg, clientID := voice(t)
	srv := httptest.NewServer(New(cfg).Handler())
	defer srv.Close()
	body := `{"subject":{"type":"agent","id":"` + clientID + `","properties":{"principal":"` + cfg.Principal + `","x":1}},` +
		`"action":{"name":"turn_on"},"resource":{"type":"light","id":"light.kitchen"},"context":{"time":"2026-10-06T12:00:00Z","foo":"bar"},"extra":true}`
	resp, err := http.Post(srv.URL+"/access/v1/evaluation", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || !out.Decision {
		t.Errorf("status %d, %+v", resp.StatusCode, out)
	}
}

func TestSnapshotDecidesManyOnOneMandate(t *testing.T) {
	cfg, clientID := voice(t)
	s, err := New(cfg).Snapshot(context.Background(), clientID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Decide("light.kitchen", "turn_on", nil).Result.Decision != evaluator.Allow ||
		s.Decide("lock.front", "unlock", nil).Result.Decision != evaluator.Ask || s.MaxActionsPerHour() != 60 {
		t.Error("snapshot decisions differ from the mandate")
	}
}

func TestEvaluatePassesParametersAndApprovers(t *testing.T) {
	cfg, clientID := voice(t)
	p := New(cfg)
	subject := Subject{Type: "agent", ID: clientID, Properties: SubjectProperties{Principal: cfg.Principal}}
	got := p.Evaluate(context.Background(), Request{Subject: subject, Action: Action{Name: "unlock"}, Resource: Resource{ID: "lock.front"}})
	if got.Context.Outcome != "ask" || got.Context.ApprovalTimeout == "" || len(got.Context.Approvers) == 0 {
		t.Errorf("ask without approval settings: %+v", got)
	}
	for name, properties := range map[string]map[string]json.Number{
		"fraction":  {"brightness": "50.5"},
		"too large": {"brightness": "9007199254740992"},
		"no number": {"brightness": "x"},
	} {
		got := p.Evaluate(context.Background(), Request{Subject: subject, Action: Action{Name: "turn_on", Properties: properties}, Resource: Resource{ID: "light.kitchen"}})
		if got.Decision || got.Context.Reason != "invalid_request" {
			t.Errorf("%s: %+v, want invalid_request", name, got)
		}
	}
	got = p.Evaluate(context.Background(), Request{Subject: subject, Action: Action{Name: "turn_on", Properties: map[string]json.Number{"brightness": "5e1"}}, Resource: Resource{ID: "light.kitchen"}})
	if !got.Decision {
		t.Errorf("integer parameter written as 5e1: %+v", got)
	}
}

func TestCriticalEntityNeedsConfirmation(t *testing.T) {
	cfg, clientID := voice(t)
	catalog := cfg.Catalog.(fakeCatalog)
	device := catalog["light.kitchen"]
	device.Critical = true
	catalog["light.kitchen"] = device
	d, _ := New(cfg).Decide(context.Background(), clientID, "light.kitchen", "turn_on", nil)
	if d.Result.Decision != evaluator.Ask || d.Result.Reason != evaluator.ReasonCriticalDemotion || !d.Resource.Critical {
		t.Errorf("Decide on an entity marked as critical = %+v", d.Result)
	}
}

// Every selection case of mandate-spec (SPEC-v0 section 4.3) runs against the AuthZEN
// endpoint: the store returns all mandates of the case, revoked ones included, and the
// PDP selects. A revoked mandate alone is no_mandate, two current ones are ambiguous.
func TestSelectionCasesOverHTTP(t *testing.T) {
	data, err := fs.ReadFile(mandatespec.FS(), mandatespec.SelectionCasesPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			conformanceCase
			Mandates []struct {
				MandateInline json.RawMessage `json:"mandate_inline"`
				Revoked       bool            `json:"revoked"`
			} `json:"mandates"`
			Subject struct {
				ClientID  string `json:"client_id"`
				Principal string `json:"principal"`
			} `json:"subject"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil || len(file.Cases) == 0 {
		t.Fatalf("selection cases: %v", err)
	}
	for _, c := range file.Cases {
		t.Run(c.ID, func(t *testing.T) {
			var candidates []mandate.Candidate
			for _, m := range c.Mandates {
				if !m.Revoked { // the store returns only active mandates
					candidates = append(candidates, stored(m.MandateInline, mandate.Info{}))
				}
			}
			at, err := time.Parse(time.RFC3339, c.Time)
			if err != nil {
				t.Fatal(err)
			}
			r := c.RawResource
			p := New(Config{Principal: c.Subject.Principal, Mandates: fakeMandates{candidates: candidates},
				Catalog:  catalogFor(r.EntityID, r.Category, r.Area, r.Critical),
				TimeZone: func() string { return c.Timezone }, Now: func() time.Time { return at }})
			srv := httptest.NewServer(p.Handler())
			defer srv.Close()
			resp, got := post(t, srv.URL, Request{
				Subject:  Subject{Type: "agent", ID: c.Subject.ClientID, Properties: SubjectProperties{Principal: c.Subject.Principal}},
				Action:   Action{Name: c.Action, Properties: c.Parameters},
				Resource: Resource{ID: r.EntityID},
			})
			if resp.StatusCode != http.StatusOK || got.Decision != (c.Expected == "allow") || got.Context.Outcome != c.Expected || got.Context.Reason != c.Reason {
				t.Errorf("%s (%s): got %+v, want %s/%s", c.ID, c.Why, got, c.Expected, c.Reason)
			}
		})
	}
}

// The rate limit is known before a mandate is selected: the strictest of the candidates.
func TestRateLimitOfTheCandidates(t *testing.T) {
	cfg, clientID := voice(t)
	doc, _ := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	cfg.Mandates = fakeMandates{candidates: []mandate.Candidate{
		stored(doc, mandate.Info{MaxActionsPerHour: 60}), stored(doc, mandate.Info{MaxActionsPerHour: 0}), stored(doc, mandate.Info{MaxActionsPerHour: 20}),
	}}
	snap, err := New(cfg).Snapshot(context.Background(), clientID)
	if err != nil || snap.MaxActionsPerHour() != 20 {
		t.Errorf("MaxActionsPerHour = %d, %v", snap.MaxActionsPerHour(), err)
	}
	cfg.Mandates = fakeMandates{}
	if snap, _ := New(cfg).Snapshot(context.Background(), clientID); snap.MaxActionsPerHour() != 0 {
		t.Errorf("without a mandate = %d", snap.MaxActionsPerHour())
	}
}

// An invalid stored mandate has no digest from the evaluation; the decision still names
// it, so that the audit entry says which mandate denied.
func TestInvalidMandateIsNamed(t *testing.T) {
	cfg, clientID := voice(t)
	cfg.Mandates = fakeMandates{candidates: []mandate.Candidate{{Stored: evaluator.NewStored(nil, evaluator.StatusActive),
		Info: mandate.Info{ID: "m-broken", Digest: "sha256:" + strings.Repeat("a", 64), MaxActionsPerHour: 5}}}}
	d, err := New(cfg).Decide(context.Background(), clientID, "light.kitchen", "turn_on", nil)
	if err != nil || d.Result.Reason != evaluator.ReasonInvalidMandate || d.MandateID != "m-broken" || d.StoredDigest == "" || d.MaxActionsPerHour != 5 {
		t.Errorf("Decide = %+v, %v", d, err)
	}
}
