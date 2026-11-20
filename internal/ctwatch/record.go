// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"database/sql"
	"time"
	"unicode/utf8"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

const (
	// maxRecordedNames is how many names of a certificate cert.unexpected records.
	maxRecordedNames = 16
	// maxIssuerRunes is how long an issuer is recorded; a longer one is cut and marked.
	maxIssuerRunes = 1024
	// noIssuer stands for an empty issuer: the format needs one, and the alarm must not be
	// kept out of the log by a certificate made without.
	noIssuer = "(no issuer)"
)

// recordUnexpected records every certificate that raises an alarm, is not valid yet or was
// a past exposure with cert.unexpected, once for each certificate and status (audit/v1,
// SPEC-v0 sections 9.2 and 11.7 item 7). A failure is logged; the next round tries again.
func (w *Watcher) recordUnexpected(ctx context.Context, all []decided, now time.Time) {
	if w.cfg.Audit == nil {
		return
	}
	for _, d := range all {
		status := ""
		switch {
		case d.Result == PastExposure:
			status = audit.CertPastExposure
		case d.Result == Alarm && d.NotBefore.After(now):
			status = audit.CertNotYetValid
		case d.Result == Alarm:
			status = audit.CertAlarm
		default:
			continue
		}
		c := w.recordedCert(d, status)
		err := w.cfg.Store.RecordOnce(ctx, d.Digest, status, func(tx *sql.Tx) error {
			_, err := w.cfg.Audit.AppendTx(ctx, tx, audit.Entry{Event: audit.EventCertUnexpected, Certificate: &c})
			return err
		})
		if err != nil {
			w.cfg.Logger.Error("certificate monitoring: audit log write failed", "error", err)
		}
	}
}

// recordedCert is what cert.unexpected records of d: its names that cover the watched name,
// the first 16 (its own first 16 if none does), and its issuer, cut to 1023 characters and
// an ellipsis, or a stand-in if it has none. A certificate whose
// first logging no source told is recorded with the time it was first seen in a log, the
// latest it can have been logged.
func (w *Watcher) recordedCert(d decided, status string) audit.Certificate {
	var names []string
	for _, n := range d.Names {
		if Covers(n, w.cfg.Name) && len(names) < maxRecordedNames {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		// It counted for another public name: its own names, rather than none.
		names = d.Names[:min(len(d.Names), maxRecordedNames)]
	}
	issuer := d.Issuer
	if issuer == "" {
		issuer = noIssuer
	}
	if utf8.RuneCountInString(issuer) > maxIssuerRunes {
		issuer = string([]rune(issuer)[:maxIssuerRunes-1]) + "…"
	}
	logged := d.Logged
	if logged.IsZero() {
		logged = d.FirstSeen
	}
	return audit.Certificate{Digest: d.Digest.String(), Status: status, Names: names, Issuer: issuer,
		PublicKey: d.PublicKey.String(), NotBefore: d.NotBefore, NotAfter: d.NotAfter, Logged: logged}
}

// recordAck is the audit entry of an acknowledgement by user, written with it.
func (w *Watcher) recordAck(ctx context.Context, digest Digest, user string) func(*sql.Tx) error {
	if w.cfg.Audit == nil {
		return nil
	}
	return func(tx *sql.Tx) error {
		_, err := w.cfg.Audit.AppendTx(ctx, tx, audit.Entry{Event: audit.EventCertAcknowledged,
			Actor: &audit.Actor{Kind: audit.ActorUser, ID: user}, Certificate: &audit.Certificate{Digest: digest.String()}})
		return err
	}
}

// recordStatus records the state of monitoring with ct.status at every change: ok while at
// least two sources answered within 24 hours, limited with one, unavailable with none
// (SPEC-v0 section 11.7 item 7). Until a source answered or 24 hours passed since since,
// there is no state yet.
func (w *Watcher) recordStatus(ctx context.Context, since, now time.Time) {
	if w.cfg.Audit == nil {
		return
	}
	answered := 0
	for _, src := range w.cfg.Sources {
		st, err := w.cfg.Store.Source(ctx, src.Name(), w.cfg.Name)
		if err != nil {
			w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
			return
		}
		if !st.LastSuccess.IsZero() && now.Sub(st.LastSuccess) < Unavailable {
			answered++
		}
	}
	status := audit.CTUnavailable
	switch {
	case answered >= 2:
		status = audit.CTOK
	case answered == 1:
		status = audit.CTLimited
	case now.Sub(since) < Unavailable:
		return
	}
	if err := w.cfg.Audit.RecordCTStatus(ctx, status); err != nil {
		w.cfg.Logger.Error("certificate monitoring: audit log write failed", "error", err)
	}
}
