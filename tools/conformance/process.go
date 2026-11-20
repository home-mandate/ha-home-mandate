// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	specaudit "github.com/home-mandate/spec/audit"
	"github.com/home-mandate/spec/evaluator"
	"github.com/home-mandate/spec/jws"

	"github.com/home-mandate/spec/rfc3339"
)

// maxLineBytes bounds one request: several mandates of 256 KiB inside JSON strings.
const maxLineBytes = 16 << 20

// request is one line of the process binding (SPEC-v0 section 10.2).
type request struct {
	Op      string  `json:"op"`
	Mandate *string `json:"mandate"`
	// MandateBase64 is the alternative to Mandate that carries the bytes of the document
	// (SPEC-v0 section 10.2); encoding/json reads it as base64 with padding.
	MandateBase64 []byte          `json:"mandate_base64"`
	Mandates      []storedMandate `json:"mandates"`
	Subject       *struct {
		ClientID  string `json:"client_id"`
		Principal string `json:"principal"`
	} `json:"subject"`
	Request *struct {
		Resource   resource                   `json:"resource"`
		Action     string                     `json:"action"`
		Parameters map[string]json.RawMessage `json:"parameters"`
		Time       string                     `json:"time"`
		Timezone   string                     `json:"timezone"`
		Revoked    bool                       `json:"revoked"`
	} `json:"request"`
	Stored *string `json:"stored"`
	// StoredRevoked is true if the stored mandate is revoked (succession).
	StoredRevoked bool    `json:"stored_revoked,omitempty"`
	Offered       *string `json:"offered"`
	// Keys is a JWK Set as JSON text, like a mandate (SPEC-v0 section 10.2).
	Keys    string   `json:"keys"`
	JSONL   *string  `json:"jsonl"`
	Entries []string `json:"entries"`
	// JSONLBase64 and EntriesBase64 carry bytes, like MandateBase64.
	JSONLBase64   []byte   `json:"jsonl_base64"`
	EntriesBase64 [][]byte `json:"entries_base64"`
	LogID         string   `json:"log_id"`
	// LastSeen is the position a relying party saw covered by the last checkpoint it
	// verified (SPEC-v0 section 9.5); only with Keys.
	LastSeen *position `json:"last_seen"`
	Entry    *string   `json:"entry"`
}

// position is an entry of an audit log by its seq and digest.
type position struct {
	Seq    int64  `json:"seq"`
	Digest string `json:"digest"`
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
	// The findings of SPEC-v0 section 9.4, each the seq of the first entry of its kind and
	// absent if there is none, and the first entry that broke the cadence of checkpoints.
	IntegrityBrokenAt   *int64 `json:"integrity_broken_at,omitempty"`
	ConformanceBrokenAt *int64 `json:"conformance_broken_at,omitempty"`
	UnknownVersionAt    *int64 `json:"unknown_version_at,omitempty"`
	CheckpointGapAt     *int64 `json:"checkpoint_gap_at,omitempty"`
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
		return response{Name: "Home-Mandate", Version: version, Ops: ops}
	case "validate":
		doc, bad := document(req)
		if bad != nil {
			return *bad
		}
		// The mandate store accepts exactly what the reference evaluator accepts.
		m, err := evaluator.Parse(doc)
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
		if err != nil {
			return response{Error: "entry: " + err.Error()}
		}
		return response{Digest: digest}
	}
	return response{Error: "unsupported"}
}

func ptr[T any](v T) *T { return &v }

// document is the mandate of a request: its text, or the bytes of its base64 alternative
// (SPEC-v0 section 10.2). A request carries exactly one of the two; bad is the error
// response otherwise.
func document(req request) (doc []byte, bad *response) {
	switch {
	case req.Mandate != nil && req.MandateBase64 != nil:
		return nil, &response{Error: "both mandate and mandate_base64"}
	case req.MandateBase64 != nil:
		return req.MandateBase64, nil
	case req.Mandate != nil:
		return []byte(*req.Mandate), nil
	}
	return nil, &response{Error: "missing mandate"}
}

// evaluate decides one request on one stored mandate through the PDP. A revoked mandate
// never reaches Home-Mandate's PDP (it is no candidate of the selection); the evaluation
// of one is the reference evaluator's, which the PDP uses as well.
func evaluate(ctx context.Context, req request) response {
	doc, bad := document(req)
	if bad != nil {
		return *bad
	}
	if req.Request == nil {
		return response{Error: "missing request"}
	}
	at, ok := pointInTime(req.Request.Time)
	m, _ := evaluator.Parse(doc)
	if !ok {
		return invalidRequest(m)
	}
	if req.Request.Revoked {
		parameters, ok := values(req.Request.Parameters)
		if !ok {
			return invalidRequest(m)
		}
		r := req.Request.Resource
		res := evaluator.Evaluate(m, evaluator.Request{Resource: evaluator.Resource{EntityID: r.EntityID, Category: r.Category, Areas: r.Areas,
			Critical: r.Critical}, Action: req.Request.Action, Parameters: parameters, Time: at, TimeZone: req.Request.Timezone,
			Status: evaluator.StatusRevoked})
		return fromResult(res)
	}
	clientID, principal := subjectOf(doc)
	s := state{Mandates: []storedMandate{{Mandate: string(doc)}}, Directory: directoryOf(req.Request.Resource), Timezone: req.Request.Timezone}
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
		return inexpressible(req)
	}
	s := state{Mandates: req.Mandates, Directory: directoryOf(req.Request.Resource), Timezone: req.Request.Timezone}
	p := newPDP(s, req.Subject.Principal, at)
	return response{outcome: decide(ctx, p, req.Subject.ClientID, req.Subject.Principal, req.Request.Resource, req.Request.Action, req.Request.Parameters)}
}

// succession is the check the mandate store makes before it stores a new version, and
// whether the accepted version takes effect only after a human confirmed it (SPEC-v0
// section 3.5): audit mode switched on, a weakening, or a revoked predecessor.
func succession(req request) response {
	if req.Stored == nil || req.Offered == nil {
		return response{Error: "missing stored and offered"}
	}
	stored, err := evaluator.Parse([]byte(*req.Stored))
	if err != nil {
		return response{Error: "stored mandate is invalid"}
	}
	offered, _ := evaluator.Parse([]byte(*req.Offered))
	if evaluator.CheckSuccessor(stored, offered) != nil {
		return response{Accept: ptr(false)}
	}
	status := evaluator.StatusActive
	if req.StoredRevoked {
		status = evaluator.StatusRevoked
	}
	return response{Accept: ptr(true), outcome: outcome{Confirm: evaluator.NeedsConfirmation(stored, status, time.Time{}, offered)}}
}

// verifyAudit checks a log as internal/audit checks the stored one and its export.
func verifyAudit(req request) response {
	var anchor *specaudit.Anchor
	if req.Keys != "" {
		keys, err := jws.ParseJWKS([]byte(req.Keys))
		if err != nil {
			return response{Error: "keys: " + err.Error()}
		}
		anchor = &specaudit.Anchor{Keys: keys, LogID: req.LogID}
		if req.LastSeen != nil {
			anchor.LastSeen = &specaudit.Position{Seq: req.LastSeen.Seq, Digest: req.LastSeen.Digest}
		}
	} else if req.LastSeen != nil {
		return response{Error: "last_seen without keys"}
	}
	jsonl, list, bad := logOf(req)
	if bad != nil {
		return *bad
	}
	var r specaudit.Result
	var err error
	switch {
	case jsonl != nil && anchor != nil:
		r, err = specaudit.VerifyJSONLinesAnchored(bytes.NewReader(jsonl), *anchor)
	case jsonl != nil:
		r, err = specaudit.VerifyJSONLines(bytes.NewReader(jsonl))
	case anchor != nil:
		r, err = specaudit.VerifyAnchored(list, *anchor)
	default:
		r, err = specaudit.Verify(list)
	}
	if err != nil {
		return response{Error: err.Error()}
	}
	resp := response{Valid: ptr(r.Valid), Entries: ptr(r.Entries), IntegrityBrokenAt: findingAt(r.Integrity),
		ConformanceBrokenAt: findingAt(r.Conformance), UnknownVersionAt: findingAt(r.UnknownVersion), CheckpointGapAt: findingAt(r.CheckpointGap)}
	if !r.Valid {
		resp.BrokenAt = ptr(r.BrokenAt)
	} else if anchor != nil {
		resp.Anchored = ptr(r.AnchoredSeq)
	}
	return resp
}

// logOf is the log of a request: JSON Lines (jsonl, jsonl_base64) or a list of entries
// (entries, entries_base64), exactly one of the four (SPEC-v0 section 10.2).
func logOf(req request) (jsonl []byte, list [][]byte, bad *response) {
	given := 0
	for _, g := range []bool{req.JSONL != nil, req.Entries != nil, req.JSONLBase64 != nil, req.EntriesBase64 != nil} {
		if g {
			given++
		}
	}
	if given > 1 {
		return nil, nil, &response{Error: "more than one of jsonl, entries, jsonl_base64 and entries_base64"}
	}
	switch {
	case req.JSONL != nil:
		return []byte(*req.JSONL), nil, nil
	case req.JSONLBase64 != nil:
		return req.JSONLBase64, nil, nil
	case req.EntriesBase64 != nil:
		return nil, req.EntriesBase64, nil
	}
	return nil, texts(req.Entries), nil
}

// findingAt is the seq of a finding, or nil if there is none.
func findingAt(f *specaudit.Finding) *int64 {
	if !f.Found() {
		return nil
	}
	return ptr(f.At)
}

func texts(entries []string) [][]byte {
	out := make([][]byte, len(entries))
	for i, e := range entries {
		out[i] = []byte(e)
	}
	return out
}

// inexpressible answers a selection whose point in time the PEP cannot read. The selection
// still comes first (SPEC-v0 section 4.1: no_mandate, ambiguous_mandate and invalid_mandate
// precede invalid_request), so the answer names the selected mandate; without a point in
// time a sole candidate is selected and several cannot be told apart (section 4.3). The
// PDP always has a clock, so this is the reference evaluator's selection, as for a revoked
// mandate's evaluation.
func inexpressible(req request) response {
	return response{outcome: selectWithoutTime(req.Mandates, req.Subject.ClientID, req.Subject.Principal)}
}

// selectWithoutTime is the selection of SPEC-v0 sections 4.3 and 10.2 for a request without
// a readable point in time, for both bindings.
func selectWithoutTime(mandates []storedMandate, clientID, principal string) outcome {
	invalid := outcome{Decision: string(evaluator.Deny), Reason: string(evaluator.ReasonInvalidRequest)}
	if !evaluator.ValidSubject(clientID, principal) {
		return invalid
	}
	stored := make([]evaluator.Stored, 0, len(mandates))
	for _, m := range mandates {
		status := evaluator.StatusActive
		if m.Revoked {
			status = evaluator.StatusRevoked
		}
		stored = append(stored, evaluator.NewStored([]byte(m.Mandate), status))
	}
	selected, r := evaluator.SelectWithoutTime(stored, clientID, principal)
	out := fromResult(r).outcome
	if r.Reason == evaluator.ReasonInvalidRequest && selected != nil {
		out = invalid
		out.MandateDigest = selected.Digest()
	}
	if selected != nil {
		out.Selected = ptr(selected.ID())
	}
	return out
}

// pointInTime parses the clock of a case; the PEP cannot express any other time.
func pointInTime(text string) (time.Time, bool) {
	at, err := rfc3339.Parse(text)
	return at, err == nil
}

// values reads parameters as the PDP does for a revoked mandate's evaluation: integers
// by their exact value, and strings.
func values(raw map[string]json.RawMessage) (map[string]evaluator.Value, bool) {
	out := make(map[string]evaluator.Value, len(raw))
	for name, data := range raw {
		var v evaluator.Value
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, false
		}
		out[name] = v
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
	out := outcome{Decision: string(r.Decision), Reason: string(r.Reason), MandateDigest: r.MandateDigest, LimitedRules: r.LimitedRules}
	if r.RuleID != "" {
		out.RuleID = &r.RuleID
	}
	if r.Approval != nil {
		out.ApprovalTimeout, out.Approvers = r.Approval.Timeout, r.Approval.Approvers
	}
	if r.NotEnforced {
		out.Enforced = new(bool)
		out.Confirm = r.Confirm
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
