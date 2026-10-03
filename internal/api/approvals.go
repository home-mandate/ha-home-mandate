// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
)

// requestIDPattern is the form of an approval request ID in the UI (128 bits in hex).
var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

const (
	historyLimit = 50
	// answerLimit bounds answers per person: guessing IDs is pointless (128 bits) and
	// costs a request each.
	answerLimit  = 20
	answerPeriod = time.Minute
)

// answerWait bounds how long an answer waits for the audit entry of the outcome (after
// an approval the action runs first); the client gives up after 15 s. A variable for tests.
var answerWait = 12 * time.Second

type wireAgentRef struct {
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name"`
}

type wireParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type wireApprovalRequest struct {
	ID         string       `json:"id"`
	Agent      wireAgentRef `json:"agent"`
	EntityID   string       `json:"entity_id"`
	DeviceName string       `json:"device_name"`
	Area       *string      `json:"area"`
	Action     string       `json:"action"`
	Critical   bool         `json:"critical"`
	Reason     *string      `json:"reason"`
	Params     []wireParam  `json:"params"`
	Recipients []string     `json:"recipients"`
	CreatedAt  string       `json:"created_at"`
	ExpiresAt  string       `json:"expires_at"`
	CanAnswer  bool         `json:"can_answer"`
}

type wireHistoryEntry struct {
	Seq        int64        `json:"seq"`
	Agent      wireAgentRef `json:"agent"`
	EntityID   string       `json:"entity_id"`
	DeviceName string       `json:"device_name"`
	Action     string       `json:"action"`
	Outcome    string       `json:"outcome"`
	ByName     *string      `json:"by_name"`
	Via        string       `json:"via,omitempty"`
	CreatedAt  string       `json:"created_at"`
	AnsweredAt string       `json:"answered_at"`
}

type wireApprovals struct {
	Open    []wireApprovalRequest `json:"open"`
	History []wireHistoryEntry    `json:"history"`
}

// presentRequest is an open request as user sees it. The texts of the agent are cleaned
// as in the push (security review S1); can_answer is checked again on the answer.
func (s *Server) presentRequest(ctx context.Context, o approval.Open, user string, approvers []approval.Approver) wireApprovalRequest {
	req := o.Request
	out := wireApprovalRequest{ID: o.ID, Agent: wireAgentRef{ClientID: req.ClientID, DisplayName: req.Agent}, EntityID: req.EntityID,
		DeviceName: approval.ShownText(req.Device, approval.ShownNameMax), Area: optional(req.Area), Action: req.Action,
		Critical: req.Critical, Reason: optional(approval.ShownText(req.Reason, approval.ShownReasonMax)),
		Params: []wireParam{}, Recipients: []string{}, CreatedAt: *formatTime(o.CreatedAt), ExpiresAt: *formatTime(o.ExpiresAt)}
	for _, p := range approval.ShownParams(req.Params) {
		out.Params = append(out.Params, wireParam{Name: p.Name, Value: p.Value})
	}
	for _, id := range o.Recipients {
		if n := s.users.name(ctx, id); n != nil {
			out.Recipients = append(out.Recipients, *n)
		} else {
			out.Recipients = append(out.Recipients, id)
		}
	}
	if slices.Contains(o.UIUsers, user) {
		i := slices.IndexFunc(approvers, func(a approval.Approver) bool { return a.UserID == user })
		// The person is an administrator (checked for every request and every 60 s on the event stream).
		out.CanAnswer = i >= 0 && approvers[i].Channels(req.Critical, true).UI
	}
	return out
}

func (s *Server) getApprovals(r *request) (any, error) {
	approvers, err := s.cfg.Approvers.List(r.Context())
	if err != nil {
		return nil, err
	}
	out := wireApprovals{Open: []wireApprovalRequest{}, History: []wireHistoryEntry{}}
	for _, o := range s.cfg.Approvals.Open() {
		out.Open = append(out.Open, s.presentRequest(r.Context(), o, r.user, approvers))
	}
	history, err := s.cfg.Log.ApprovalHistory(r.Context(), historyLimit)
	if err != nil {
		return nil, err
	}
	for _, e := range history {
		h, err := s.historyEntry(r.Context(), e.Seq, e.RecordedAt, e.Entry)
		if err != nil {
			return nil, err
		}
		out.History = append(out.History, h)
	}
	return out, nil
}

// errNoRequest means an entry has no request, so it cannot have ended one.
var errNoRequest = errors.New("api: entry without a request")

// historySource is the part of a decision entry the history shows.
type historySource struct {
	Agent struct {
		ClientID    string `json:"client_id"`
		DisplayName string `json:"display_name"`
	} `json:"agent"`
	Request struct {
		Time     time.Time `json:"time"`
		Resource struct {
			EntityID string `json:"entity_id"`
		} `json:"resource"`
		Action string `json:"action"`
	} `json:"request"`
	Approval *struct {
		Outcome string    `json:"outcome"`
		By      string    `json:"by"`
		Via     string    `json:"via"`
		At      time.Time `json:"at"`
	} `json:"approval"`
	Result struct {
		DeniedBy string `json:"denied_by"`
	} `json:"result"`
}

// historyEntry maps a decision entry that ended an approval request; one without an
// approval was ended by the emergency stop or a revocation (decision F1).
func (s *Server) historyEntry(ctx context.Context, seq int64, recorded time.Time, entry json.RawMessage) (wireHistoryEntry, error) {
	var src historySource
	if err := json.Unmarshal(entry, &src); err != nil {
		return wireHistoryEntry{}, err
	}
	if src.Request.Time.IsZero() || recorded.IsZero() {
		return wireHistoryEntry{}, errNoRequest
	}
	out := wireHistoryEntry{Seq: seq, Agent: wireAgentRef{ClientID: src.Agent.ClientID, DisplayName: src.Agent.DisplayName},
		EntityID: src.Request.Resource.EntityID, DeviceName: s.deviceName(src.Request.Resource.EntityID), Action: src.Request.Action,
		CreatedAt: *formatTime(src.Request.Time), AnsweredAt: *formatTime(recorded)}
	switch {
	case src.Approval != nil:
		out.Outcome, out.Via = src.Approval.Outcome, src.Approval.Via
		if at := formatTime(src.Approval.At); at != nil {
			out.AnsweredAt = *at
		}
		out.ByName = s.users.name(ctx, src.Approval.By)
	case src.Result.DeniedBy == audit.DeniedByEmergencyStop:
		out.Outcome = "emergency_stop"
	default:
		out.Outcome = "revoked"
	}
	return out, nil
}

// deviceName is the device's name in the catalog now, else its entity ID.
func (s *Server) deviceName(entityID string) string {
	if d, ok := s.cfg.Catalog.Lookup(entityID); ok && d.Name() != "" {
		return d.Name()
	}
	return entityID
}

// historyOf builds the history entry of an entry that was just written.
func (s *Server) historyOf(ctx context.Context, seq int64, at time.Time, e audit.Entry) (wireHistoryEntry, error) {
	data, err := json.Marshal(struct {
		Agent    *audit.Agent    `json:"agent"`
		Request  *audit.Request  `json:"request"`
		Approval *audit.Approval `json:"approval,omitempty"`
		Result   *audit.Result   `json:"result"`
	}{e.Agent, e.Request, e.Approval, e.Result})
	if err != nil {
		return wireHistoryEntry{}, err
	}
	return s.historyEntry(ctx, seq, at, data)
}

// answerApproval takes an answer given in the UI (decision F2). An unknown or ended
// request and one the person may not answer here look alike (not_found), so IDs cannot
// be probed; a refused answer leaves the request open.
func (s *Server) answerApproval(r *request) (any, error) {
	id := r.PathValue("id")
	if !requestIDPattern.MatchString(id) {
		return nil, fail(codeNotFound)
	}
	var in struct {
		Approve *bool `json:"approve"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.Approve == nil {
		return nil, failField(codeInvalidInput, "/approve")
	}
	if ok, wait := s.limits.allow("answer:"+r.user, answerLimit, answerPeriod); !ok {
		return nil, failRetry(codeRateLimited, wait)
	}
	closed := s.answers.add(id)
	defer s.answers.remove(id, closed)
	if _, err := s.cfg.Approvals.Answer(r.Context(), id, r.user, *in.Approve); err != nil {
		if errors.Is(err, approval.ErrNotPending) || errors.Is(err, approval.ErrNotApprover) || errors.Is(err, approval.ErrChannel) {
			return nil, fail(codeNotFound)
		}
		return nil, err
	}
	timer := time.NewTimer(answerWait)
	defer timer.Stop()
	select {
	case entry := <-closed:
		return entry, nil
	case <-timer.C:
		// The answer counted; its outcome is not written yet. The UI says it is unclear
		// and shows the outcome once the history has it.
		return nil, fail(codeUnavailable)
	case <-r.Context().Done():
		return nil, fail(codeUnavailable)
	}
}

// waiters hand the history entry of a closed request to answers waiting for it.
type waiters struct {
	mu   sync.Mutex
	byID map[string][]chan wireHistoryEntry
}

func newWaiters() *waiters { return &waiters{byID: map[string][]chan wireHistoryEntry{}} }

func (w *waiters) add(id string) chan wireHistoryEntry {
	ch := make(chan wireHistoryEntry, 1)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.byID[id] = append(w.byID[id], ch)
	return ch
}

func (w *waiters) remove(id string, ch chan wireHistoryEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := slices.DeleteFunc(w.byID[id], func(c chan wireHistoryEntry) bool { return c == ch })
	if len(list) == 0 {
		delete(w.byID, id)
		return
	}
	w.byID[id] = list
}

func (w *waiters) done(id string, entry wireHistoryEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ch := range w.byID[id] {
		select {
		case ch <- entry:
		default:
		}
	}
}

// ApprovalOpened announces a request that was just delivered (approval.Config.OnOpened).
// It returns at once; the announcement follows in order with the others.
func (s *Server) ApprovalOpened(o approval.Open) {
	s.later(func() { s.announceOpened(o) })
}

func (s *Server) announceOpened(o approval.Open) {
	// Answered on a phone meanwhile: announcing it now would show a request that is gone.
	if !slices.ContainsFunc(s.cfg.Approvals.Open(), func(open approval.Open) bool { return open.ID == o.ID }) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), usersTimeout)
	defer cancel()
	approvers, err := s.cfg.Approvers.List(ctx)
	if err != nil {
		s.cfg.Logger.Warn("approvers unreadable, request announced without answer in the UI", "error", err)
	}
	s.hub.publishFor(func(user string) any {
		return event{Type: "approval.opened", Request: ptr(s.presentRequest(ctx, o, user, approvers))}
	})
}

// AuditCommitted is called for every entry the gateway writes (audit.Log.OnCommit). An
// entry that ends an approval request closes it in every open UI and answers a waiting
// answer. It returns at once.
func (s *Server) AuditCommitted(seq int64, e audit.Entry) {
	if e.ApprovalID == "" {
		return
	}
	at := s.now()
	s.later(func() { s.announceClosed(seq, at, e) })
}

func (s *Server) announceClosed(seq int64, at time.Time, e audit.Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), usersTimeout)
	defer cancel()
	entry, err := s.historyOf(ctx, seq, at, e)
	if err != nil {
		s.cfg.Logger.Error("closed approval request not announced", "error", err)
		return
	}
	s.answers.done(e.ApprovalID, entry)
	s.publish(event{Type: "approval.closed", ID: e.ApprovalID, Entry: &entry})
}

func ptr[T any](v T) *T { return &v }
