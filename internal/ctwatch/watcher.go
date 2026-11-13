// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

const (
	// Interval is the time between two rounds; RELAY-v0 section 6.7 asks for at least
	// one an hour, SSLMate agreed to four.
	Interval = 15 * time.Minute
	// maxJitter spreads the rounds of many households.
	maxJitter = 2 * time.Minute
	// startDelay gives Home Assistant's connection time before the first round.
	startDelay = 30 * time.Second
	// Unavailable is how long no query may succeed before the household is told that
	// monitoring is not possible (RELAY-v0 section 6.7).
	Unavailable = 24 * time.Hour
	// roundTimeout bounds one round; a slow source must not hold up the next.
	roundTimeout = 5 * time.Minute

	bellAlarm       = "hm_ct_alarm"
	bellUnavailable = "hm_ct_unavailable"
)

// Notifier sends a push notification to a device of the Companion App (ha.Client).
type Notifier interface {
	Notify(ctx context.Context, service string, n ha.Notification) error
	ClearNotification(ctx context.Context, service, tag string) error
}

// Bell shows a persistent notification in Home Assistant (ha.Bell).
type Bell interface {
	Ring(ctx context.Context, id string, n ha.Notification) error
	Clear(ctx context.Context, id string) error
}

// Approvers lists the configured approvers (approval.Approvers).
type Approvers interface {
	List(ctx context.Context) ([]approval.Approver, error)
}

// Config configures a Watcher.
type Config struct {
	Name    string // the public name watched
	Store   *Store
	Sources []Source
	// Key is where the gateway's own server key is read; nil when Home-Mandate cannot
	// know it (TLS ends at something it does not see), and then every certificate for
	// the name needs an acknowledgement.
	Key       KeySource
	Approvers Approvers
	// IsAdmin tells whether a Home Assistant user is an administrator now; only
	// administrators may answer in the UI.
	IsAdmin func(ctx context.Context, user string) (bool, error)
	// ServiceUser is Home-Mandate's own Home Assistant user, never asked.
	ServiceUser func() string
	// UserName is a user's display name for notifications; nil or "" means the ID.
	UserName func(ctx context.Context, user string) string
	Notifier Notifier
	Bell     Bell
	// Language is the household's language, for the bell and approvers without one.
	Language func() i18n.Lang
	// ConfirmTimeout is how long a confirmation of an acknowledgement waits.
	ConfirmTimeout func() time.Duration
	// Link is what a tap on a notification opens; "" or nil means the Companion App.
	Link     func() string
	OnChange func()
	Now      func() time.Time
	Logger   *slog.Logger
}

// Watcher runs the monitoring of one name. It is safe for concurrent use.
type Watcher struct {
	cfg Config

	round sync.Mutex // one round, or one re-evaluation after an acknowledgement, at a time

	mu              sync.Mutex
	backoff         map[string]time.Time // source → no query before
	keyErr          error
	keyErrLogged    bool
	// bellAlarms is the count the alarm bell shows, -1 while not known (after a start), so
	// that the first round sets the bell either way.
	bellAlarms int
	// unavailableRung is whether the bell says that monitoring is not possible; nil while
	// not known (after a start), so that a notification left from before is taken back.
	unavailableRung *bool
	pending         map[string]*pendingAck // by ID
	stopped         bool
	// asking serialises requests for acknowledgements, so that two at once for one
	// certificate send one confirmation.
	asking sync.Mutex
}

// New returns a watcher.
func New(cfg Config) *Watcher {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Language == nil {
		cfg.Language = func() i18n.Lang { return i18n.Default }
	}
	if cfg.OnChange == nil {
		cfg.OnChange = func() {}
	}
	return &Watcher{cfg: cfg, backoff: map[string]time.Time{}, pending: map[string]*pendingAck{}, bellAlarms: -1}
}

// Run does a round shortly after the start and then every Interval with a jitter, until
// ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	wait := startDelay
	for {
		select {
		case <-ctx.Done():
			w.stop()
			return
		case <-time.After(wait):
		}
		w.Round(ctx)
		wait = Interval + rand.N(maxJitter)
	}
}

func (w *Watcher) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
}

// Round queries every source, decides, tells the household of what is new and of a
// monitoring that is not possible, and ends confirmations that waited too long.
//
// OnChange runs while the round lock is held: it must not start a round or call back into
// anything that takes it.
func (w *Watcher) Round(ctx context.Context) {
	w.round.Lock()
	defer w.round.Unlock()
	ctx, cancel := context.WithTimeout(ctx, roundTimeout)
	defer cancel()
	defer w.cfg.OnChange()
	now := w.cfg.Now()
	since, first, err := w.cfg.Store.WatchSince(ctx, w.cfg.Name, now)
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
		return
	}
	if first {
		w.cfg.Logger.Info("certificate monitoring started", "name", w.cfg.Name)
	}
	w.observeKey(ctx, false, now)
	for _, src := range w.cfg.Sources {
		w.query(ctx, src, now)
	}
	w.evaluate(ctx, since)
	w.availability(ctx, since, now)
	w.expire(ctx, now)
}

// observeKey records the current server key; a failure keeps the one known.
func (w *Watcher) observeKey(ctx context.Context, recheck bool, now time.Time) {
	if w.cfg.Key == nil {
		return
	}
	key, err := w.cfg.Key.Key(ctx, recheck)
	w.mu.Lock()
	w.keyErr = err
	logIt := err != nil && !w.keyErrLogged
	w.keyErrLogged = err != nil
	w.mu.Unlock()
	if err != nil {
		if logIt {
			w.cfg.Logger.Warn("certificate monitoring: own certificate not readable, the last known key stays", "error", err)
		}
		return
	}
	changed, err := w.cfg.Store.ObserveKey(ctx, key, now)
	switch {
	case err != nil:
		w.cfg.Logger.Error("certificate monitoring: server key not stored", "error", err)
	case changed:
		w.cfg.Logger.Info("certificate monitoring: server key recorded", "key", key.String())
	}
}

// query asks one source and keeps what counts for the name.
func (w *Watcher) query(ctx context.Context, src Source, now time.Time) {
	w.mu.Lock()
	until := w.backoff[src.Name()]
	w.mu.Unlock()
	if now.Before(until) {
		return
	}
	st, err := w.cfg.Store.Source(ctx, src.Name(), w.cfg.Name)
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
		return
	}
	batch, err := src.Fetch(ctx, w.cfg.Name, st.Cursor)
	if err != nil {
		var limited *RateLimitedError
		if errors.As(err, &limited) {
			w.mu.Lock()
			w.backoff[src.Name()] = now.Add(max(limited.RetryAfter, Interval))
			w.mu.Unlock()
		}
		w.cfg.Logger.Warn("certificate monitoring: query failed", "source", src.Name(), "error", err)
		if err := w.cfg.Store.Failed(ctx, src.Name(), w.cfg.Name, err, now); err != nil {
			w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
		}
		return
	}
	if batch.Unread > 0 {
		w.cfg.Logger.Warn("certificate monitoring: certificates that could not be read are kept from the listing and count as foreign",
			"source", src.Name(), "unread", batch.Unread)
	}
	watched := Situation{PublicNames: []string{w.cfg.Name}}
	for _, p := range batch.Certs {
		if !watched.counts(p.Cert) {
			continue
		}
		if _, err := w.cfg.Store.PutCert(ctx, p, now); err != nil {
			w.cfg.Logger.Error("certificate monitoring: certificate not stored, the query is repeated", "error", err)
			return // the cursor stays, so nothing is lost
		}
	}
	if err := w.cfg.Store.Succeeded(ctx, src.Name(), w.cfg.Name, batch.Cursor, now); err != nil {
		w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
	}
}

// decided is a stored certificate with its result now.
type decided struct {
	Stored
	Result Result
}

// decideAll applies the decision to every stored certificate.
func (w *Watcher) decideAll(ctx context.Context, since, now time.Time) ([]decided, error) {
	certs, err := w.cfg.Store.Certs(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := w.cfg.Store.Keys(ctx)
	if err != nil {
		return nil, err
	}
	acks, err := w.cfg.Store.Acks(ctx)
	if err != nil {
		return nil, err
	}
	s := Situation{PublicNames: []string{w.cfg.Name}, Since: since, Now: now, Keys: keys, Acknowledged: map[Digest]bool{}}
	for _, a := range acks {
		s.Acknowledged[a.Digest] = true
	}
	if len(certs) >= maxStoredCerts {
		w.cfg.Logger.Error("certificate monitoring: more certificates for the name than are read; only the newest are decided",
			"limit", maxStoredCerts)
	}
	out := make([]decided, len(certs))
	for i, c := range certs {
		out[i] = decided{Stored: c, Result: Decide(s, c.Cert)}
	}
	return out, nil
}

// unnotified are the certificates that raise an alarm or are a past exposure and that
// the household was not told of.
func unnotified(all []decided) []decided {
	var out []decided
	for _, d := range all {
		if d.Result != None && d.NotifiedAt.IsZero() {
			out = append(out, d)
		}
	}
	return out
}

// anyNotified tells whether the household was ever told of a certificate.
func anyNotified(all []decided) bool {
	for _, d := range all {
		if !d.NotifiedAt.IsZero() {
			return true
		}
	}
	return false
}

// evaluate tells the household of what is new and keeps the bell in step. Before
// telling, the own key is read again: a renewal not yet taken over raises no alarm. The
// first notice ever about the name is one inventory. What reached nobody is told again
// next round.
func (w *Watcher) evaluate(ctx context.Context, since time.Time) {
	now := w.cfg.Now()
	all, err := w.decideAll(ctx, since, now)
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
		return
	}
	news := unnotified(all)
	if len(news) > 0 && w.cfg.Key != nil {
		w.observeKey(ctx, true, now)
		if all, err = w.decideAll(ctx, since, now); err != nil {
			w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
			return
		}
		news = unnotified(all)
	}
	if len(news) > 0 {
		for _, d := range news {
			w.cfg.Logger.Warn("certificate for the public name not obtained by Home-Mandate", "result", d.Result.String(),
				"digest", d.Digest.String(), "issuer", d.Issuer, "serial", d.Serial, "names", strings.Join(d.Names, ","),
				"not_before", d.NotBefore.Format(time.RFC3339), "not_after", d.NotAfter.Format(time.RFC3339))
		}
		if w.tellAlarm(ctx, news, !anyNotified(all)) {
			digests := make([]Digest, len(news))
			for i, d := range news {
				digests[i] = d.Digest
			}
			if err := w.cfg.Store.MarkNotified(ctx, digests, now); err != nil {
				w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
			}
		}
	}
	alarms := 0
	for _, d := range all {
		if d.Result == Alarm {
			alarms++
		}
	}
	w.ringAlarm(ctx, alarms)
}

// availability tells the household once when no query succeeded for Unavailable, and
// takes the notification back when one does again.
func (w *Watcher) availability(ctx context.Context, since, now time.Time) {
	last, err := w.lastSuccess(ctx, since)
	if err != nil {
		w.cfg.Logger.Error("certificate monitoring: store unavailable", "error", err)
		return
	}
	down := now.Sub(last) >= Unavailable
	w.mu.Lock()
	known := w.unavailableRung
	w.mu.Unlock()
	rung := known != nil && *known
	switch {
	case down && !rung:
		w.cfg.Logger.Error("certificate monitoring not possible: no query succeeded for 24 hours", "since", last.Format(time.RFC3339))
		lang := w.cfg.Language()
		n := ha.Notification{Title: i18n.T(lang, i18n.CTUnavailableTitle, nil), Message: i18n.T(lang, i18n.CTUnavailableMessage,
			i18n.Args{"name": w.cfg.Name, "since": last.UTC().Format("2006-01-02 15:04 UTC")})}
		if w.cfg.Bell != nil && w.cfg.Bell.Ring(ctx, bellUnavailable, n) == nil {
			w.setUnavailableRung(true)
		}
	case !down && (rung || known == nil):
		if w.cfg.Bell != nil && w.cfg.Bell.Clear(ctx, bellUnavailable) == nil {
			w.setUnavailableRung(false)
		}
	}
}

func (w *Watcher) setUnavailableRung(v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.unavailableRung = &v
}

// lastSuccess is the latest successful query of any source, or since if later.
func (w *Watcher) lastSuccess(ctx context.Context, since time.Time) (time.Time, error) {
	last := since
	for _, src := range w.cfg.Sources {
		st, err := w.cfg.Store.Source(ctx, src.Name(), w.cfg.Name)
		if err != nil {
			return time.Time{}, err
		}
		if st.LastSuccess.After(last) {
			last = st.LastSuccess
		}
	}
	return last, nil
}

// Applicable returns the name to watch for a public URL, or why there is none: no
// public URL, not https, an IP address, or a name that is not public.
func Applicable(publicURL string) (name, reason string) {
	if publicURL == "" {
		return "", "no_public_url"
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" {
		return "", "no_tls"
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(host) != nil {
		return "", "ip_address"
	}
	if !publicName(host) {
		return "", "not_public_name"
	}
	return host, ""
}

// nonPublicSuffixes are names no public CA issues for (RFC 6761, RFC 6762, RFC 8375 and
// names in common use inside homes).
var nonPublicSuffixes = []string{".local", ".localhost", ".localdomain", ".lan", ".home", ".home.arpa", ".internal",
	".corp", ".test", ".example", ".invalid", ".intranet", ".private"}

func publicName(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, s := range nonPublicSuffixes {
		if strings.HasSuffix(host, s) {
			return false
		}
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}
