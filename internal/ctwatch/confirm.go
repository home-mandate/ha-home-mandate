// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// An acknowledgement ("This is mine") is asked for by an administrator in the UI and
// takes effect only when an approver confirms it, like a critical request: on a device
// that asks for unlocking before a button counts, or in the UI with the right to answer
// critical requests there (approval.Approver.Channels). It lives in memory; after a
// restart the alarm stays and can be acknowledged again.

var (
	// ErrNotAlarming means the certificate raises no alarm and is no past exposure.
	ErrNotAlarming = errors.New("ctwatch: certificate raises no alarm")
	// ErrNoApprover means no approver can be reached for a confirmation.
	ErrNoApprover = errors.New("ctwatch: no approver can confirm")
	// ErrNotPending means the confirmation does not exist or has ended.
	ErrNotPending = errors.New("ctwatch: no such confirmation")
	// ErrNotApprover means the user is not an approver of the confirmation.
	ErrNotApprover = errors.New("ctwatch: not an approver")
	// ErrChannel means the approver may not answer in the UI now.
	ErrChannel = errors.New("ctwatch: may not answer in the UI")
)

const (
	actionYes = "HM_CT_YES_"
	actionNo  = "HM_CT_NO_"
	tagPrefix = "hm_ct_"
	idBytes   = 16
	// defaultConfirmTimeout is used when no timeout is configured.
	defaultConfirmTimeout = 10 * time.Minute
	// settleTimeout bounds what is done after an answer (storing, notifications).
	settleTimeout = 30 * time.Second
)

var noncePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type pendingAck struct {
	id, nonce   string
	digest      Digest
	cert        Stored
	requestedBy string
	expires     time.Time
	approvers   map[string]bool
	uiUsers     []string
	delivered   []string // notify services the request reached
}

// Pending is a confirmation as the UI shows it.
type Pending struct {
	ID          string
	Digest      Digest
	RequestedBy string
	Expires     time.Time
	// UIUsers may answer in the UI.
	UIUsers []string
}

func newHex() string {
	b := make([]byte, idBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *pendingAck) public() Pending {
	return Pending{ID: p.id, Digest: p.digest, RequestedBy: p.requestedBy, Expires: p.expires, UIUsers: slices.Clone(p.uiUsers)}
}

// recipient is an approver and how they are reached for a confirmation.
type recipient struct {
	approval.Approver
	channels approval.Channels
}

func (w *Watcher) isAdmin(ctx context.Context, user string) bool {
	if w.cfg.IsAdmin == nil {
		return false
	}
	admin, err := w.cfg.IsAdmin(ctx, user)
	if err != nil {
		w.cfg.Logger.Warn("certificate monitoring: administrator rights unknown, taken as none", "error", err)
		return false
	}
	return admin
}

// recipients are the approvers who can be reached for a confirmation, as for a critical
// request.
func (w *Watcher) recipients(ctx context.Context) ([]recipient, error) {
	if w.cfg.Approvers == nil {
		return nil, nil
	}
	all, err := w.cfg.Approvers.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []recipient
	for _, a := range all {
		if a.UserID == w.serviceUser() {
			continue
		}
		ch := a.Channels(true, a.UI && w.isAdmin(ctx, a.UserID))
		if ch.Reachable() {
			out = append(out, recipient{Approver: a, channels: ch})
		}
	}
	return out, nil
}

// RequestAck asks the approvers to confirm that the certificate with digest is the
// household's. A confirmation already waiting for it is returned as it is.
func (w *Watcher) RequestAck(ctx context.Context, digest Digest, user string) (Pending, error) {
	cert, err := w.alarming(ctx, digest)
	if err != nil {
		return Pending{}, err
	}
	w.mu.Lock()
	for _, p := range w.pending {
		if p.digest == digest && w.cfg.Now().Before(p.expires) {
			w.mu.Unlock()
			return p.public(), nil
		}
	}
	w.mu.Unlock()
	recipients, err := w.recipients(ctx)
	if err != nil {
		return Pending{}, err
	}
	p := &pendingAck{id: newHex(), nonce: newHex(), digest: digest, cert: cert, requestedBy: user,
		expires: w.cfg.Now().Add(w.confirmTimeout()), approvers: map[string]bool{}}
	for _, r := range recipients {
		p.approvers[r.UserID] = true
		if r.channels.UI {
			p.uiUsers = append(p.uiUsers, r.UserID)
		}
	}
	p.delivered = w.deliver(ctx, p, recipients)
	if len(p.delivered) == 0 && len(p.uiUsers) == 0 {
		return Pending{}, ErrNoApprover
	}
	w.mu.Lock()
	w.pending[p.id] = p
	w.mu.Unlock()
	w.cfg.Logger.Info("certificate acknowledgement asked for, waiting for an approver", "digest", digest.String(), "by", user)
	w.cfg.OnChange()
	return p.public(), nil
}

// alarming returns the stored certificate if it raises an alarm or is a past exposure.
func (w *Watcher) alarming(ctx context.Context, digest Digest) (Stored, error) {
	now := w.cfg.Now()
	since, _, err := w.cfg.Store.WatchSince(ctx, w.cfg.Name, now)
	if err != nil {
		return Stored{}, err
	}
	all, err := w.decideAll(ctx, since, now)
	if err != nil {
		return Stored{}, err
	}
	for _, d := range all {
		if d.Digest == digest && d.Result != None {
			return d.Stored, nil
		}
	}
	return Stored{}, ErrNotAlarming
}

func (w *Watcher) confirmTimeout() time.Duration {
	if w.cfg.ConfirmTimeout == nil || w.cfg.ConfirmTimeout() <= 0 {
		return defaultConfirmTimeout
	}
	return w.cfg.ConfirmTimeout()
}

func (w *Watcher) userName(ctx context.Context, user string) string {
	if w.cfg.UserName != nil {
		if name := w.cfg.UserName(ctx, user); name != "" {
			return name
		}
	}
	return user
}

// deliver sends the confirmation to the devices of the recipients and returns the
// notify services it reached.
func (w *Watcher) deliver(ctx context.Context, p *pendingAck, recipients []recipient) []string {
	if w.cfg.Notifier == nil {
		return nil
	}
	who := w.userName(ctx, p.requestedBy)
	link := w.link()
	var reached []string
	for _, r := range recipients {
		lang := w.language(r.Approver)
		args := certArgs(w.cfg.Name, p.cert)
		args["user"] = who
		n := ha.Notification{Title: i18n.T(lang, i18n.CTConfirmTitle, nil), Message: i18n.T(lang, i18n.CTConfirmMessage, args),
			Actions: []ha.NotificationAction{{Action: actionYes + p.nonce, Title: i18n.T(lang, i18n.CTConfirmYes, nil)},
				{Action: actionNo + p.nonce, Title: i18n.T(lang, i18n.CTConfirmNo, nil), Destructive: true}},
			Tag: tagPrefix + p.nonce, Sticky: true, URL: link}
		for _, device := range r.channels.Devices {
			if err := w.cfg.Notifier.Notify(ctx, device, n); err != nil {
				w.cfg.Logger.Warn("certificate acknowledgement not delivered", "approver", r.UserID, "notify_service", device, "error", err)
				continue
			}
			reached = append(reached, device)
		}
	}
	return reached
}

// AnswerAck takes an answer given in the UI by user (from the Ingress header or the
// sign-in of direct mode).
func (w *Watcher) AnswerAck(ctx context.Context, id, user string, approve bool) error {
	w.mu.Lock()
	p, ok := w.pending[id]
	if !ok || !w.cfg.Now().Before(p.expires) || w.stopped {
		w.mu.Unlock()
		return ErrNotPending
	}
	member := p.approvers[user]
	w.mu.Unlock()
	if !member || user == w.serviceUser() {
		return ErrNotApprover
	}
	if !w.uiAllowed(ctx, user) {
		return ErrChannel
	}
	p, ok = w.take(id)
	if !ok {
		return ErrNotPending
	}
	w.settle(ctx, p, user, ViaUI, approve)
	return nil
}

// uiAllowed checks with the current settings and rights whether user may answer a
// confirmation in the UI; every failure means no.
func (w *Watcher) uiAllowed(ctx context.Context, user string) bool {
	recipients, err := w.recipients(ctx)
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: approvers unreadable, answer in the UI refused", "error", err)
		return false
	}
	i := slices.IndexFunc(recipients, func(r recipient) bool { return r.UserID == user })
	return i >= 0 && recipients[i].channels.UI
}

// take removes a confirmation, so that exactly one answer settles it.
func (w *Watcher) take(id string) (*pendingAck, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.pending[id]
	if ok {
		delete(w.pending, id)
	}
	return p, ok
}

// HandleEvent takes a mobile_app_notification_action event. It runs on the read loop of
// the Home Assistant client: it never calls the client synchronously.
func (w *Watcher) HandleEvent(e ha.Event) {
	var data struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return
	}
	var nonce string
	approve := false
	switch {
	case strings.HasPrefix(data.Action, actionYes):
		nonce, approve = strings.TrimPrefix(data.Action, actionYes), true
	case strings.HasPrefix(data.Action, actionNo):
		nonce = strings.TrimPrefix(data.Action, actionNo)
	default:
		return // not ours
	}
	if !noncePattern.MatchString(nonce) {
		w.cfg.Logger.Warn("certificate acknowledgement answer discarded: malformed")
		return
	}
	user := e.Context.UserID
	w.mu.Lock()
	id := ""
	for _, p := range w.pending {
		if p.nonce == nonce && w.cfg.Now().Before(p.expires) {
			id = p.id
		}
	}
	w.mu.Unlock()
	p, ok := w.take(id)
	if !ok {
		w.cfg.Logger.Warn("certificate acknowledgement answer discarded: unknown, expired or already answered")
		return
	}
	if user == "" || !p.approvers[user] || user == w.serviceUser() {
		// Someone who may not confirm pressed the button: the request ends unconfirmed.
		w.cfg.Logger.Warn("certificate acknowledgement answered by someone who may not confirm; not acknowledged", "user_id", user)
		approve = false
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), settleTimeout)
		defer cancel()
		w.settle(ctx, p, user, ViaPush, approve)
	}()
}

// settle ends a confirmation: the notifications go, and an approval is stored, the
// approvers are told who confirmed, and the bell is brought in step.
func (w *Watcher) settle(ctx context.Context, p *pendingAck, user string, via Via, approve bool) {
	defer w.cfg.OnChange()
	w.clearDelivered(ctx, p)
	if !approve {
		w.cfg.Logger.Info("certificate acknowledgement not confirmed", "digest", p.digest.String(), "by", user, "via", string(via))
		return
	}
	if err := w.cfg.Store.Acknowledge(ctx, Ack{Digest: p.digest, By: user, Via: via, At: w.cfg.Now()}); err != nil {
		w.cfg.Logger.Error("certificate acknowledgement not stored", "error", err)
		return
	}
	w.cfg.Logger.Warn("certificate acknowledged as the household's", "digest", p.digest.String(), "by", user,
		"via", string(via), "asked_by", p.requestedBy)
	who := w.userName(ctx, user)
	viaKey := i18n.CTViaUI
	if via == ViaPush {
		viaKey = i18n.CTViaPush
	}
	w.tellApprovers(ctx, func(lang i18n.Lang) ha.Notification {
		args := certArgs(w.cfg.Name, p.cert)
		args["user"], args["via"] = who, i18n.T(lang, viaKey, nil)
		return ha.Notification{Title: i18n.T(lang, i18n.CTAcknowledgedTitle, nil), Message: i18n.T(lang, i18n.CTAcknowledgedMessage, args)}
	})
	w.reevaluate(ctx)
}

func (w *Watcher) clearDelivered(ctx context.Context, p *pendingAck) {
	if w.cfg.Notifier == nil {
		return
	}
	for _, service := range p.delivered {
		if err := w.cfg.Notifier.ClearNotification(ctx, service, tagPrefix+p.nonce); err != nil {
			w.cfg.Logger.Warn("certificate acknowledgement notification not removed", "notify_service", service, "error", err)
		}
	}
}

// reevaluate brings the bell in step after an acknowledgement, without querying.
func (w *Watcher) reevaluate(ctx context.Context) {
	w.round.Lock()
	defer w.round.Unlock()
	since, _, err := w.cfg.Store.WatchSince(ctx, w.cfg.Name, w.cfg.Now())
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
		return
	}
	w.evaluate(ctx, since, false)
}

// expire ends the confirmations whose time ran out.
func (w *Watcher) expire(ctx context.Context, now time.Time) {
	w.mu.Lock()
	var ended []*pendingAck
	for id, p := range w.pending {
		if !now.Before(p.expires) {
			ended = append(ended, p)
			delete(w.pending, id)
		}
	}
	w.mu.Unlock()
	for _, p := range ended {
		w.cfg.Logger.Info("certificate acknowledgement not confirmed in time", "digest", p.digest.String())
		w.clearDelivered(ctx, p)
	}
}
