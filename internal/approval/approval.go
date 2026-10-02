// SPDX-License-Identifier: AGPL-3.0-or-later

// Package approval asks humans to confirm actions with the decision ask (ARCHITECTURE
// section 7). An actionable notification goes to the approvers of the mandate; the
// answer arrives as a mobile_app_notification_action event whose context.user_id must
// belong to one of them. Each request has a 128-bit nonce that is valid exactly once;
// no answer within the timeout means deny. An answer from anyone else ends the request
// as invalid_response and warns the approvers (decision W8).
package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
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
)

// ErrNoApprover means no approver of the mandate is configured or none could be
// notified; the request must be denied.
var ErrNoApprover = errors.New("approval: no approver can be reached")

const (
	approvePrefix = "HM_APPROVE_"
	denyPrefix    = "HM_DENY_"
	nonceBytes    = 16

	maxReason = 200 // runes of the agent's reason shown to the human
	maxName   = 80  // runes of agent and device names

	warnTimeout = 10 * time.Second
	unknownUser = "unknown"
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
	Logger     *slog.Logger
	Now        func() time.Time
}

// Request is one action waiting for a human.
type Request struct {
	Agent     string // display name of the agent
	Device    string // friendly name or entity ID
	Action    string // vocabulary action
	Reason    string // the agent's claim, untrusted
	Approvers []string
	Timeout   time.Duration // from the mandate's approval settings
}

// Result is the outcome of a request; By is empty for a timeout.
type Result struct {
	Outcome string
	By      string
	At      time.Time
}

type pending struct {
	approvers  map[string]bool
	recipients []Approver
	req        Request
	result     chan Result // buffered, receives exactly one result
	done       bool
}

// Service sends approval requests and takes the answers.
type Service struct {
	cfg Config

	mu      sync.Mutex
	pending map[string]*pending // by SHA-256 of the nonce
}

// New returns the service for cfg.
func New(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg, pending: map[string]*pending{}}
}

// Ask notifies the approvers of req that are configured and waits for the first valid
// answer, the timeout or the end of ctx. Only ErrNoApprover and errors reading the
// approvers are returned as errors; everything else is a Result.
func (s *Service) Ask(ctx context.Context, req Request) (Result, error) {
	all, err := s.cfg.Approvers.List(ctx)
	if err != nil {
		return Result{}, err
	}
	p := &pending{approvers: map[string]bool{}, req: req, result: make(chan Result, 1)}
	for _, a := range all {
		for _, id := range req.Approvers {
			if a.UserID == id {
				p.approvers[id] = true
				p.recipients = append(p.recipients, a)
			}
		}
	}
	if len(p.recipients) == 0 {
		return Result{}, ErrNoApprover
	}
	nonce := newNonce()
	key := hashKey(nonce)
	s.mu.Lock()
	s.pending[key] = p
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
	}()

	delivered := 0
	for _, r := range p.recipients {
		if err := s.cfg.Notifier.Notify(ctx, r.NotifyService, buildRequest(s.language(r), req, nonce)); err != nil {
			s.cfg.Logger.Warn("approval request not delivered", "approver", r.UserID, "notify_service", r.NotifyService, "error", err)
			continue
		}
		delivered++
	}
	if delivered == 0 {
		return Result{}, fmt.Errorf("%w: no notification delivered", ErrNoApprover)
	}
	return s.wait(ctx, key, p), nil
}

func (s *Service) wait(ctx context.Context, key string, p *pending) Result {
	timeout := s.cfg.MaxTimeout
	if req := p.req.Timeout; req > 0 && req < timeout {
		timeout = req
	}
	timer := time.NewTimer(timeout)
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
	return Result{Outcome: OutcomeTimeout, At: s.cfg.Now()}
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
		s.cfg.Logger.Warn("approval answer discarded: malformed", "user_id", user)
		return
	}
	s.mu.Lock()
	p, ok := s.pending[hashKey(nonce)]
	if !ok || p.done {
		s.mu.Unlock()
		s.cfg.Logger.Warn("approval answer discarded: unknown, expired or already answered", "user_id", user)
		return
	}
	p.done = true
	res := Result{Outcome: OutcomeRejected, By: user, At: s.cfg.Now()}
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
		lang := s.language(r)
		n := ha.Notification{Title: i18n.T(lang, i18n.ApprovalInvalidTitle, nil), Message: i18n.T(lang, i18n.ApprovalInvalidMessage,
			i18n.Args{"user": user, "agent": sanitize(p.req.Agent, maxName), "device": sanitize(p.req.Device, maxName),
				"action": i18n.ActionName(lang, p.req.Action)})}
		if err := s.cfg.Notifier.Notify(ctx, r.NotifyService, n); err != nil {
			s.cfg.Logger.Error("warning about an invalid approval answer not delivered", "approver", r.UserID, "error", err)
		}
	}
}

func (s *Service) language(a Approver) i18n.Lang {
	if lang, ok := i18n.Parse(a.Language); ok {
		return lang
	}
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

// markup are characters that format text or build links in Markdown or HTML.
const markup = "*_~`[]()<>#|\\{}"

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

func newNonce() string {
	var b [nonceBytes]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return hex.EncodeToString(b[:])
}

func hashKey(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
