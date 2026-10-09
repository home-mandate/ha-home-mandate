// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/i18n"
)

// Request limits of the unauthenticated endpoints, per sender and for everyone together,
// in windows of rateWindow. A pairing takes a human seven requests, so one sender can
// admit eight agents a minute; an agent polls the token endpoint every deviceInterval
// for each of at most maxGrantsPerHost pairings and refreshes every few minutes. The
// token endpoint counts separately, so that a flood of sign-ins does not stop agents
// from refreshing.
const (
	rateWindow          = time.Minute
	onboardingPerSender = 60
	onboardingGlobal    = 600
	tokenPerSender      = 120
	tokenGlobal         = 1200

	// warnEvery bounds warnings that anyone can cause, so that nobody can fill the log.
	warnEvery = time.Minute
)

// senderKey identifies the sender of a request for per-sender limits: an IPv4 address,
// or the /64 network of an IPv6 address, which one subscriber usually holds as a whole.
// RemoteAddr may be the forwarded sender behind the proxy (HM_PROXY).
func senderKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	ip = ip.Unmap()
	if ip.Is4() {
		return ip.String()
	}
	p, _ := ip.WithZone("").Prefix(64)
	return p.String()
}

// rateLimit counts requests per sender and in total in fixed windows. It remembers at
// most global senders per window: refused requests are not counted.
type rateLimit struct {
	perSender, global int

	mu       sync.Mutex
	window   time.Time // start of the current window
	total    int
	bySender map[string]int
}

func newRateLimit(perSender, global int) *rateLimit {
	return &rateLimit{perSender: perSender, global: global, bySender: map[string]int{}}
}

// allow counts a request of sender; if refused, it returns the time until the window ends.
func (l *rateLimit) allow(sender string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= rateWindow {
		l.window, l.total = now, 0
		clear(l.bySender)
	}
	if l.total >= l.global || l.bySender[sender] >= l.perSender {
		return false, l.window.Add(rateWindow).Sub(now)
	}
	l.total++
	l.bySender[sender]++
	return true, 0
}

// limited serves next within l; a refused request gets 429 with Retry-After, as a page
// or, with asJSON, as an OAuth error.
func (s *Server) limited(l *rateLimit, asJSON bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, retry := l.allow(s.clientAddr(r), s.cfg.Now())
		if ok {
			next(w, r)
			return
		}
		s.warn("requests over the rate limit refused", "path", r.URL.Path)
		w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
		if asJSON {
			oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable")
			return
		}
		s.fail(w, r, http.StatusTooManyRequests, i18n.PageBusy)
	}
}

// warnings throttles warnings per message.
type warnings struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// warn logs a warning that anyone can cause at most once per warnEvery and message.
func (s *Server) warn(msg string, args ...any) {
	now := s.cfg.Now()
	s.warned.mu.Lock()
	last, seen := s.warned.last[msg]
	due := !seen || now.Sub(last) >= warnEvery
	if due {
		s.warned.last[msg] = now
	}
	s.warned.mu.Unlock()
	if due {
		s.cfg.Logger.Warn(msg, args...)
	}
}
