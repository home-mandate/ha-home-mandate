// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"slices"
	"time"
)

// Status is the monitoring as the UI shows it.
type Status struct {
	Name  string
	Since time.Time
	// KeyKnown: Home-Mandate knows its own server key; without it every certificate for
	// the name needs an acknowledgement. KeyError is why the last read of it failed.
	KeyKnown bool
	KeyError string
	Sources  []SourceStatus
	// LastSuccess is the latest successful query of any source, zero before the first;
	// Unavailable says that none succeeded for 24 hours.
	LastSuccess time.Time
	Unavailable bool
	// Certs are the certificates that raise an alarm, are a past exposure or were
	// acknowledged, newest first (as the store gives them).
	Certs   []CertStatus
	Pending []Pending
}

// SourceStatus is one source's state.
type SourceStatus struct {
	Name        string
	LastSuccess time.Time
	LastFailure time.Time
	Failure     string
}

// CertStatus is a certificate the household should know of.
type CertStatus struct {
	Stored
	Result Result
	Ack    *Ack
}

// Status reads the state of the monitoring.
func (w *Watcher) Status(ctx context.Context) (Status, error) {
	now := w.cfg.Now()
	since, _, err := w.cfg.Store.WatchSince(ctx, w.cfg.Name, now)
	if err != nil {
		return Status{}, err
	}
	out := Status{Name: w.cfg.Name, Since: since}
	keys, err := w.cfg.Store.Keys(ctx)
	if err != nil {
		return Status{}, err
	}
	out.KeyKnown = keys.HasCurrent
	w.mu.Lock()
	if w.keyErr != nil {
		out.KeyError = w.keyErr.Error()
	}
	w.mu.Unlock()
	last := since
	for _, src := range w.cfg.Sources {
		st, err := w.cfg.Store.Source(ctx, src.Name(), w.cfg.Name)
		if err != nil {
			return Status{}, err
		}
		out.Sources = append(out.Sources, SourceStatus{Name: src.Name(), LastSuccess: st.LastSuccess, LastFailure: st.LastFailure, Failure: st.Failure})
		if st.LastSuccess.After(out.LastSuccess) {
			out.LastSuccess = st.LastSuccess
		}
		if st.LastSuccess.After(last) {
			last = st.LastSuccess
		}
	}
	out.Unavailable = now.Sub(last) >= Unavailable
	if out.Certs, err = w.certStatus(ctx, since, now); err != nil {
		return Status{}, err
	}
	out.Pending = w.pendingList(now)
	return out, nil
}

func (w *Watcher) certStatus(ctx context.Context, since, now time.Time) ([]CertStatus, error) {
	all, err := w.decideAll(ctx, since, now)
	if err != nil {
		return nil, err
	}
	acks, err := w.cfg.Store.Acks(ctx)
	if err != nil {
		return nil, err
	}
	byDigest := map[Digest]Ack{}
	for _, a := range acks {
		byDigest[a.Digest] = a
	}
	var out []CertStatus
	for _, d := range all {
		c := CertStatus{Stored: d.Stored, Result: d.Result}
		if a, ok := byDigest[d.Digest]; ok {
			c.Ack = &a
		}
		if c.Result != None || c.Ack != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func (w *Watcher) pendingList(now time.Time) []Pending {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Pending
	for _, p := range w.pending {
		if now.Before(p.expires) {
			out = append(out, p.public())
		}
	}
	slices.SortFunc(out, func(a, b Pending) int { return a.Expires.Compare(b.Expires) })
	return out
}
