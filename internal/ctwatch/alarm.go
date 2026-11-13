// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/untrusted"
)

const (
	// maxIssuerShown and maxNamesShown bound what of a certificate a notification shows;
	// both come from a public log and may have been chosen by an attacker.
	maxIssuerShown = 120
	maxNamesShown  = 200
	dateFormat     = "2006-01-02"
)

// shownNames are the names of a certificate for a human: cleaned, cut, comma-separated.
func shownNames(names []string) string {
	return untrusted.ForNotification(strings.Join(names, ", "), maxNamesShown)
}

func shownIssuer(issuer string) string {
	if s := untrusted.ForNotification(issuer, maxIssuerShown); s != "" {
		return s
	}
	return "?"
}

// certArgs are the placeholders every message about one certificate fills.
func certArgs(name string, c Stored) i18n.Args {
	return i18n.Args{"name": name, "issuer": shownIssuer(c.Issuer), "names": shownNames(c.Names),
		"not_before": c.NotBefore.UTC().Format(dateFormat), "not_after": c.NotAfter.UTC().Format(dateFormat)}
}

// alarmNotice is the message about certificates the household was not told of yet: on the
// first round of a name one inventory, otherwise the first certificate in detail and how
// many more there are.
func (w *Watcher) alarmNotice(lang i18n.Lang, news []decided, first bool, now time.Time) ha.Notification {
	head := news[0]
	args := certArgs(w.cfg.Name, head.Stored)
	if first {
		args["count"] = strconv.Itoa(len(news))
		return ha.Notification{Title: i18n.T(lang, i18n.CTInventoryTitle, args), Message: i18n.T(lang, i18n.CTInventoryMessage, args)}
	}
	title, message := i18n.CTAlarmTitle, i18n.CTAlarmMessage
	if head.Result == PastExposure {
		title, message = i18n.CTPastTitle, i18n.CTPastMessage
	}
	text := i18n.T(lang, message, args)
	if head.NotBefore.After(now) {
		text += " " + i18n.T(lang, i18n.CTAlarmNotYet, nil)
	}
	if len(news) > 1 {
		text += " " + i18n.T(lang, i18n.CTAlarmMore, i18n.Args{"count": strconv.Itoa(len(news) - 1)})
	}
	return ha.Notification{Title: i18n.T(lang, title, args), Message: text}
}

// tellAlarm sends the notice to every device of every approver. Alarms come first in the
// notice, then past exposures.
func (w *Watcher) tellAlarm(ctx context.Context, news []decided, first bool) {
	ordered := make([]decided, 0, len(news))
	for _, r := range []Result{Alarm, PastExposure} {
		for _, d := range news {
			if d.Result == r {
				ordered = append(ordered, d)
			}
		}
	}
	now := w.cfg.Now()
	w.tellApprovers(ctx, func(lang i18n.Lang) ha.Notification { return w.alarmNotice(lang, ordered, first, now) })
}

// tellApprovers sends a notice to every device of every approver, each in their
// language; failures are logged.
func (w *Watcher) tellApprovers(ctx context.Context, notice func(i18n.Lang) ha.Notification) {
	if w.cfg.Approvers == nil || w.cfg.Notifier == nil {
		return
	}
	approvers, err := w.cfg.Approvers.List(ctx)
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: approvers unreadable, nobody told", "error", err)
		return
	}
	link := w.link()
	reached := 0
	for _, a := range approvers {
		if a.UserID == w.serviceUser() {
			continue
		}
		n := notice(w.language(a))
		n.URL = link
		for _, d := range a.Devices {
			if err := w.cfg.Notifier.Notify(ctx, d.Service, n); err != nil {
				w.cfg.Logger.Warn("certificate monitoring: notification not delivered", "approver", a.UserID, "notify_service", d.Service, "error", err)
				continue
			}
			reached++
		}
	}
	if reached == 0 {
		w.cfg.Logger.Warn("certificate monitoring: no approver's device reached; the bell and the UI show it")
	}
}

func (w *Watcher) language(a approval.Approver) i18n.Lang {
	if l, ok := i18n.Parse(a.Language); ok {
		return l
	}
	return w.cfg.Language()
}

func (w *Watcher) serviceUser() string {
	if w.cfg.ServiceUser == nil {
		return ""
	}
	return w.cfg.ServiceUser()
}

func (w *Watcher) link() string {
	if w.cfg.Link == nil {
		return ""
	}
	link := w.cfg.Link()
	if !ha.ValidTapTarget(link) {
		return ""
	}
	return link
}

// ringAlarm keeps Home Assistant's bell in step with the alarms: shown (and its count
// updated) while there is one, taken back once when there is none.
func (w *Watcher) ringAlarm(ctx context.Context, alarms int) {
	if w.cfg.Bell == nil {
		return
	}
	on := alarms > 0
	w.mu.Lock()
	known := w.bellAlarmOn
	w.mu.Unlock()
	if !on && known != nil && !*known {
		return
	}
	var err error
	if on {
		lang := w.cfg.Language()
		args := i18n.Args{"name": w.cfg.Name, "count": strconv.Itoa(alarms)}
		err = w.cfg.Bell.Ring(ctx, bellAlarm, ha.Notification{Title: i18n.T(lang, i18n.CTBellTitle, nil), Message: i18n.T(lang, i18n.CTBellMessage, args)})
	} else {
		err = w.cfg.Bell.Clear(ctx, bellAlarm)
	}
	if err != nil {
		w.cfg.Logger.Warn("certificate monitoring: Home Assistant's bell not updated", "error", err)
		return
	}
	w.mu.Lock()
	w.bellAlarmOn = &on
	w.mu.Unlock()
}
