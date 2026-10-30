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
	EntityID string   `json:"entity_id"`
	Category string   `json:"category,omitempty"`
	Areas    []string `json:"areas,omitempty"`
	Critical bool     `json:"critical,omitempty"`
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
// the stored mandates with their status, for every agent; the PDP selects among them and
// answers revoked for an agent whose only mandates are revoked.
type candidates []mandate.Candidate

func (c candidates) Candidates(context.Context, string) ([]mandate.Candidate, error) {
	return c, nil
}

func newCandidates(stored []storedMandate) candidates {
	out := candidates{}
	for _, s := range stored {
		info, status := mandate.Info{Status: mandate.StatusActive}, evaluator.StatusActive
		if s.Revoked {
			info.Status, status = mandate.StatusRevoked, evaluator.StatusRevoked
		}
		if m, err := evaluator.Parse([]byte(s.Mandate)); err == nil {
			info.ID, info.ClientID, info.Digest = m.ID(), m.ClientID(), m.Digest()
		}
		out = append(out, mandate.Candidate{Info: info, Stored: evaluator.NewStored([]byte(s.Mandate), status)})
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
		out[r.EntityID] = catalog.Device{EntityID: r.EntityID, Category: r.Category, Areas: r.Areas, Critical: r.Critical}
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
	// Enforced is present, and false, only for a deny that is not enforced (audit mode).
	Enforced *bool `json:"enforced,omitempty"`
	// Confirm is present, and true, only for such a deny that requires a confirmation.
	Confirm  bool    `json:"confirm,omitempty"`
	Selected *string `json:"selected,omitempty"`
}

// decide sends the request of an agent through the AuthZEN path of the PDP, as another
// gateway would, and reports which mandate it was decided on.
func decide(ctx context.Context, p *pdp.PDP, clientID, principal string, r resource, action string, parameters map[string]json.RawMessage) outcome {
	resp := p.Evaluate(ctx, pdp.Request{
		Subject:  pdp.Subject{Type: "agent", ID: clientID, Properties: pdp.SubjectProperties{Principal: principal}},
		Action:   pdp.Action{Name: action, Properties: parameters},
		Resource: pdp.Resource{ID: r.EntityID},
	})
	out := outcome{Decision: resp.Context.Outcome, Reason: resp.Context.Reason, ApprovalTimeout: resp.Context.ApprovalTimeout,
		Approvers: resp.Context.Approvers, MandateDigest: resp.Context.MandateDigest, Enforced: resp.Context.Enforced,
		Confirm: resp.Context.Confirm}
	if resp.Context.RuleID != "" {
		out.RuleID = &resp.Context.RuleID
	}
	// The mandate it was decided on, or the revoked one the selection names (SPEC-v0
	// section 4.3): AuthZEN reports it as mandate_id.
	if resp.Context.MandateID != "" {
		out.Selected = &resp.Context.MandateID
	}
	return out
}
