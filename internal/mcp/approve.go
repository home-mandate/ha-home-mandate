// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/spec/evaluator"
	"github.com/home-mandate/spec/jcs"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/pdp"
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
// confirmation. Every outcome is in the audit log with the approval. An agent whose last
// request for the device was not approved waits before anyone is asked again. The
// request outlives the call: it is followed in the background (follow), and the call
// waits for its outcome at most ApprovalWait (await).
func (g *Gateway) askHuman(ctx context.Context, a agent.Agent, token string, d pdp.Decision, call ha.ServiceCall, reason, key string) (*sdk.CallToolResult, actionOut, error) {
	digest := callDigest(call)
	if digest == "" {
		// Fail closed: without a fingerprint repeats cannot be told apart.
		g.cfg.Logger.Error("call fingerprint failed, request refused", "entity_id", d.Resource.EntityID)
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: errFingerprint})
		return nil, actionOut{}, errors.New(codeFailed)
	}
	// The key first, then the device: the same key for two devices at once is checked and
	// registered by one call at a time (always in this order, so no deadlock).
	var keyLock *sync.Mutex
	if key != "" {
		keyLock = g.keyLock(a.ClientID + "\x00" + key)
		keyLock.Lock()
	}
	deviceLock := g.deviceLock(deviceKey(a.ClientID, d.Resource.EntityID))
	deviceLock.Lock()
	unlock := func() {
		deviceLock.Unlock()
		if keyLock != nil {
			keyLock.Unlock()
		}
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	// A repeated call asks nobody: it attaches to the open request, gets the result of
	// one executed a short while ago, or is refused (dedup.go).
	r, err := g.findRepeat(ctx, a.ClientID, d.Resource.EntityID, d.Action, call, digest, key)
	if err != nil {
		g.cfg.Logger.Error("approval journal unreadable, request refused", "error", err)
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: errJournal})
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if r != nil {
		unlock()
		locked = false
		return g.answerRepeat(ctx, a, d, r)
	}
	// Nobody is asked for what is done already (SPEC-v0 section 11.1 item 10, state.go),
	// unless the agent may not read the device: then it must not learn its state, and the
	// human, who sees it, is asked.
	canRead := g.mayRead(ctx, a.ClientID, d.Resource.EntityID)
	if dev := g.knownState(d.Resource.EntityID); canRead && dev != nil && reachedNow(d.Resource.Category, d.Action, call, *dev) {
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: errAlreadyInState})
		return nil, actionOut{}, errors.New(codeFailed + ": " + errAlreadyInState)
	}
	if g.cooling(a.ClientID, d.Resource.EntityID) > 0 {
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval, Error: "approval_cooldown"})
		return nil, actionOut{}, errors.New(codeDenied + ": approval_cooldown")
	}
	// A request counts until it ends, not until this call returns (SPEC-v0 section 11.1
	// item 7): follow releases it.
	if !g.reserve(a.ClientID) {
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval})
		return nil, actionOut{}, errors.New(codeDenied + ": approval_pending")
	}

	ref := newAgentRef()
	req := approval.Request{ClientID: a.ClientID, Agent: a.DisplayName, EntityID: d.Resource.EntityID, Area: d.Resource.Area,
		Device: d.Resource.EntityID, Action: d.Action, Reason: reason, Params: shownParams(d, call), Critical: criticalRequest(d),
		Record: &approval.Record{Entry: g.entry(a, d, true, audit.Result{}), ParamsDigest: digest, AgentRef: ref, IdempotencyKey: key}}
	if dev, ok := g.cfg.Catalog.Lookup(d.Resource.EntityID); ok {
		if name, _ := dev.Attributes["friendly_name"].(string); name != "" {
			req.Device = name
		}
		req.State = shownState(dev.Category, dev.State)
	}
	if ap := d.Result.Approval; ap != nil {
		req.Approvers, req.Timeout = ap.Approvers, ap.Duration()
	}
	w := &waiting{ref: ref, clientID: a.ClientID, entityID: d.Resource.EntityID, action: d.Action, digest: digest, key: key,
		state: req.State, canRead: canRead, started: make(chan struct{}), answered: make(chan struct{}), done: make(chan struct{})}
	// Registered before the device lock is let go: a repeat from now on attaches to it.
	g.register(w)
	g.track(w)
	unlock()
	locked = false
	// Calls may attach to the request from now on: it must not fail because this one ends.
	p, err := g.cfg.Approvals.Start(context.WithoutCancel(ctx), req)
	if err != nil {
		res, out, err := g.notStarted(ctx, a, d, p.ID, err)
		g.finish(w, out, err)
		return res, out, err
	}
	g.started(w, p)
	go g.follow(w, p, a, token, d, call, req.Timeout)
	return g.await(ctx, w)
}

// notStarted records a request that could not be made (SPEC-v0 section 9.1: no approval,
// and no request in the UI to close); one that is in the journal but reached nobody
// ends there (row).
func (g *Gateway) notStarted(ctx context.Context, a agent.Agent, d pdp.Decision, row string, err error) (*sdk.CallToolResult, actionOut, error) {
	ref := approvalRef{row: row}
	if errors.Is(err, approval.ErrJournal) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: errJournal}, ref)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if !errors.Is(err, approval.ErrNoApprover) {
		g.cfg.Logger.Error("approval request failed", "error", err)
	}
	_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, ref)
	return nil, actionOut{}, errors.New(codeDenied + ": no_approver")
}

// settle records how a request ended and, after a confirmation, checks again and
// executes. It runs in the background (follow) with a context of its own.
func (g *Gateway) settle(ctx context.Context, a agent.Agent, token string, d pdp.Decision, call ha.ServiceCall, res approval.Result, timeout time.Duration, state string, canRead bool) (*sdk.CallToolResult, actionOut, error) {
	appr := approvalRef{approval: &audit.Approval{Outcome: res.Outcome, By: res.By, Via: res.Via, At: res.At, Cause: res.Cause},
		id: res.ID, row: res.ID}
	if res.Outcome == approval.OutcomeCancelled {
		// Withdrawing and asking again would notify the approvers again and again: a
		// withdrawal starts the same wait as a refusal. An end by the emergency stop, a
		// revocation or a restart is none of the agent's doing.
		if res.Cause == audit.CauseWithdrawn {
			g.coolDown(a.ClientID, d.Resource.EntityID)
		}
		return g.cancelled(ctx, a, token, d, appr)
	}
	if res.Outcome == approval.OutcomeApproved {
		g.forgive(a.ClientID, d.Resource.EntityID)
		return g.afterApproval(ctx, a, token, d, call, appr, res.At.Add(g.approvalValidity(timeout)), state, canRead)
	}
	g.coolDown(a.ClientID, d.Resource.EntityID)
	var code string
	switch res.Outcome {
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

// shownParams is what the human sees of the call besides device and action: its service
// data, and for arming the alarm the mode, which is part of the service, not of the data.
func shownParams(d pdp.Decision, call ha.ServiceCall) map[string]any {
	if d.Resource.Category == "alarm" && d.Action == "arm" {
		return map[string]any{"mode": strings.TrimPrefix(call.Service, vocabulary["alarm"]["arm"].service)}
	}
	return call.Data
}

// approvalRef is the outcome of an approval request for its audit entry: the approval
// (nil when no request was made), the request's ID, which tells the UI which open
// request the entry closes, and the request in the journal that the entry ends (the same
// ID; also set when the request reached nobody, which the UI never showed).
type approvalRef struct {
	approval *audit.Approval
	id       string
	row      string
}

// cancelled records a request that ended before anyone answered (SPEC-v0 section 11.1
// item 8, decision F1) as cancelled with its cause and the denied_by that matches it: a
// revocation of the agent (its token no longer counts) denies by authentication, one of
// only its mandate by mandate.
func (g *Gateway) cancelled(ctx context.Context, a agent.Agent, token string, d pdp.Decision, appr approvalRef) (*sdk.CallToolResult, actionOut, error) {
	deniedBy := audit.DeniedByApproval
	switch appr.approval.Cause {
	case audit.CauseEmergencyStop:
		deniedBy = audit.DeniedByEmergencyStop
	case audit.CauseRevoked:
		deniedBy = audit.DeniedByMandate
		if _, err := g.cfg.Agents.StillAuthorized(ctx, token, g.cfg.Resource); err != nil {
			if !errors.Is(err, agent.ErrUnauthorized) {
				g.cfg.Logger.Error("token check after a revocation failed", "error", err)
			}
			deniedBy = audit.DeniedByAuthentication
		}
	}
	_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: deniedBy}, appr)
	return nil, actionOut{}, errors.New(codeDenied + ": " + cancelledCode(appr.approval.Cause, deniedBy))
}

// callDigest is the digest of the effective call: domain, service and data as they would
// be executed, canonicalised (RFC 8785), so that 50 and 50.0 or another key order are the
// same call. It is empty if the call cannot be encoded, which never happens for data
// that came from JSON.
func callDigest(call ha.ServiceCall) string {
	data, err := json.Marshal(map[string]any{"domain": call.Domain, "service": call.Service, "entity_id": call.EntityID, "data": call.Data})
	if err != nil {
		return ""
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return ""
	}
	canonical, err := jcs.Canonicalize(generic)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// afterApproval checks again what may have changed while the human decided: the
// emergency stop, the agent's token (revoked, e.g. after refresh token reuse), the
// mandate (a revoked agent's mandate denies) and the availability.
//
// A confirmation is valid until expires, its timeout after it was given (SPEC-v0 section
// 11.1 item 5): it is checked right before the call, and the call to Home Assistant ends
// at that point at the latest.
func (g *Gateway) afterApproval(ctx context.Context, a agent.Agent, token string, d pdp.Decision, call ha.ServiceCall, appr approvalRef, expires time.Time, state string, canRead bool) (*sdk.CallToolResult, actionOut, error) {
	if g.stopped(ctx) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": emergency_stop")
	}
	// The token that made the request may have expired meanwhile (10 minutes); what counts
	// is that nothing revoked it (agent.Store.StillAuthorized).
	if _, err := g.cfg.Agents.StillAuthorized(ctx, token, g.cfg.Resource); err != nil {
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
	if code := g.unavailable(); code != "" {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: code}, appr)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if g.clockWrong(ctx) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: errClockBehind}, appr)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if !g.cfg.Now().Before(expires) {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval}, appr)
		return nil, actionOut{}, errors.New(codeDenied + ": approval_expired")
	}
	// The state that was shown (SPEC-v0 section 11.1 item 10, state.go); Home Assistant
	// is connected and the directory current here (checked above).
	dev, exists := g.cfg.Catalog.Lookup(d.Resource.EntityID)
	if code := stateCheck(d.Resource.Category, d.Action, call, state, dev, exists); code != "" {
		_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: code}, appr)
		return nil, actionOut{}, errors.New(codeFailed + ": " + agentCode(code, canRead))
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
