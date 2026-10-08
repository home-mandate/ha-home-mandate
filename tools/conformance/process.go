// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	specaudit "github.com/home-mandate/spec/audit"
	"github.com/home-mandate/spec/evaluator"
	"github.com/home-mandate/spec/jws"
)

// maxLineBytes bounds one request: several mandates of 256 KiB inside JSON strings.
const maxLineBytes = 16 << 20

// request is one line of the process binding (SPEC-v0 section 10.2).
type request struct {
	Op       string          `json:"op"`
	Mandate  *string         `json:"mandate"`
	Mandates []storedMandate `json:"mandates"`
	Subject  *struct {
		ClientID  string `json:"client_id"`
		Principal string `json:"principal"`
	} `json:"subject"`
	Request *struct {
		Resource   resource               `json:"resource"`
		Action     string                 `json:"action"`
		Parameters map[string]json.Number `json:"parameters"`
		Time       string                 `json:"time"`
		Timezone   string                 `json:"timezone"`
		Revoked    bool                   `json:"revoked"`
	} `json:"request"`
	Stored  *string         `json:"stored"`
	Offered *string         `json:"offered"`
	Keys    json.RawMessage `json:"keys"`
	JSONL   *string         `json:"jsonl"`
	Entries []string        `json:"entries"`
	LogID   string          `json:"log_id"`
	Entry   *string         `json:"entry"`
}

type response struct {
	Error   string   `json:"error,omitempty"`
	Name    string   `json:"name,omitempty"`
	Version string   `json:"version,omitempty"`
	Ops     []string `json:"ops,omitempty"`
	Valid   *bool    `json:"valid,omitempty"`
	Digest  string   `json:"digest,omitempty"`
	outcome
	Accept   *bool  `json:"accept,omitempty"`
	BrokenAt *int64 `json:"broken_at,omitempty"`
	Anchored *int64 `json:"anchored,omitempty"`
	Entries  *int   `json:"entries,omitempty"`
}

// ops are the operations Home-Mandate offers. verify_signed is missing: Home-Mandate
// does not import signed mandates yet, so it does not claim the class signatures.
var ops = []string{"validate", "evaluate", "select", "succession", "verify_audit", "entry_digest"}

// serveProcess answers requests from r, one JSON object per line, until r ends.
func serveProcess(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var req request
		resp := response{Error: "malformed request"}
		if err := json.Unmarshal(scanner.Bytes(), &req); err == nil {
			resp = answer(ctx, req)
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

func answer(ctx context.Context, req request) response {
	switch req.Op {
	case "capabilities":
		return response{Name: "Home-Mandate", Version: "v0", Ops: ops}
	case "validate":
		if req.Mandate == nil {
			return response{Error: "missing mandate"}
		}
		// The mandate store accepts exactly what the reference evaluator accepts.
		m, err := evaluator.Parse([]byte(*req.Mandate))
		return response{Valid: ptr(err == nil), Digest: m.Digest()}
	case "evaluate":
		return evaluate(ctx, req)
	case "select":
		return selectMandate(ctx, req)
	case "succession":
		return succession(req)
	case "verify_audit":
		return verifyAudit(req)
	case "entry_digest":
		if req.Entry == nil {
			return response{Error: "missing entry"}
		}
		digest, err := specaudit.Digest([]byte(*req.Entry))
		return response{Valid: ptr(err == nil), Digest: digest}
	}
	return response{Error: "unsupported"}
}

func ptr[T any](v T) *T { return &v }

// evaluate decides one request on one stored mandate through the PDP. A revoked mandate
// never reaches Home-Mandate's PDP (it is no candidate of the selection); the evaluation
// of one is the reference evaluator's, which the PDP uses as well.
func evaluate(ctx context.Context, req request) response {
	if req.Mandate == nil || req.Request == nil {
		return response{Error: "missing mandate and request"}
	}
	doc := []byte(*req.Mandate)
	at, ok := pointInTime(req.Request.Time)
	m, _ := evaluator.Parse(doc)
	if !ok {
		return invalidRequest(m)
	}
	if req.Request.Revoked {
		parameters, ok := integers(req.Request.Parameters)
		if !ok {
			return invalidRequest(m)
		}
		r := req.Request.Resource
		res := evaluator.Evaluate(m, evaluator.Request{Resource: evaluator.Resource{EntityID: r.EntityID, Category: r.Category, Area: r.Area,
			Critical: r.Critical}, Action: req.Request.Action, Parameters: parameters, Time: at, TimeZone: req.Request.Timezone,
			Status: evaluator.StatusRevoked})
		return fromResult(res)
	}
	clientID, principal := subjectOf(doc)
	s := state{Mandates: []storedMandate{{Mandate: *req.Mandate}}, Directory: directoryOf(req.Request.Resource), Timezone: req.Request.Timezone}
	out := decide(ctx, newPDP(s, principal, at), clientID, principal, req.Request.Resource, req.Request.Action, req.Request.Parameters)
	out.Selected = nil
	return response{outcome: out}
}

// selectMandate decides one request among stored mandates through the PDP.
func selectMandate(ctx context.Context, req request) response {
	if req.Subject == nil || req.Request == nil {
		return response{Error: "missing subject and request"}
	}
	at, ok := pointInTime(req.Request.Time)
	if !ok {
		return response{outcome: outcome{Decision: string(evaluator.Deny), Reason: string(evaluator.ReasonInvalidRequest)}}
	}
	s := state{Mandates: req.Mandates, Directory: directoryOf(req.Request.Resource), Timezone: req.Request.Timezone}
	p := newPDP(s, req.Subject.Principal, at)
	return response{outcome: decide(ctx, p, req.Subject.ClientID, req.Subject.Principal, req.Request.Resource, req.Request.Action, req.Request.Parameters)}
}

// succession is the check the mandate store makes before it stores a new version.
func succession(req request) response {
	if req.Stored == nil || req.Offered == nil {
		return response{Error: "missing stored and offered"}
	}
	stored, err := evaluator.Parse([]byte(*req.Stored))
	if err != nil {
		return response{Error: "stored mandate is invalid"}
	}
	offered, _ := evaluator.Parse([]byte(*req.Offered))
	return response{Accept: ptr(evaluator.CheckSuccessor(stored, offered) == nil)}
}

// verifyAudit checks a log as internal/audit checks the stored one and its export.
func verifyAudit(req request) response {
	var anchor *specaudit.Anchor
	if len(req.Keys) > 0 {
		keys, err := jws.ParseJWKS(req.Keys)
		if err != nil {
			return response{Error: "keys: " + err.Error()}
		}
		anchor = &specaudit.Anchor{Keys: keys, LogID: req.LogID}
	}
	var r specaudit.Result
	var err error
	switch {
	case req.JSONL != nil && anchor != nil:
		r, err = specaudit.VerifyJSONLinesAnchored(strings.NewReader(*req.JSONL), *anchor)
	case req.JSONL != nil:
		r, err = specaudit.VerifyJSONLines(strings.NewReader(*req.JSONL))
	case anchor != nil:
		r, err = specaudit.VerifyAnchored(texts(req.Entries), *anchor)
	default:
		r, err = specaudit.Verify(texts(req.Entries))
	}
	if err != nil {
		return response{Error: err.Error()}
	}
	resp := response{Valid: ptr(r.Valid), Entries: ptr(r.Entries)}
	if !r.Valid {
		resp.BrokenAt = ptr(r.BrokenAt)
	} else if anchor != nil {
		resp.Anchored = ptr(r.AnchoredSeq)
	}
	return resp
}

func texts(entries []string) [][]byte {
	out := make([][]byte, len(entries))
	for i, e := range entries {
		out[i] = []byte(e)
	}
	return out
}

// pointInTime parses the clock of a case; the PEP cannot express any other time.
func pointInTime(text string) (time.Time, bool) {
	at, err := time.Parse(time.RFC3339, text)
	return at, err == nil
}

// integers converts parameters as the PDP does for a revoked mandate's evaluation.
func integers(raw map[string]json.Number) (map[string]int64, bool) {
	out := make(map[string]int64, len(raw))
	for name, n := range raw {
		i, err := n.Int64()
		if err != nil {
			f, ferr := n.Float64()
			if ferr != nil || f != float64(int64(f)) {
				return nil, false
			}
			i = int64(f)
		}
		out[name] = i
	}
	return out, true
}

// invalidRequest is the result for a request the PEP cannot express; an invalid mandate
// comes first (SPEC-v0 section 4.1).
func invalidRequest(m *evaluator.Mandate) response {
	if m == nil {
		return fromResult(evaluator.Evaluate(nil, evaluator.Request{}))
	}
	return response{outcome: outcome{Decision: string(evaluator.Deny), Reason: string(evaluator.ReasonInvalidRequest), MandateDigest: m.Digest()}}
}

func fromResult(r evaluator.Result) response {
	out := outcome{Decision: string(r.Decision), Reason: string(r.Reason), MandateDigest: r.MandateDigest}
	if r.RuleID != "" {
		out.RuleID = &r.RuleID
	}
	if r.Approval != nil {
		out.ApprovalTimeout, out.Approvers = r.Approval.Timeout, r.Approval.Approvers
	}
	return response{outcome: out}
}

// subjectOf reads agent and principal of a mandate, also of an invalid one; a document
// that names neither is a candidate for every agent anyway.
func subjectOf(doc []byte) (clientID, principal string) {
	var m struct {
		Principal string `json:"principal"`
		Agent     struct {
			ClientID string `json:"client_id"`
		} `json:"agent"`
	}
	_ = json.Unmarshal(doc, &m)
	clientID, principal = m.Agent.ClientID, m.Principal
	if clientID == "" {
		clientID = "conformance:unknown"
	}
	if principal == "" {
		principal = "household:conformance"
	}
	return clientID, principal
}

// directoryOf is the directory with the case's resource; a resource without category is
// one the directory does not contain (SPEC-v0 section 4).
func directoryOf(r resource) []resource {
	if r.Category == "" {
		return nil
	}
	return []resource{r}
}
