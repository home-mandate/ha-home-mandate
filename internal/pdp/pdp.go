// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pdp is the Policy Decision Point: the AuthZEN evaluation endpoint of SPEC-v0
// section 6 on top of the reference evaluator. It takes category, area, time, time zone
// and mandate status from the catalog, its clock, the household configuration and the
// mandate store, never from the request (SPEC-v0 section 4, origin of inputs). The HTTP
// endpoint listens on loopback only; the PEP calls Decide in-process.
package pdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

const (
	evaluationPath  = "/access/v1/evaluation"
	maxRequestBytes = 64 << 10
)

// Mandates provides the current mandate of an agent.
type Mandates interface {
	ForAgent(ctx context.Context, clientID string) (mandate.Loaded, error)
}

// Catalog resolves entities to category and area.
type Catalog interface {
	Lookup(entityID string) (catalog.Device, bool)
}

// Config wires the PDP to its sources.
type Config struct {
	Principal string
	Mandates  Mandates
	Catalog   Catalog
	// TimeZone returns the household's IANA time zone from Home Assistant.
	TimeZone func() string
	Now      func() time.Time
}

// PDP evaluates requests.
type PDP struct {
	cfg Config
}

// New returns a PDP; Now defaults to time.Now.
func New(cfg Config) *PDP {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &PDP{cfg: cfg}
}

// Decision is the result of an evaluation together with the inputs it was based on, for
// the PEP and the audit log.
type Decision struct {
	Result            evaluator.Result
	Resource          evaluator.Resource
	Action            string
	Known             bool // the entity is in the catalog
	Time              time.Time
	TimeZone          string
	Status            evaluator.MandateStatus
	MandateID         string
	MaxActionsPerHour int
}

// Snapshot holds an agent's mandate, the time and the household time zone for a
// series of decisions, so that one request (e.g. a list) is decided on one mandate
// version with one store query.
type Snapshot struct {
	p        *PDP
	loaded   mandate.Loaded
	found    bool
	now      time.Time
	timeZone string
}

// Snapshot loads the mandate of clientID. A store error is returned together with a
// snapshot that denies everything.
func (p *PDP) Snapshot(ctx context.Context, clientID string) (*Snapshot, error) {
	s := &Snapshot{p: p, now: p.cfg.Now(), timeZone: p.cfg.TimeZone()}
	loaded, err := p.cfg.Mandates.ForAgent(ctx, clientID)
	switch {
	case err == nil:
		s.loaded, s.found = loaded, true
	case !errors.Is(err, mandate.ErrNotFound):
		return s, fmt.Errorf("pdp: load mandate: %w", err)
	}
	return s, nil
}

// Decide evaluates action on entityID.
func (s *Snapshot) Decide(entityID, action string) Decision {
	d := Decision{Time: s.now, TimeZone: s.timeZone, Resource: evaluator.Resource{EntityID: entityID}, Action: action}
	if dev, ok := s.p.cfg.Catalog.Lookup(entityID); ok {
		d.Known, d.Resource.Category, d.Resource.Area = true, dev.Category, dev.Area
	}
	if !s.found {
		d.Result = evaluator.Result{Decision: evaluator.Deny, Reason: evaluator.ReasonInvalidMandate}
		return d
	}
	d.Status, d.MandateID, d.MaxActionsPerHour = s.loaded.Status, s.loaded.Info.ID, s.loaded.Info.MaxActionsPerHour
	d.Result = evaluator.Evaluate(s.loaded.Mandate, evaluator.Request{
		Resource: d.Resource, Action: action, Time: d.Time, TimeZone: d.TimeZone, Status: d.Status,
	})
	return d
}

// MaxActionsPerHour is the agent's rate limit; 0 without a mandate.
func (s *Snapshot) MaxActionsPerHour() int {
	return s.loaded.Info.MaxActionsPerHour
}

// Decide evaluates action on entityID for the agent clientID. A store error is returned
// together with a deny decision.
func (p *PDP) Decide(ctx context.Context, clientID, entityID, action string) (Decision, error) {
	s, err := p.Snapshot(ctx, clientID)
	return s.Decide(entityID, action), err
}

// AuthZEN request and response (SPEC-v0 section 6).
type (
	Request struct {
		Subject  Subject        `json:"subject"`
		Action   Action         `json:"action"`
		Resource Resource       `json:"resource"`
		Context  RequestContext `json:"context"`
	}
	Subject struct {
		Type       string            `json:"type"`
		ID         string            `json:"id"`
		Properties SubjectProperties `json:"properties"`
	}
	SubjectProperties struct {
		Principal string `json:"principal"`
	}
	Action struct {
		Name string `json:"name"`
	}
	Resource struct {
		Type       string             `json:"type"`
		ID         string             `json:"id"`
		Properties ResourceProperties `json:"properties"`
	}
	ResourceProperties struct {
		Area string `json:"area,omitempty"`
	}
	RequestContext struct {
		Time string `json:"time,omitempty"`
	}
	Response struct {
		Decision bool            `json:"decision"`
		Context  ResponseContext `json:"context"`
	}
	ResponseContext struct {
		Outcome         string `json:"outcome"`
		Reason          string `json:"reason"`
		RuleID          string `json:"rule_id,omitempty"`
		ApprovalTimeout string `json:"approval_timeout,omitempty"`
		MandateDigest   string `json:"mandate_digest,omitempty"`
	}
)

// Evaluate answers an AuthZEN request. resource.type, resource.properties.area and
// context.time are not used (SPEC-v0 section 6). An unknown subject is deny with
// invalid_mandate.
func (p *PDP) Evaluate(ctx context.Context, req Request) Response {
	if req.Subject.Type != "agent" || req.Subject.ID == "" || req.Subject.Properties.Principal != p.cfg.Principal {
		return response(evaluator.Result{Decision: evaluator.Deny, Reason: evaluator.ReasonInvalidMandate})
	}
	d, _ := p.Decide(ctx, req.Subject.ID, req.Resource.ID, req.Action.Name) // an error is a deny already
	return response(d.Result)
}

func response(r evaluator.Result) Response {
	out := Response{Decision: r.Decision == evaluator.Allow, Context: ResponseContext{
		Outcome: string(r.Decision), Reason: string(r.Reason), RuleID: r.RuleID, MandateDigest: r.MandateDigest,
	}}
	if r.Decision == evaluator.Ask && r.Approval != nil {
		out.Context.ApprovalTimeout = r.Approval.Timeout
	}
	return out
}

// Handler serves POST /access/v1/evaluation.
func (p *PDP) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+evaluationPath, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		// AuthZEN allows additional members; they are ignored.
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.Evaluate(r.Context(), req))
	})
	return mux
}

// ListenAndServe serves the endpoint on a loopback address until ctx ends and returns
// the address it listens on.
func (p *PDP) ListenAndServe(ctx context.Context, addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("pdp: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("pdp: %s is not a loopback address", host)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("pdp: %w", err)
	}
	srv := &http.Server{Handler: p.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	return ln.Addr().String(), nil
}
