// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/pdp"
)

// errAsk tells performAction that a human must confirm; it never reaches the agent.
var errAsk = errors.New("ask")

const (
	// maxReasonRunes bounds the reason an agent may give; the human sees at most 200.
	maxReasonRunes = 1000
	// maxPendingAsks bounds the approval requests one agent may have waiting, against
	// flooding the approvers and holding many requests open.
	maxPendingAsks = 2
)

// askHuman asks the approvers of the mandate and executes only after a valid
// confirmation. Every outcome is in the audit log with the approval.
func (g *Gateway) askHuman(ctx context.Context, a agent.Agent, token string, d pdp.Decision, call ha.ServiceCall, reason string) (*sdk.CallToolResult, actionOut, error) {
	g.mu.Lock()
	if g.pendingAsks[a.ClientID] >= maxPendingAsks {
		g.mu.Unlock()
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval})
		return nil, actionOut{}, errors.New(codeDenied + ": approval_pending")
	}
	g.pendingAsks[a.ClientID]++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.pendingAsks[a.ClientID]--; g.pendingAsks[a.ClientID] == 0 {
			delete(g.pendingAsks, a.ClientID)
		}
	}()

	req := approval.Request{ClientID: a.ClientID, Agent: a.DisplayName, EntityID: d.Resource.EntityID, Area: d.Resource.Area,
		Device: d.Resource.EntityID, Action: d.Action, Reason: reason, Params: call.Data, Critical: criticalRequest(d)}
	if dev, ok := g.cfg.Catalog.Lookup(d.Resource.EntityID); ok {
		if name, _ := dev.Attributes["friendly_name"].(string); name != "" {
			req.Device = name
		}
	}
	if ap := d.Result.Approval; ap != nil {
		req.Approvers, req.Timeout = ap.Approvers, ap.Duration()
	}
	res, err := g.cfg.Approvals.Ask(ctx, req)
	if err != nil {
		if !errors.Is(err, approval.ErrNoApprover) {
			g.cfg.Logger.Error("approval request failed", "error", err)
		}
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval})
		return nil, actionOut{}, errors.New(codeDenied + ": no_approver")
	}
	if res.Outcome == approval.OutcomeCancelled {
		return g.cancelled(ctx, a, d, res.ID)
	}
	appr := approvalRef{approval: &audit.Approval{Outcome: res.Outcome, By: res.By, Via: res.Via, At: res.At}, id: res.ID}
	var code string
	switch res.Outcome {
	case approval.OutcomeApproved:
		return g.afterApproval(ctx, a, token, d, call, appr, res.At.Add(g.approvalValidity(req.Timeout)))
	case approval.OutcomeRejected:
		code = "approval_rejected"
	case approval.OutcomeInvalidResponse:
		code = "approval_invalid"
	default:
		code = "approval_timeout"
	}
	_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, appr)
	return nil, actionOut{}, errors.New(codeDenied + ": " + code)
}

// approvalRef is the outcome of an approval request for its audit entry: the approval
// (nil when the request ended without an answer) and the request's ID, which tells the UI
// which open request the entry closes.
type approvalRef struct {
	approval *audit.Approval
	id       string
}

// cancelled records a request that the emergency stop or a revocation ended before
// anyone answered (decision F1): no approval, only the denial with its cause.
func (g *Gateway) cancelled(ctx context.Context, a agent.Agent, d pdp.Decision, id string) (*sdk.CallToolResult, actionOut, error) {
	ref := approvalRef{id: id}
	if g.stopped(ctx) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}, ref)
		return nil, actionOut{}, errors.New(codeDenied + ": emergency_stop")
	}
	_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication}, ref)
	return nil, actionOut{}, errors.New(codeDenied + ": unauthorized")
}

// afterApproval checks again what may have changed while the human decided: the
// emergency stop, the agent's token (revoked, e.g. after refresh token reuse), the
// mandate (a revoked agent's mandate denies) and the connection.
//
// A confirmation is valid until expires, its timeout after it was given (SPEC-v0 section
// 11.1 item 5): it is checked right before the call, and the call to Home Assistant ends
// at that point at the latest.
func (g *Gateway) afterApproval(ctx context.Context, a agent.Agent, token string, d pdp.Decision, call ha.ServiceCall, appr approvalRef, expires time.Time) (*sdk.CallToolResult, actionOut, error) {
	if g.stopped(ctx) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": emergency_stop")
	}
	if _, err := g.cfg.Agents.Authenticate(ctx, token, g.cfg.Resource); err != nil {
		if !errors.Is(err, agent.ErrUnauthorized) {
			g.cfg.Logger.Error("token check after an approval failed", "error", err)
		}
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": unauthorized")
	}
	snap, err := g.cfg.PDP.Snapshot(ctx, a.ClientID)
	if err != nil {
		g.cfg.Logger.Error("loading the mandate after an approval failed", "error", err)
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: "mandate_unavailable"}, appr)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if now := snap.Decide(d.Resource.EntityID, d.Action, d.Parameters); now.Result.Decision == evaluator.Deny || now.Result.MandateDigest != d.Result.MandateDigest {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": mandate_changed")
	}
	if !g.available() {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: "ha_unavailable"}, appr)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if !g.cfg.Now().Before(expires) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": approval_expired")
	}
	return g.execute(ctx, a, d, call, appr, expires)
}

// approvalValidity is how long a confirmation stays valid: the timeout of the request,
// at most the configured upper limit of a wait.
func (g *Gateway) approvalValidity(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > g.cfg.ApprovalLimit {
		return g.cfg.ApprovalLimit
	}
	return timeout
}

// criticalRequest tells whether an approval request is critical: by the vocabulary, or on
// a device the household marked, every action except read (SPEC-v0 section 4, step 5).
// Critical requests reach only devices where critical requests are on.
func criticalRequest(d pdp.Decision) bool {
	return evaluator.IsCritical(d.Resource.Category, d.Action) || d.Resource.Critical && d.Action != "read"
}
