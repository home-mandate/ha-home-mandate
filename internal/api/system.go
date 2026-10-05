// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/ha"
)

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

// formatTime writes UTC with milliseconds, like the audit log; zero is null.
func formatTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(timeFormat)
	return &s
}

type wireHA struct {
	Connected bool     `json:"connected"`
	Since     *string  `json:"since"`
	Version   *string  `json:"version"`
	UserName  *string  `json:"user_name"`
	Commands  []string `json:"commands"`
}

type wireTLS struct {
	Present    bool    `json:"present"`
	ValidUntil *string `json:"valid_until"`
	// RenewalFailed: renewed files were found but not taken over (the previous pair stays).
	RenewalFailed bool `json:"renewal_failed"`
}

type wireStop struct {
	Active bool    `json:"active"`
	Since  *string `json:"since"`
	ByName *string `json:"by_name"`
}

type wireChain struct {
	Valid       bool    `json:"valid"`
	BrokenAtSeq *int64  `json:"broken_at_seq"`
	CheckedAt   *string `json:"checked_at"`
}

type wireSystem struct {
	Mode                string    `json:"mode"`
	Version             string    `json:"version"`
	Commit              string    `json:"commit"`
	ServerTime          string    `json:"server_time"`
	RetentionDays       int       `json:"retention_days"`
	HA                  wireHA    `json:"ha"`
	MCPURL              *string   `json:"mcp_url"`
	TLS                 wireTLS   `json:"tls"`
	EmergencyStop       wireStop  `json:"emergency_stop"`
	Chain               wireChain `json:"chain"`
	ApproversConfigured int       `json:"approvers_configured"`
	// ClockBehind: the clock lies behind the newest audit entry; nothing is decided.
	ClockBehind bool `json:"clock_behind"`
	// Directory: storing renames of Home Assistant has failed for a while; with overflow,
	// renames cannot be held any more and nothing is decided.
	Directory wireDirectory `json:"directory"`
}

type wireDirectory struct {
	StoreFailingSince *string `json:"store_failing_since"`
	Overflow          bool    `json:"overflow"`
	// RenamesLastHour: renames from Home Assistant in the last hour; above
	// RenameFloodThreshold the UI says that this hints at a broken integration.
	RenamesLastHour      int `json:"renames_last_hour"`
	RenameFloodThreshold int `json:"rename_flood_threshold"`
}

// directoryGrace is how long storing renames may fail before the UI says so: a short
// lock of the database is no news.
const directoryGrace = 2 * time.Minute

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Server) getSystem(r *request) (any, error) {
	return s.system(r.Context())
}

func (s *Server) system(ctx context.Context) (wireSystem, error) {
	st := s.cfg.Status()
	stop, err := s.stopState(ctx)
	if err != nil {
		return wireSystem{}, err
	}
	approvers, err := s.cfg.Approvers.List(ctx)
	if err != nil {
		return wireSystem{}, err
	}
	behind, err := s.cfg.Log.ClockBehind(ctx)
	if err != nil {
		return wireSystem{}, err
	}
	tls := s.cfg.TLS()
	out := wireSystem{
		Mode: s.cfg.Mode, Version: s.cfg.Version, Commit: s.cfg.Commit, ServerTime: *formatTime(s.now()),
		RetentionDays: int(s.cfg.Retention / (24 * time.Hour)),
		HA: wireHA{Connected: st.HAConnected, Since: formatTime(st.HASince), Version: optional(st.HAVersion),
			UserName: s.users.name(ctx, st.ServiceUser), Commands: ha.AllowedCommands()},
		MCPURL:              optional(s.cfg.MCPURL),
		TLS:                 wireTLS{Present: tls.Present, ValidUntil: formatTime(tls.ValidUntil), RenewalFailed: tls.RenewalFailed},
		EmergencyStop:       stop,
		Chain:               s.chain.get(),
		ApproversConfigured: len(approvers),
		ClockBehind:         behind,
		Directory: wireDirectory{Overflow: s.cfg.Renames.Overflowing(), RenamesLastHour: s.cfg.Renames.RenamesLastHour(),
			RenameFloodThreshold: catalog.RenameFloodThreshold},
	}
	if since := s.cfg.Renames.FailingSince(); !since.IsZero() && s.now().Sub(since) >= directoryGrace {
		out.Directory.StoreFailingSince = formatTime(since)
	}
	if !tls.Present {
		out.TLS.ValidUntil = nil
	}
	return out, nil
}

func (s *Server) stopState(ctx context.Context) (wireStop, error) {
	st, err := s.cfg.Agents.EmergencyStopState(ctx)
	if err != nil {
		return wireStop{}, err
	}
	if !st.Active {
		return wireStop{}, nil
	}
	return wireStop{Active: true, Since: formatTime(st.Since), ByName: s.users.name(ctx, st.By)}, nil
}

// chainStatus is the result of the last verification of the audit log (every 10 minutes
// and on request).
type chainStatus struct {
	mu      sync.Mutex
	checked bool
	valid   bool
	broken  int64
	at      time.Time
}

func (c *chainStatus) set(valid bool, broken int64, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked, c.valid, c.broken, c.at = true, valid, broken, at
}

// get reports the chain as valid until the first check: the gateway refuses to start
// with a broken log, so before the first periodic check it was valid at start.
func (c *chainStatus) get() wireChain {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checked {
		return wireChain{Valid: true}
	}
	out := wireChain{Valid: c.valid, CheckedAt: formatTime(c.at)}
	if !c.valid {
		broken := c.broken
		out.BrokenAtSeq = &broken
	}
	return out
}

// verify checks the whole audit log, keeps the result and tells every open UI.
func (s *Server) verify(ctx context.Context) (wireChain, int, error) {
	r, n, err := s.cfg.Log.Check(ctx)
	if err != nil {
		return wireChain{}, 0, err
	}
	s.chain.set(r.Valid, int64(r.BrokenAt), s.now())
	if !r.Valid {
		s.cfg.Logger.Error("audit log chain is broken", "broken_at", r.BrokenAt)
	}
	s.publishSystem(ctx)
	return s.chain.get(), n, nil
}

// verifyLimit: one verification on request every 10 seconds for everyone; it reads the
// whole log.
const (
	verifyLimit  = 1
	verifyPeriod = 10 * time.Second
)

type wireVerification struct {
	wireChain
	Checked int `json:"checked"`
}

// verifyTimeout bounds a verification on request; it reads the whole log, longer than
// other requests may take.
const verifyTimeout = 2 * time.Minute

func (s *Server) verifyAudit(r *request) (any, error) {
	if ok, wait := s.limits.allow("verify", verifyLimit, verifyPeriod); !ok {
		return nil, failRetry(codeRateLimited, wait)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), verifyTimeout)
	defer cancel()
	chain, n, err := s.verify(ctx)
	if err != nil {
		return nil, err
	}
	return wireVerification{wireChain: chain, Checked: n}, nil
}

// verifyEvery is how often the gateway verifies the audit log on its own (a variable
// for tests).
var verifyEvery = 10 * time.Minute

// RunVerifier verifies the audit log every 10 minutes until ctx ends.
func (s *Server) RunVerifier(ctx context.Context) {
	for {
		if _, _, err := s.verify(ctx); err != nil && ctx.Err() == nil {
			s.cfg.Logger.Error("verifying the audit log failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(verifyEvery):
		}
	}
}
