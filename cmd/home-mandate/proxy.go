// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"
)

// refusalLogEvery bounds the log of refused senders, so that nobody can fill it.
const refusalLogEvery = time.Minute

// onlyProxy serves the MCP listener behind a reverse proxy that ends TLS (HM_PROXY,
// decision 1, addition of 2026-10-08). Only the proxy's own address is served; anything
// else gets an empty 403, so nobody bypasses the proxy's TLS. The sender is the last
// entry of X-Forwarded-For, the one the proxy appended; earlier entries come from the
// client. Without a usable entry the proxy itself is the sender. The sender serves the
// per-sender limits and the log only, never a decision about access.
func onlyProxy(proxy netip.Addr, next http.Handler, now func() time.Time, logger *slog.Logger) http.Handler {
	var lastLog atomic.Int64 // unix nanoseconds of the last refusal logged
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || peer.Addr().Unmap() != proxy {
			t, last := now().UnixNano(), lastLog.Load()
			if t-last >= int64(refusalLogEvery) && lastLog.CompareAndSwap(last, t) {
				logger.Warn("request from outside the proxy refused", "remote", r.RemoteAddr)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		sender := proxy
		if ip, ok := forwardedFor(r.Header); ok {
			sender = ip
		}
		// A shallow copy is enough: only RemoteAddr changes, a plain string.
		out := r.WithContext(r.Context())
		out.RemoteAddr = netip.AddrPortFrom(sender, 0).String()
		next.ServeHTTP(w, out)
	})
}

// forwardedFor is the last entry of the last X-Forwarded-For line, if it is a plain IP
// address.
func forwardedFor(h http.Header) (netip.Addr, bool) {
	lines := h.Values("X-Forwarded-For")
	if len(lines) == 0 {
		return netip.Addr{}, false
	}
	entries := strings.Split(lines[len(lines)-1], ",")
	ip, err := netip.ParseAddr(strings.TrimSpace(entries[len(entries)-1]))
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}
