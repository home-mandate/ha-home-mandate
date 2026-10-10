// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/pdp"
)

// Repeated requests (ARCHITECTURE section 7, issue #27 part C). Clients give up on a call
// after about a minute and call again; the gateway cannot tell that a client gave up,
// only that the same thing is asked again. Three layers keep a repeat from asking the
// human twice or acting twice:
//
//  1. One open request per agent and device: the same call (the fingerprint of the
//     effective call, params_digest) attaches to the open request and gets its approval
//     ID and outcome, as approval_status would; another call is denied approval_pending.
//  2. Replay window: after a confirmed request was executed, the same call of the agent
//     for the device within the request's timeout plus 5 minutes gets "already executed"
//     from the journal, also after a restart.
//  3. The state that was shown (state.go): checked at the execution.
//
// An optional idempotency key ties an agent's calls to one request for as long as the
// journal holds it, regardless of the window. The checks and the creation of a request
// run under a lock per agent and device, so concurrent identical calls create one
// request.

// Audit error codes of calls that made no request of their own. They are decision
// entries (SPEC-v0 section 9.2: every request of an authenticated agent) denied by the
// approval process without an approval object (no request was made for them, section
// 9.1); they count towards the rate limit like every request.
const (
	errDuplicate           = "approval_duplicate"   // attached to the open request
	errAlreadyExecuted     = "already_executed"     // replayed from the journal
	errIdempotencyMismatch = "idempotency_conflict" // the key names another call
	// errFingerprint: the effective call could not be fingerprinted; refused, never
	// compared as "".
	errFingerprint = "call_fingerprint"
)

const statusAlreadyExecuted = "already_executed"

// idempotencyKeyPattern bounds the key an agent may give perform_action.
var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// deviceKey is the key of layer 1: the agent and the resolved device.
func deviceKey(clientID, entityID string) string {
	return clientID + "\x00" + entityID
}

// lockStripes is the number of locks for keys and for devices each: a fixed set, so
// agent-chosen keys cannot make it grow. Two keys may share a lock; that only serializes
// their checks.
const lockStripes = 256

func stripe(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // a hash.Hash never fails to write
	return h.Sum32() % lockStripes
}

// keyLock and deviceLock serialize the checks and the creation of a request per
// idempotency key and per agent and device; always taken in this order.
func (g *Gateway) keyLock(key string) *sync.Mutex    { return &g.keyLocks[stripe(key)] }
func (g *Gateway) deviceLock(key string) *sync.Mutex { return &g.deviceLocks[stripe(key)] }

// repeated is what an earlier request answers for a repeated call; nil if there is none
// and a new request is to be made. It runs with the device lock held.
type repeated struct {
	w       *waiting // the open (or kept) request to attach to
	ref     string   // a request in the journal the idempotency key names
	replay  bool     // ref was executed within the replay window
	at      time.Time
	auditAs string // the audit error of the call
	err     error  // a refusal instead
}

// findRepeat applies the idempotency key and layers 1 and 2 for a call of agent a.
func (g *Gateway) findRepeat(ctx context.Context, clientID, entityID, action string, call ha.ServiceCall, digest, key string) (*repeated, error) {
	if key != "" {
		g.mu.Lock()
		w := g.byKey[clientID+"\x00"+key]
		g.mu.Unlock()
		if w != nil {
			if w.entityID != entityID || w.digest != digest {
				return &repeated{auditAs: errIdempotencyMismatch, err: errKeyConflict}, nil
			}
			return &repeated{w: w, auditAs: errDuplicate}, nil
		}
		if g.cfg.Journal != nil {
			k, ok, err := g.cfg.Journal.ByKey(ctx, clientID, key)
			if err != nil {
				return nil, err
			}
			if ok {
				if k.EntityID != entityID || k.ParamsDigest != digest {
					return &repeated{auditAs: errIdempotencyMismatch, err: errKeyConflict}, nil
				}
				return &repeated{ref: k.Ref, auditAs: errDuplicate}, nil
			}
		}
	}
	g.mu.Lock()
	w := g.open[deviceKey(clientID, entityID)]
	g.mu.Unlock()
	if w != nil {
		if w.digest != digest {
			return &repeated{auditAs: "approval_pending", err: fmt.Errorf("%s: approval_pending: a request for this device waits "+
				"for a human (approval_id %s); approval_status gives its outcome, approval_cancel withdraws it", codeDenied, w.ref)}, nil
		}
		return &repeated{w: w, auditAs: errDuplicate}, nil
	}
	if g.cfg.Journal == nil {
		return nil, nil
	}
	r, ok, err := g.cfg.Journal.Replay(ctx, clientID, entityID, digest, g.cfg.Now())
	if err != nil || !ok {
		return nil, err
	}
	// Only while the effect holds: a device with a state check that is no longer in the
	// state the call leads to (closed by hand, or by another call) is asked for again.
	if dev := g.knownState(entityID); dev != nil && hasStateCheck(dev.Category, action, call) && !reachedNow(dev.Category, action, call, *dev) {
		return nil, nil
	}
	return &repeated{ref: r.Ref, replay: true, at: r.At, auditAs: errAlreadyExecuted}, nil
}

// errKeyConflict is the answer for an idempotency key that names another call.
var errKeyConflict = errors.New(codeInvalidParams + ": idempotency_conflict: the idempotency_key was used for another action")

// answerRepeat records the repeated call and answers it from the earlier request.
func (g *Gateway) answerRepeat(ctx context.Context, a agent.Agent, d pdp.Decision, r *repeated) (*sdk.CallToolResult, actionOut, error) {
	_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval, Error: r.auditAs})
	switch {
	case r.err != nil:
		return nil, actionOut{}, r.err
	case r.w != nil:
		return g.await(ctx, r.w)
	case r.replay:
		return alreadyExecuted(d, r.ref, r.at)
	}
	return g.earlier(ctx, a.ClientID, d, r.ref)
}

// earlier answers a call whose idempotency key names a request in the journal: attached
// while it is open; once it ended, its outcome marked as earlier (issue #27): an
// execution as already_executed with its time, a refusal with "earlier result of" in
// the text. Never a plain executed: nothing new was done or asked.
func (g *Gateway) earlier(ctx context.Context, clientID string, d pdp.Decision, ref string) (*sdk.CallToolResult, actionOut, error) {
	st, ok, err := g.cfg.Journal.Lookup(ctx, ref, clientID)
	if err != nil {
		g.cfg.Logger.Error("approval journal unreadable", "error", err)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	if !ok {
		return nil, actionOut{}, errNoRequest
	}
	if !st.Ended() {
		if w := g.lookup(ref, clientID); w != nil {
			return g.await(ctx, w)
		}
		return g.fromJournal(ctx, ref, clientID)
	}
	if st.Result.Status == audit.StatusExecuted {
		return alreadyExecuted(d, ref, cmp.Or(st.AnsweredAt, st.EndedAt))
	}
	_, _, refusal := journalResult(st, g.mayRead(ctx, clientID, st.EntityID))
	return nil, actionOut{}, fmt.Errorf("%w (earlier result of %s; nothing new was asked)", refusal, st.EndedAt.UTC().Format(time.RFC3339))
}

// alreadyExecuted tells the agent that the same call was executed after a confirmation
// a short while ago and is not executed again (layer 2).
func alreadyExecuted(d pdp.Decision, ref string, at time.Time) (*sdk.CallToolResult, actionOut, error) {
	when := at.UTC().Format(time.RFC3339)
	text := fmt.Sprintf("ALREADY EXECUTED: %s on %s was executed at %s after a human confirmed it; it was NOT executed again "+
		"now. Approval ID: %s. Ask again later if it really should happen a second time.", d.Action, d.Resource.EntityID, when, ref)
	out := actionOut{Status: statusAlreadyExecuted, ApprovalID: ref, ConfirmedAt: when}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, out, nil
}

// register makes w the open request of its device (and its key) while it is followed;
// the device lock is held.
func (g *Gateway) register(w *waiting) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open[deviceKey(w.clientID, w.entityID)] = w
	if w.key != "" {
		g.byKey[w.clientID+"\x00"+w.key] = w
	}
}

// unregisterLocked ends w as the open request of its device; g.mu must be held. A key
// stays tied to the request through the journal.
func (g *Gateway) unregisterLocked(w *waiting) {
	if k := deviceKey(w.clientID, w.entityID); g.open[k] == w {
		delete(g.open, k)
	}
	if k := w.clientID + "\x00" + w.key; w.key != "" && g.byKey[k] == w {
		delete(g.byKey, k)
	}
}
