// SPDX-License-Identifier: AGPL-3.0-or-later

// Package approval asks humans to confirm actions with the decision ask (ARCHITECTURE
// section 7). An actionable notification goes to every device of the approvers of the
// mandate; the answer arrives as a mobile_app_notification_action event whose
// context.user_id must belong to one of them. Each request has a 128-bit nonce that is
// valid exactly once; no answer within the timeout means deny. An answer from anyone
// else ends the request as invalid_response and warns the approvers (decision W8).
//
// Approvers who are Home Assistant administrators may also answer in the Home-Mandate
// UI (decision F2), with a separate random request ID, never the nonce; critical
// actions only if they chose so. The first answer on any channel counts. Revoking an
// agent or the emergency stop ends open requests at once (decision F1).
package approval

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/i18n"
)

// Outcomes of an approval request (SPEC-v0 section 9.1).
const (
	OutcomeApproved        = "approved"
	OutcomeRejected        = "rejected"
	OutcomeTimeout         = "timeout"
	OutcomeInvalidResponse = "invalid_response"
	// OutcomeCancelled is no outcome of the specification: the request ended without
	// an answer because the agent was revoked or the emergency stop was activated. The
	// audit entry then has no approval, only the denial.
	OutcomeCancelled = "cancelled"
)

// Channels an answer came through (audit approval.via).
const (
	ViaPush = "push"
	ViaUI   = "ui"
)

// ErrNoApprover means no approver of the mandate is configured or none could be
// notified; the request must be denied.
var ErrNoApprover = errors.New("approval: no approver can be reached")

// Errors of an answer in the UI; the request stays open with all of them.
var (
	// ErrNotPending means there is no open request with this ID (any more).
	ErrNotPending = errors.New("approval: no such open request")
	// ErrNotApprover means the user is no approver of this request.
	ErrNotApprover = errors.New("approval: not an approver of this request")
	// ErrChannel means the user may not answer this request in the UI: no UI channel,
	// no administrator (or the check failed), or a critical action without UICritical.
	ErrChannel = errors.New("approval: answering this request in the UI is not allowed")
)

const (
	approvePrefix = "HM_APPROVE_"
	denyPrefix    = "HM_DENY_"
	nonceBytes    = 16
	bellPrefix    = "hm_approval_"

	maxReason = 200 // runes of the agent's reason shown to the human
	maxName   = 80  // runes of agent and device names

	warnTimeout = 10 * time.Second
	unknownUser = "unknown"

	defaultMaxTimeout = 2 * time.Minute
)

var noncePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Interfaces to the rest of Home-Mandate.
type (
	// Notifier sends push notifications through Home Assistant.
	Notifier interface {
		Notify(ctx context.Context, service string, n ha.Notification) error
	}
	// ApproverList returns the configured approvers.
	ApproverList interface {
		List(ctx context.Context) ([]Approver, error)
	}
	// Bell shows a persistent notification in Home Assistant (B2), keyed by the
	// request ID, and removes it again.
	Bell interface {
		Ring(ctx context.Context, id string, n ha.Notification) error
		Clear(ctx context.Context, id string) error
	}
)

// Config wires the service.
type Config struct {
	Approvers ApproverList
	Notifier  Notifier
	// Language is the household language for approvers without their own setting.
	Language func() i18n.Lang
	// MaxTimeout is the upper limit of a wait (HM_APPROVAL_TIMEOUT); a mandate may only
	// shorten it.
	MaxTimeout time.Duration
	// ServiceUser returns Home-Mandate's own Home Assistant user, which never approves:
	// whoever holds its token must not be able to answer.
	ServiceUser func() string
	// IsAdmin tells whether a user is a Home Assistant administrator now. Without it,
	// or when it fails, nobody answers in the UI.
	IsAdmin func(ctx context.Context, userID string) (bool, error)
	// Bell and BellEnabled show a neutral hint in Home Assistant while a request can be
	// answered in the UI (B2, off by default).
	Bell        Bell
	BellEnabled func() bool
	// OnOpened is called when a request was delivered and is open (shown by Open); the UI
	// announces it. It must not block.
	OnOpened func(Open)
	Logger   *slog.Logger
	Now      func() time.Time
}

// Request is one action waiting for a human.
type Request struct {
	ClientID  string         // the agent, for revocation
	Agent     string         // display name of the agent
	EntityID  string         // the device
	Area      string         // its area, empty if none
	Device    string         // friendly name or entity ID
	Action    string         // vocabulary action
	Reason    string         // the agent's claim, untrusted
	Params    map[string]any // the service data that will be sent, shown to the human
	Approvers []string
	Timeout   time.Duration // from the mandate's approval settings
	Critical  bool          // a critical action (SPEC-v0 section 5)
}

// Result is the outcome of a request; By and Via are empty for a timeout and a
// cancellation. ID is the request's ID in the UI, so that the audit entry written for
// the outcome can be matched to the request it closes (never the nonce).
type Result struct {
	ID      string
	Outcome string
	By      string
	Via     string
	At      time.Time
}

// Open is an open request as the UI shows it.
type Open struct {
	ID         string
	Request    Request
	Recipients []string // user IDs reached on at least one channel
	UIUsers    []string // user IDs who may answer in the UI
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// recipient is an approver with their channels for one request.
type recipient struct {
	Approver
	channels Channels
}

type pending struct {
	id         string
	bellID     string // separate from id: every Home Assistant user sees the bell
	seq        uint64
	approvers  map[string]bool
	recipients []recipient
	req        Request
	result     chan Result // buffered, receives exactly one result
	done       bool
	listed     bool // delivered; shown by Open
	reached    []string
	uiUsers    []string
	created    time.Time
	expires    time.Time
}

// Service sends approval requests and takes the answers.
type Service struct {
	cfg Config

	mu      sync.Mutex
	pending map[string]*pending // by SHA-256 of the nonce
	byID    map[string]*pending // by request ID (UI)
	seq     uint64
}

// New returns the service for cfg.
func New(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = defaultMaxTimeout
	}
	return &Service{cfg: cfg, pending: map[string]*pending{}, byID: map[string]*pending{}}
}

// Ask notifies the approvers of req on their channels and waits for the first valid
// answer, the timeout, a cancellation or the end of ctx. Only ErrNoApprover and errors
// reading the approvers are returned as errors; everything else is a Result.
func (s *Service) Ask(ctx context.Context, req Request) (Result, error) {
	recipients, err := s.recipients(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if len(recipients) == 0 {
		return Result{}, ErrNoApprover
	}
	p := &pending{id: newNonce(), bellID: bellPrefix + newNonce(), approvers: map[string]bool{}, recipients: recipients, req: req,
		result: make(chan Result, 1), created: s.cfg.Now()}
	p.expires = p.created.Add(s.timeout(req))
	for _, r := range recipients {
		p.approvers[r.UserID] = true
	}
	nonce := newNonce()
	key := hashKey(nonce)
	s.mu.Lock()
	s.seq++
	p.seq = s.seq
	s.pending[key], s.byID[p.id] = p, p
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		p.done = true
		delete(s.pending, key)
		delete(s.byID, p.id)
		s.mu.Unlock()
	}()

	reached, uiUsers := s.deliver(ctx, p, nonce)
	s.mu.Lock()
	if p.done { // answered on a phone or cancelled during the delivery
		s.mu.Unlock()
		return withID(<-p.result, p.id), nil
	}
	if len(reached) == 0 {
		s.mu.Unlock()
		return Result{}, fmt.Errorf("%w: no notification delivered", ErrNoApprover)
	}
	p.reached, p.uiUsers, p.listed = reached, uiUsers, true
	opened := s.openOf(p)
	s.mu.Unlock()
	if s.cfg.OnOpened != nil {
		s.cfg.OnOpened(opened)
	}
	if rung := s.ring(ctx, p); rung {
		defer s.clear(p.bellID)
	}
	return withID(s.wait(ctx, key, p), p.id), nil
}

func withID(r Result, id string) Result {
	r.ID = id
	return r
}

// recipients are the configured approvers of req who can be reached for it, each once;
// Home-Mandate's own Home Assistant user is never asked.
func (s *Service) recipients(ctx context.Context, req Request) ([]recipient, error) {
	all, err := s.cfg.Approvers.List(ctx)
	if err != nil {
		return nil, err
	}
	service := s.serviceUser()
	var out []recipient
	for _, a := range all {
		if !slices.Contains(req.Approvers, a.UserID) {
			continue
		}
		if a.UserID == service {
			s.cfg.Logger.Warn("Home-Mandate's own Home Assistant user is set up as approver; it is never asked")
			continue
		}
		ch := a.Channels(req.Critical, a.UI && s.isAdmin(ctx, a.UserID))
		if ch.Reachable() {
			out = append(out, recipient{Approver: a, channels: ch})
		}
	}
	return out, nil
}

// deliver sends the request to every device of every recipient and returns who was
// reached on at least one channel and who may answer in the UI.
func (s *Service) deliver(ctx context.Context, p *pending, nonce string) (reached, uiUsers []string) {
	for _, r := range p.recipients {
		ok := r.channels.UI
		if ok {
			uiUsers = append(uiUsers, r.UserID)
		}
		n := buildRequest(s.language(r.Approver), p.req, nonce)
		for _, device := range r.channels.Devices {
			if s.ended(p) {
				return reached, uiUsers
			}
			if err := s.cfg.Notifier.Notify(ctx, device, n); err != nil {
				s.cfg.Logger.Warn("approval request not delivered", "approver", r.UserID, "notify_service", device, "error", err)
				continue
			}
			ok = true
		}
		if ok {
			reached = append(reached, r.UserID)
		}
	}
	return reached, uiUsers
}

// ended tells whether p was answered or cancelled already.
func (s *Service) ended(p *pending) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.done
}

// ring shows the neutral bell when it is switched on and someone can answer in the UI.
func (s *Service) ring(ctx context.Context, p *pending) bool {
	if s.cfg.Bell == nil || s.cfg.BellEnabled == nil || !s.cfg.BellEnabled() || len(p.uiUsers) == 0 {
		return false
	}
	lang := s.householdLanguage()
	n := ha.Notification{Title: i18n.T(lang, i18n.ApprovalBellTitle, nil), Message: i18n.T(lang, i18n.ApprovalBellMessage, nil)}
	if err := s.cfg.Bell.Ring(ctx, p.bellID, n); err != nil {
		s.cfg.Logger.Warn("approval hint in Home Assistant not shown", "error", err)
	}
	return true // clear even after an error: the hint may have arrived
}

// clear removes the bell, also when the caller's context has ended.
func (s *Service) clear(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), warnTimeout)
	defer cancel()
	if err := s.cfg.Bell.Clear(ctx, id); err != nil {
		s.cfg.Logger.Warn("approval hint in Home Assistant not removed", "error", err)
	}
}

// timeout is the shorter of the mandate's timeout and the upper limit.
func (s *Service) timeout(req Request) time.Duration {
	if req.Timeout > 0 && req.Timeout < s.cfg.MaxTimeout {
		return req.Timeout
	}
	return s.cfg.MaxTimeout
}

// wait waits until p.expires, so that the end is the one the UI shows.
func (s *Service) wait(ctx context.Context, key string, p *pending) Result {
	timer := time.NewTimer(max(p.expires.Sub(s.cfg.Now()), 0))
	defer timer.Stop()
	select {
	case res := <-p.result:
		return res
	case <-timer.C:
	case <-ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.done { // an answer arrived at the same moment; it counts
		return <-p.result
	}
	p.done = true
	delete(s.pending, key)
	delete(s.byID, p.id)
	return Result{Outcome: OutcomeTimeout, At: s.cfg.Now()}
}

// Answer takes an answer given in the UI by user, who comes from the Ingress header
// (only from the Supervisor). The user must be an approver of the request and may
// answer there now (see ErrChannel); otherwise the request stays open.
func (s *Service) Answer(ctx context.Context, id, user string, approve bool) (Result, error) {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok || p.done || !p.listed {
		s.mu.Unlock()
		return Result{}, ErrNotPending
	}
	member, critical := p.approvers[user], p.req.Critical
	s.mu.Unlock()
	if !member || user == s.serviceUser() {
		return Result{}, ErrNotApprover
	}
	if !s.uiAllowed(ctx, user, critical) {
		return Result{}, ErrChannel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.done {
		return Result{}, ErrNotPending
	}
	p.done = true
	res := Result{ID: p.id, Outcome: OutcomeRejected, By: user, Via: ViaUI, At: s.cfg.Now()}
	if approve {
		res.Outcome = OutcomeApproved
	}
	p.result <- res
	return res, nil
}

// uiAllowed checks with the current settings and administrator rights whether user may
// answer a request in the UI. Every failure means no.
func (s *Service) uiAllowed(ctx context.Context, user string, critical bool) bool {
	all, err := s.cfg.Approvers.List(ctx)
	if err != nil {
		s.cfg.Logger.Error("approvers unreadable, answer in the UI refused", "error", err)
		return false
	}
	i := slices.IndexFunc(all, func(a Approver) bool { return a.UserID == user })
	return i >= 0 && all[i].Channels(critical, all[i].UI && s.isAdmin(ctx, user)).UI
}

func (s *Service) isAdmin(ctx context.Context, user string) bool {
	if s.cfg.IsAdmin == nil {
		return false
	}
	admin, err := s.cfg.IsAdmin(ctx, user)
	if err != nil {
		s.cfg.Logger.Warn("administrator check failed, no answer in the UI", "user_id", user, "error", err)
		return false
	}
	return admin
}

func (s *Service) serviceUser() string {
	if s.cfg.ServiceUser == nil {
		return ""
	}
	return s.cfg.ServiceUser()
}

// Open returns the open requests in the order they were asked, as copies.
func (s *Service) Open() []Open {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*pending
	for _, p := range s.byID {
		if p.listed && !p.done {
			list = append(list, p)
		}
	}
	slices.SortFunc(list, func(a, b *pending) int { return cmp.Compare(a.seq, b.seq) })
	out := make([]Open, len(list))
	for i, p := range list {
		out[i] = s.openOf(p)
	}
	return out
}

// openOf copies p; s.mu must be held.
func (s *Service) openOf(p *pending) Open {
	req := p.req
	req.Approvers, req.Params = slices.Clone(req.Approvers), maps.Clone(req.Params)
	return Open{ID: p.id, Request: req, Recipients: slices.Clone(p.reached), UIUsers: slices.Clone(p.uiUsers),
		CreatedAt: p.created, ExpiresAt: p.expires}
}

// CancelAgent ends the open requests of an agent that was revoked and returns how many.
func (s *Service) CancelAgent(clientID string) int {
	if clientID == "" {
		return 0
	}
	return s.cancel(func(p *pending) bool { return p.req.ClientID == clientID })
}

// Withdraw takes a person out of every open request, e.g. after they were removed from
// the approvers: their answer then counts as one from anyone else (on a phone it ends
// the request as invalid_response). It returns how many requests were affected.
func (s *Service) Withdraw(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.byID {
		if !p.done && p.approvers[userID] {
			delete(p.approvers, userID)
			n++
		}
	}
	return n
}

// CancelAll ends every open request (emergency stop) and returns how many.
func (s *Service) CancelAll() int {
	return s.cancel(func(*pending) bool { return true })
}

func (s *Service) cancel(match func(*pending) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.byID {
		if !p.done && match(p) {
			p.done = true
			p.result <- Result{Outcome: OutcomeCancelled, At: s.cfg.Now()}
			n++
		}
	}
	return n
}

// HandleEvent takes a mobile_app_notification_action event. It runs on the read loop of
// the Home Assistant client: it never calls the client synchronously.
func (s *Service) HandleEvent(e ha.Event) {
	var data struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return
	}
	var nonce string
	approve := false
	switch {
	case strings.HasPrefix(data.Action, approvePrefix):
		nonce, approve = strings.TrimPrefix(data.Action, approvePrefix), true
	case strings.HasPrefix(data.Action, denyPrefix):
		nonce = strings.TrimPrefix(data.Action, denyPrefix)
	default:
		return // not ours
	}
	user := e.Context.UserID
	if !noncePattern.MatchString(nonce) {
		s.cfg.Logger.Warn("approval answer discarded: malformed", "user_id", loggable(user))
		return
	}
	s.mu.Lock()
	p, ok := s.pending[hashKey(nonce)]
	if !ok || p.done {
		s.mu.Unlock()
		s.cfg.Logger.Warn("approval answer discarded: unknown, expired or already answered", "user_id", loggable(user))
		return
	}
	p.done = true
	res := Result{Outcome: OutcomeRejected, By: user, Via: ViaPush, At: s.cfg.Now()}
	switch {
	case user == "" || !p.approvers[user]:
		res.Outcome = OutcomeInvalidResponse
		if user == "" || !userIDPattern.MatchString(user) {
			res.By = unknownUser
		}
	case approve:
		res.Outcome = OutcomeApproved
	}
	p.result <- res
	s.mu.Unlock()
	if res.Outcome == OutcomeInvalidResponse {
		s.cfg.Logger.Warn("approval answer from someone who may not approve, request denied", "user_id", res.By)
		go s.warn(p, res.By)
	}
}

// warn tells the approvers of a request that someone else answered it.
func (s *Service) warn(p *pending, user string) {
	ctx, cancel := context.WithTimeout(context.Background(), warnTimeout)
	defer cancel()
	for _, r := range p.recipients {
		lang := s.language(r.Approver)
		n := ha.Notification{Title: i18n.T(lang, i18n.ApprovalInvalidTitle, nil), Message: i18n.T(lang, i18n.ApprovalInvalidMessage,
			i18n.Args{"user": user, "agent": sanitize(p.req.Agent, maxName), "device": sanitize(p.req.Device, maxName),
				"action": i18n.ActionName(lang, p.req.Action)})}
		for _, device := range r.channels.Devices {
			if err := s.cfg.Notifier.Notify(ctx, device, n); err != nil {
				s.cfg.Logger.Error("warning about an invalid approval answer not delivered", "approver", r.UserID, "error", err)
			}
		}
	}
}

func (s *Service) language(a Approver) i18n.Lang {
	if lang, ok := i18n.Parse(a.Language); ok {
		return lang
	}
	return s.householdLanguage()
}

func (s *Service) householdLanguage() i18n.Lang {
	if s.cfg.Language != nil {
		return s.cfg.Language()
	}
	return i18n.Default
}

// buildRequest is the notification of a request in lang. The agent's reason is shown
// sanitized and marked as its claim.
func buildRequest(lang i18n.Lang, req Request, nonce string) ha.Notification {
	agentName, device := sanitize(req.Agent, maxName), sanitize(req.Device, maxName)
	lines := []string{i18n.T(lang, i18n.ApprovalMessage, i18n.Args{"agent": agentName, "device": device,
		"action": i18n.ActionName(lang, req.Action)})}
	if params := formatParams(req.Params); params != "" {
		lines = append(lines, i18n.T(lang, i18n.ApprovalParams, i18n.Args{"params": params}))
	}
	if reason := sanitize(req.Reason, maxReason); reason != "" {
		lines = append(lines, i18n.T(lang, i18n.ApprovalReason, i18n.Args{"reason": reason}))
	}
	lines = append(lines, i18n.T(lang, i18n.ApprovalNoAnswer, nil))
	return ha.Notification{
		Title:   i18n.T(lang, i18n.ApprovalTitle, i18n.Args{"agent": agentName}),
		Message: strings.Join(lines, "\n"),
		Actions: []ha.NotificationAction{
			{Action: approvePrefix + nonce, Title: i18n.T(lang, i18n.ApprovalApprove, nil)},
			{Action: denyPrefix + nonce, Title: i18n.T(lang, i18n.ApprovalDeny, nil), Destructive: true},
		},
	}
}

// Shown limits of untrusted text, as in the push: the agent's reason and names.
const (
	ShownReasonMax = maxReason
	ShownNameMax   = maxName
)

// Param is one field of the service data as the push shows it.
type Param struct {
	Name  string
	Value string
}

// ShownParams returns the service data as the push shows it (security review S1): sorted
// by name, each name and value sanitized and cut to ShownNameMax.
func ShownParams(params map[string]any) []Param {
	out := make([]Param, 0, len(params))
	for _, k := range slices.Sorted(maps.Keys(params)) {
		out = append(out, Param{Name: sanitize(k, maxName), Value: sanitize(fmt.Sprint(params[k]), maxName)})
	}
	return out
}

// ShownText sanitizes untrusted text as the push does and cuts it to limit runes.
func ShownText(text string, limit int) string {
	return sanitize(text, limit)
}

// formatParams shows the service data as sorted name=value pairs, sanitized.
func formatParams(params map[string]any) string {
	parts := make([]string, 0, len(params))
	for _, k := range slices.Sorted(maps.Keys(params)) {
		parts = append(parts, sanitize(k, maxName)+"="+sanitize(fmt.Sprint(params[k]), maxName))
	}
	return sanitize(strings.Join(parts, ", "), maxReason)
}

// markup are characters that format text or build links in Markdown or HTML. The
// underscore stays: it is part of identifiers such as hvac_mode, and notifications are
// not rendered as Markdown.
const markup = "*~`[]()<>#|\\{}"

// sanitize makes untrusted text safe to show to a human: control, format and separator
// characters and markup become spaces, "://" is broken so no link can be clicked,
// whitespace is collapsed and the text is cut to limit runes.
func sanitize(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) || r == utf8.RuneError ||
			strings.ContainsRune(markup, r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(strings.ReplaceAll(b.String(), "://", " ")), " ")
	if utf8.RuneCountInString(out) > limit {
		out = string([]rune(out)[:limit-1]) + "…"
	}
	return out
}

// loggable is a user ID from Home Assistant as it may appear in the log.
func loggable(user string) string {
	if userIDPattern.MatchString(user) {
		return user
	}
	return unknownUser
}

func newNonce() string {
	var b [nonceBytes]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return hex.EncodeToString(b[:])
}

func hashKey(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
