// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/pdp"
)

// Waiting of the agent (ARCHITECTURE section 7, issue #27). Clients cut tool calls off
// long before an approval timeout, so a perform_action that needs a human waits at most
// ApprovalWait; without an answer by then the agent gets a pending result with an
// approval ID and asks again with approval_status, or withdraws with approval_cancel.
// The request lives on its own: the gateway follows it in the background (follow) and
// executes a confirmed action when the confirmation arrives (SPEC-v0 section 11.1 item
// 5), never when the agent fetches the result.
//
// The MCP Tasks extension (io.modelcontextprotocol/tasks) would plug in at await: a
// client that declares it would get a task bound to the same waiting request instead of
// the pending result; not built while no client supports it.

const (
	// defaultApprovalWait is how long a tool call waits for the answer before it returns
	// the pending result; below the cut-off of the clients (about 60 s).
	defaultApprovalWait = 45 * time.Second
	// statusPerHour bounds the approval_status and approval_cancel calls of one agent: a
	// status call waits up to ApprovalWait, so polling one request without pause takes
	// about 80 an hour.
	statusPerHour = 120
	// resultKeep is how long the exact result of an ended request stays in memory; the
	// journal answers after that (and after a restart) for a day.
	resultKeep = 15 * time.Minute
	// cancelWait bounds how long approval_cancel waits for the withdrawal to be recorded.
	cancelWait = 10 * time.Second

	agentRefPrefix = "apr_"
	agentRefBytes  = 16

	statusPending   = "pending"
	statusExecuted  = "executed"
	statusWithdrawn = "withdrawn"
	// Phases of a pending request: no answer yet, or answered (or otherwise ended) and its
	// outcome, an execution after a confirmation, not settled yet.
	phaseWaiting  = "waiting"
	phaseAnswered = "answered"
	codeConflict  = "conflict"
)

var agentRefPattern = regexp.MustCompile(`^apr_[0-9a-f]{32}$`)

// AnswerGrace bounds how long a call waits for the execution of an answer that arrived
// within ApprovalWait, so that the call stays below the clients' cut-off; the HTTP write
// timeout covers it (cmd/home-mandate).
const AnswerGrace = 10 * time.Second

// answerGrace is AnswerGrace, a variable for tests.
var answerGrace = AnswerGrace

// errNoRequest is the one answer for an approval ID that is unknown, belongs to another
// agent or has expired: they look alike.
var errNoRequest = errors.New(codeNotFound + ": no such approval request")

// waiting is an approval request the gateway follows beyond the tool call that made it.
type waiting struct {
	ref      string // the approval ID the agent knows
	clientID string
	entityID string
	action   string
	digest   string // fingerprint of the effective call (layer 1)
	key      string // the agent's idempotency key, empty without
	state    string // the device's state shown with the request (layer 3)
	canRead  bool   // the agent may read the device: state refusals are named to it
	// row (the request's ID in the UI and the journal) and expires are set once the
	// request was started; read them under g.mu (info).
	row      string
	expires  time.Time
	started  chan struct{} // closed once Start returned (row and expires set) or failed
	answered chan struct{} // closed when the request ended (answered, timed out, cancelled)
	done     chan struct{} // closed when the outcome is final: executed or refused

	// set before done is closed
	out     actionOut
	err     error
	endedAt time.Time
}

func newAgentRef() string {
	var b [agentRefBytes]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return agentRefPrefix + hex.EncodeToString(b[:])
}

// reserve counts a request of the agent towards maxPendingAsks until it ends (release);
// false if the agent has that many waiting already.
func (g *Gateway) reserve(clientID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pendingAsks[clientID] >= maxPendingAsks {
		return false
	}
	g.pendingAsks[clientID]++
	return true
}

// release ends what reserve counted; g.mu must not be held.
func (g *Gateway) release(clientID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseLocked(clientID)
}

func (g *Gateway) releaseLocked(clientID string) {
	if g.pendingAsks[clientID]--; g.pendingAsks[clientID] <= 0 {
		delete(g.pendingAsks, clientID)
	}
}

// track keeps w findable by its approval ID and forgets results older than resultKeep.
func (g *Gateway) track(w *waiting) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forgetLocked()
	g.waits[w.ref] = w
}

// forgetLocked drops results older than resultKeep; the journal answers for them. g.mu
// must be held.
func (g *Gateway) forgetLocked() {
	now := g.cfg.Now()
	for ref, old := range g.waits {
		if !old.endedAt.IsZero() && now.Sub(old.endedAt) > resultKeep {
			delete(g.waits, ref)
		}
	}
}

// follow waits for the end of the request and settles it with a context of its own:
// the agent's call may have ended long ago. A confirmed action is executed here, once.
// After Close a request that ends (no answer can be taken any more: a timeout or a
// cancellation) is not settled: its journal row stays, and the next start records it
// as interrupted (approval.Journal.Recover).
func (g *Gateway) follow(w *waiting, p approval.Pending, a agent.Agent, token string, d pdp.Decision, call ha.ServiceCall, timeout time.Duration) {
	out, err := actionOut{}, errors.New(codeUnavailable)
	defer func() { g.finish(w, out, err) }()
	res := <-p.Done
	close(w.answered)
	g.mu.Lock()
	closed := g.closing
	g.mu.Unlock()
	if closed {
		return
	}
	_, out, err = g.settle(context.Background(), a, token, d, call, res, timeout, w.state, w.canRead)
}

// started records what Start returned for w.
func (g *Gateway) started(w *waiting, p approval.Pending) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.row, w.expires = p.ID, p.Expires
	close(w.started)
}

// info returns w's request ID and expiry, set once it was started.
func (g *Gateway) info(w *waiting) (row string, expires time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return w.row, w.expires
}

// finish makes w's outcome final: whoever waits for it gets out and err, the agent's
// place among its waiting requests is free, and the device has no open request.
func (g *Gateway) finish(w *waiting, out actionOut, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.out, w.err, w.endedAt = out, err, g.cfg.Now()
	close(w.done)
	select {
	case <-w.started:
	default:
		close(w.started) // Start failed
	}
	select {
	case <-w.answered:
	default:
		close(w.answered) // never started
	}
	g.releaseLocked(w.clientID)
	g.unregisterLocked(w)
}

// closePoll is how often Close looks whether the accepted answers are settled.
const closePoll = 10 * time.Millisecond

// Close is the shutdown: first the approval service stops taking answers (StopAnswers),
// so that an answer is either accepted before or discarded. Then it waits at most timeout
// until every request that is no longer open is settled: an accepted answer is recorded
// and, after a confirmation, executed while Home Assistant and the database are still
// there. Requests still open are left to the next start, which records them as
// interrupted: nobody's answer was accepted (SPEC-v0 section 11.1 items 8 and 9). It
// tells whether everything finished.
func (g *Gateway) Close(timeout time.Duration) bool {
	if g.cfg.Approvals != nil {
		g.cfg.Approvals.StopAnswers()
	}
	deadline := time.Now().Add(timeout)
	for g.unsettled() {
		if !time.Now().Before(deadline) {
			g.setClosing()
			return false
		}
		time.Sleep(closePoll)
	}
	g.setClosing()
	return true
}

// unsettled tells whether a request has ended (answered, timed out, cancelled) but its
// outcome is not final yet.
func (g *Gateway) unsettled() bool {
	g.mu.Lock()
	list := make([]*waiting, 0, len(g.waits))
	for _, w := range g.waits {
		list = append(list, w)
	}
	g.mu.Unlock()
	for _, w := range list {
		select {
		case <-w.done:
			continue
		default:
		}
		if row, _ := g.info(w); row != "" && !g.cfg.Approvals.IsOpen(row) {
			return true
		}
	}
	return false
}

func (g *Gateway) setClosing() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing = true
}

// await waits for the outcome of w at most ApprovalWait. An answer that arrived within
// that time is waited for until its execution ended, at most answerGrace longer;
// otherwise the agent gets the pending result. The end of ctx (the agent gave up) ends
// only the waiting, never the request.
func (g *Gateway) await(ctx context.Context, w *waiting) (*sdk.CallToolResult, actionOut, error) {
	timer := time.NewTimer(g.cfg.ApprovalWait)
	defer timer.Stop()
	select {
	case <-w.done:
		return nil, w.out, w.err
	case <-w.answered:
		grace := time.NewTimer(answerGrace)
		defer grace.Stop()
		select {
		case <-w.done:
			return nil, w.out, w.err
		case <-grace.C:
		case <-ctx.Done():
		}
	case <-timer.C:
	case <-ctx.Done():
	}
	return g.pendingResult(ctx, w)
}

// pendingResult tells the agent plainly that the action is not executed yet, and what to
// call: still waiting for a human, or answered and its execution not finished yet.
func (g *Gateway) pendingResult(ctx context.Context, w *waiting) (*sdk.CallToolResult, actionOut, error) {
	select {
	case <-w.answered:
		text := fmt.Sprintf("NOT FINISHED YET. The request for %s on %s was confirmed by a human or ended otherwise, and its "+
			"outcome is being settled: do not tell the user it was done. Approval ID: %s. Call approval_status with this "+
			"approval_id to learn the outcome.", w.action, w.entityID, w.ref)
		out := actionOut{Status: statusPending, Phase: phaseAnswered, ApprovalID: w.ref, Next: "approval_status"}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, out, nil
	default:
	}
	select {
	case <-w.started: // Start returns once delivered (bounded), or fails and finish closes it
	case <-ctx.Done():
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	_, expires := g.info(w)
	until := expires.UTC().Format(time.RFC3339)
	left := max(expires.Sub(g.cfg.Now()).Round(time.Second), 0)
	text := fmt.Sprintf("NOT EXECUTED YET. %s on %s is waiting for a human to approve it; nothing has happened so far. "+
		"Do not tell the user it was done. Approval ID: %s. The request stays open until %s (%s from now); without an "+
		"approval by then it is denied. Call approval_status with this approval_id to learn the outcome (it waits up to %s "+
		"for it), or approval_cancel to withdraw the request.",
		w.action, w.entityID, w.ref, until, left, g.cfg.ApprovalWait)
	out := actionOut{Status: statusPending, Phase: phaseWaiting, ApprovalID: w.ref, OpenUntil: until, Next: "approval_status"}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, out, nil
}

// lookup returns the request the agent knows by ref while the gateway follows it or
// keeps its result; nil otherwise.
func (g *Gateway) lookup(ref, clientID string) *waiting {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forgetLocked()
	w := g.waits[ref]
	if w == nil || w.clientID != clientID {
		return nil
	}
	return w
}

// statusCall checks the agent and its rate limit for approval_status and
// approval_cancel. They are no requests to act: no audit entry, not counted towards
// max_actions_per_hour.
func (g *Gateway) statusCall(req *sdk.CallToolRequest, in approvalInput) (agent.Agent, error) {
	a, err := agentOf(req)
	if err != nil {
		return agent.Agent{}, err
	}
	if !g.statusLimiter.Allow("status:"+a.ClientID, statusPerHour) {
		return agent.Agent{}, errors.New(codeRateLimited)
	}
	if !agentRefPattern.MatchString(in.ApprovalID) {
		return agent.Agent{}, errNoRequest
	}
	return a, nil
}

func (g *Gateway) approvalStatus(ctx context.Context, req *sdk.CallToolRequest, in approvalInput) (*sdk.CallToolResult, actionOut, error) {
	a, err := g.statusCall(req, in)
	if err != nil {
		return nil, actionOut{}, err
	}
	if w := g.lookup(in.ApprovalID, a.ClientID); w != nil {
		return g.await(ctx, w)
	}
	return g.fromJournal(ctx, in.ApprovalID, a.ClientID)
}

// fromJournal answers for a request the gateway no longer follows: one that ended more
// than resultKeep ago, or before a restart. The answer is the outcome the journal holds
// (journalResult); a row that has not ended is from another process and is still pending
// for the agent.
func (g *Gateway) fromJournal(ctx context.Context, ref, clientID string) (*sdk.CallToolResult, actionOut, error) {
	if g.cfg.Journal == nil {
		return nil, actionOut{}, errNoRequest
	}
	st, ok, err := g.cfg.Journal.Lookup(ctx, ref, clientID)
	if err != nil {
		g.cfg.Logger.Error("approval journal unreadable", "error", err)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if !ok {
		return nil, actionOut{}, errNoRequest
	}
	if !st.Ended() {
		phase := phaseWaiting
		if st.State == "executing" {
			phase = phaseAnswered
		}
		return nil, actionOut{Status: statusPending, Phase: phase, ApprovalID: ref, OpenUntil: st.Expires.UTC().Format(time.RFC3339), Next: "approval_status"}, nil
	}
	return journalResult(st, g.mayRead(ctx, clientID, st.EntityID))
}

// mayRead tells whether the agent may read the device now (decision allow); every
// failure means no. An agent that may not read learns nothing about the state.
func (g *Gateway) mayRead(ctx context.Context, clientID, entityID string) bool {
	snap, err := g.cfg.PDP.Snapshot(ctx, clientID)
	return err == nil && snap.Decide(entityID, "read", nil).Result.Decision == evaluator.Allow
}

// knownState is the device as the directory has it, if its state can be trusted now:
// Home Assistant connected, the directory current, the state one of the known ones.
// Otherwise nil, and there is no shortcut: the human is asked.
func (g *Gateway) knownState(entityID string) *catalog.Device {
	if g.unavailable() != "" {
		return nil
	}
	dev, ok := g.cfg.Catalog.Lookup(entityID)
	if !ok || shownState(dev.Category, dev.State) == "" {
		return nil
	}
	return &dev
}

// journalResult is the answer the agent would have got for an ended request, from what
// the journal holds: executed, or the refusal with its reason code.
func journalResult(st approval.Status, canRead bool) (*sdk.CallToolResult, actionOut, error) {
	r := st.Result
	switch r.Status {
	case audit.StatusExecuted:
		return nil, actionOut{Status: statusExecuted}, nil
	case audit.StatusFailed:
		switch r.Error {
		case approval.ErrorOutcomeUnknown, errAlreadyInState, errStateChanged:
			return nil, actionOut{}, errors.New(codeFailed + ": " + agentCode(r.Error, canRead))
		case "mandate_unavailable", errJournal, errClockBehind, "ha_unavailable", "timezone_unknown", "service_user_unknown":
			// As the checks before the call answer; Home Assistant failing during the call
			// (also ha_unavailable) answers failed synchronously, which the journal cannot
			// tell apart.
			return nil, actionOut{}, errors.New(codeUnavailable)
		}
		return nil, actionOut{}, errors.New(codeFailed)
	}
	return nil, actionOut{}, errors.New(codeDenied + ": " + deniedCode(st))
}

// deniedCode is the reason code of a refusal, as the synchronous answer gives it.
func deniedCode(st approval.Status) string {
	r := st.Result
	switch {
	case r.Error != "":
		return r.Error
	case st.Outcome == approval.OutcomeRejected:
		return "approval_rejected"
	case st.Outcome == approval.OutcomeTimeout:
		return "approval_timeout"
	case st.Outcome == approval.OutcomeInvalidResponse:
		return "approval_invalid"
	case st.Outcome == approval.OutcomeCancelled:
		return cancelledCode(st.Cause, r.DeniedBy)
	case st.Outcome == approval.OutcomeApproved:
		switch r.DeniedBy {
		case audit.DeniedByEmergencyStop:
			return "emergency_stop"
		case audit.DeniedByAuthentication:
			return "unauthorized"
		case audit.DeniedByMandate:
			return "mandate_changed"
		}
		return "approval_expired"
	}
	return "no_approver"
}

// cancelledCode is the reason code of a request that ended before an answer.
func cancelledCode(cause, deniedBy string) string {
	switch cause {
	case audit.CauseEmergencyStop:
		return "emergency_stop"
	case audit.CauseRevoked:
		if deniedBy == audit.DeniedByAuthentication {
			return "unauthorized"
		}
		return "revoked"
	case audit.CauseWithdrawn:
		return "approval_withdrawn"
	case audit.CauseInterrupted:
		return "approval_interrupted"
	}
	return "approval_cancelled"
}

// approvalCancel withdraws the agent's own open request (SPEC-v0 section 11.1 item 8).
// It is no answer: a request that was answered or has ended keeps its outcome, also on a
// repeated cancel (conflict). It answers withdrawn once the service took the withdrawal;
// the entry that records it follows within cancelWait at the latest.
func (g *Gateway) approvalCancel(ctx context.Context, req *sdk.CallToolRequest, in approvalInput) (*sdk.CallToolResult, actionOut, error) {
	a, err := g.statusCall(req, in)
	if err != nil {
		return nil, actionOut{}, err
	}
	w := g.lookup(in.ApprovalID, a.ClientID)
	if w == nil {
		// Not followed: unknown, another agent's, or ended (before a restart, or longer ago).
		_, _, err := g.fromJournal(ctx, in.ApprovalID, a.ClientID)
		if errors.Is(err, errNoRequest) || err != nil && err.Error() == codeUnavailable {
			return nil, actionOut{}, err
		}
		return nil, actionOut{}, errAnswered
	}
	select {
	case <-w.started:
	case <-ctx.Done():
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	select {
	case <-w.answered:
		return nil, actionOut{}, errAnswered
	default:
	}
	if row, _ := g.info(w); !g.cfg.Approvals.CancelRequest(row) {
		return nil, actionOut{}, errAnswered // answered at the same moment: the answer stands
	}
	timer := time.NewTimer(cancelWait)
	defer timer.Stop()
	select {
	case <-w.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil, actionOut{Status: statusWithdrawn, ApprovalID: w.ref}, nil
}

// errAnswered is approval_cancel's answer for a request that can no longer be withdrawn.
var errAnswered = errors.New(codeConflict + ": the request was answered or has ended; approval_status gives its outcome")
