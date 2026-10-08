// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/pdp"
)

// resource is a resource as the directory of the PEP knows it.
type resource struct {
	EntityID string `json:"entity_id"`
	Category string `json:"category,omitempty"`
	Area     string `json:"area,omitempty"`
	Critical bool   `json:"critical,omitempty"`
}

// storedMandate is a stored mandate with its status.
type storedMandate struct {
	Mandate string `json:"mandate"`
	Revoked bool   `json:"revoked,omitempty"`
}

// state is everything the PDP otherwise takes from its mandate store, its directory, its
// clock and the household configuration.
type state struct {
	Mandates  []storedMandate `json:"mandates"`
	Directory []resource      `json:"directory"`
	Time      string          `json:"time"`
	Timezone  string          `json:"timezone,omitempty"`
}

// candidates is the mandate store of a state: like mandate.Store.Candidates it returns
// the mandates that are not revoked, for every agent; the PDP selects among them.
type candidates []mandate.Candidate

func (c candidates) Candidates(context.Context, string) ([]mandate.Candidate, error) {
	return c, nil
}

func newCandidates(stored []storedMandate) candidates {
	out := candidates{}
	for _, s := range stored {
		if s.Revoked {
			continue
		}
		info := mandate.Info{Status: mandate.StatusActive}
		if m, err := evaluator.Parse([]byte(s.Mandate)); err == nil {
			info.ID, info.ClientID, info.Digest = m.ID(), m.ClientID(), m.Digest()
		}
		out = append(out, mandate.Candidate{Info: info, Stored: evaluator.NewStored([]byte(s.Mandate), evaluator.StatusActive)})
	}
	return out
}

// directory is the resource directory of a state.
type directory map[string]catalog.Device

func (d directory) Lookup(entityID string) (catalog.Device, bool) {
	dev, ok := d[entityID]
	return dev, ok
}

func newDirectory(resources []resource) directory {
	out := directory{}
	for _, r := range resources {
		out[r.EntityID] = catalog.Device{EntityID: r.EntityID, Category: r.Category, Area: r.Area, Critical: r.Critical}
	}
	return out
}

// newPDP is Home-Mandate's PDP on the state. principal is the household it serves.
func newPDP(s state, principal string, at time.Time) *pdp.PDP {
	return pdp.New(pdp.Config{
		Principal: principal,
		Mandates:  newCandidates(s.Mandates),
		Catalog:   newDirectory(s.Directory),
		TimeZone:  func() string { return s.Timezone },
		Now:       func() time.Time { return at },
	})
}

// outcome is a decision as the test interface reports it.
type outcome struct {
	Decision        string   `json:"decision,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	RuleID          *string  `json:"rule_id,omitempty"`
	ApprovalTimeout string   `json:"approval_timeout,omitempty"`
	Approvers       []string `json:"approvers,omitempty"`
	MandateDigest   string   `json:"mandate_digest,omitempty"`
	Selected        *string  `json:"selected,omitempty"`
}

// decide sends the request of an agent through the AuthZEN path of the PDP, as another
// gateway would, and reports which mandate it was decided on.
func decide(ctx context.Context, p *pdp.PDP, clientID, principal string, r resource, action string, parameters map[string]json.Number) outcome {
	resp := p.Evaluate(ctx, pdp.Request{
		Subject:  pdp.Subject{Type: "agent", ID: clientID, Properties: pdp.SubjectProperties{Principal: principal}},
		Action:   pdp.Action{Name: action, Properties: parameters},
		Resource: pdp.Resource{ID: r.EntityID},
	})
	out := outcome{Decision: resp.Context.Outcome, Reason: resp.Context.Reason, ApprovalTimeout: resp.Context.ApprovalTimeout,
		Approvers: resp.Context.Approvers, MandateDigest: resp.Context.MandateDigest}
	if resp.Context.RuleID != "" {
		out.RuleID = &resp.Context.RuleID
	}
	// The mandate it was decided on; AuthZEN does not report it.
	if d, err := p.Decide(ctx, clientID, r.EntityID, action, nil); err == nil && d.MandateID != "" && d.Result.MandateDigest == out.MandateDigest {
		out.Selected = &d.MandateID
	}
	return out
}
