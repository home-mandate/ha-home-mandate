// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"strconv"
	"strings"
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

// maxReasonRunes bounds the reason an agent may give; the human sees at most 200 runes.
const maxReasonRunes = 1000

// askHuman asks the approvers of the mandate and executes only after a valid
// confirmation. Every outcome is in the audit log with the approval.
func (g *Gateway) askHuman(ctx context.Context, a agent.Agent, d pdp.Decision, call ha.ServiceCall, reason string) (*sdk.CallToolResult, actionOut, error) {
	req := approval.Request{Agent: a.DisplayName, Device: d.Resource.EntityID, Action: d.Action, Reason: reason}
	if dev, ok := g.cfg.Catalog.Lookup(d.Resource.EntityID); ok {
		if name, _ := dev.Attributes["friendly_name"].(string); name != "" {
			req.Device = name
		}
	}
	if ap := d.Result.Approval; ap != nil {
		req.Approvers, req.Timeout = ap.Approvers, approvalTimeout(ap.Timeout)
	}
	res, err := g.cfg.Approvals.Ask(ctx, req)
	if err != nil {
		if !errors.Is(err, approval.ErrNoApprover) {
			g.cfg.Logger.Error("approval request failed", "error", err)
		}
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval})
		return nil, actionOut{}, errors.New(codeDenied + ": no_approver")
	}
	appr := &audit.Approval{Outcome: res.Outcome, By: res.By, At: res.At}
	var code string
	switch res.Outcome {
	case approval.OutcomeApproved:
		return g.afterApproval(ctx, a, d, call, appr)
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

// afterApproval checks again what may have changed while the human decided: the
// emergency stop, the mandate (a revoked agent's mandate denies) and the connection.
func (g *Gateway) afterApproval(ctx context.Context, a agent.Agent, d pdp.Decision, call ha.ServiceCall, appr *audit.Approval) (*sdk.CallToolResult, actionOut, error) {
	if g.stopped(ctx) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": emergency_stop")
	}
	snap, err := g.cfg.PDP.Snapshot(ctx, a.ClientID)
	now := snap.Decide(d.Resource.EntityID, d.Action)
	if err != nil || now.Result.Decision == evaluator.Deny || now.Result.MandateDigest != d.Result.MandateDigest {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": mandate_changed")
	}
	if !g.available() {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: "ha_unavailable"}, appr)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	return g.execute(ctx, a, d, call, appr)
}

// approvalTimeout reads the PT<m>M<s>S timeout of a mandate (validated by the
// evaluator); 0 lets the upper limit apply.
func approvalTimeout(s string) time.Duration {
	rest, ok := strings.CutPrefix(s, "PT")
	if !ok {
		return 0
	}
	var total time.Duration
	if minutes, after, found := strings.Cut(rest, "M"); found {
		n, err := strconv.Atoi(minutes)
		if err != nil {
			return 0
		}
		total, rest = time.Duration(n)*time.Minute, after
	}
	if seconds, found := strings.CutSuffix(rest, "S"); found {
		n, err := strconv.Atoi(seconds)
		if err != nil {
			return 0
		}
		total += time.Duration(n) * time.Second
	} else if rest != "" {
		return 0
	}
	return total
}
