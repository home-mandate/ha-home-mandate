// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"
)

// Settings keys of the defaults for new templates and mandates, and of the bell (F2 B2).
const (
	settingTimeout   = "default_approval_timeout"
	settingRateLimit = "default_max_actions_per_hour"
	settingBell      = "approval_bell"
)

// Defaults of the defaults, as in the UI's fixtures.
const (
	defaultTimeout   = "PT2M"
	defaultRateLimit = 60
	minTimeout       = 10 * time.Second
	maxTimeout       = time.Hour
	maxRateLimit     = 1000
)

// durationPattern is SPEC-v0's form of an approval timeout: PT, minutes and/or seconds.
var durationPattern = regexp.MustCompile(`^PT(?:([0-9]{1,2})M)?(?:([0-9]{1,4})S)?$`)

// parseTimeout reads "PT…M…S"; false if it is not that form.
func parseTimeout(s string) (time.Duration, bool) {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil || m[1] == "" && m[2] == "" {
		return 0, false
	}
	minutes, _ := strconv.Atoi(m[1])
	seconds, _ := strconv.Atoi(m[2])
	return time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, true
}

type wireDefaults struct {
	ApprovalTimeout   string `json:"approval_timeout"`
	MaxActionsPerHour int    `json:"max_actions_per_hour"`
	Bell              bool   `json:"bell"`
}

// bellSetting keeps the bell switch in memory for internal/approval, which asks for it
// on every request.
type bellSetting struct{ on atomic.Bool }

// BellEnabled tells internal/approval whether to show the neutral hint in Home
// Assistant's notification bell (off by default).
func (s *Server) BellEnabled() bool { return s.bell.on.Load() }

// LoadSettings reads the settings that live in memory; called at start.
func (s *Server) LoadSettings(ctx context.Context) error {
	d, err := s.defaults(ctx)
	if err != nil {
		return err
	}
	s.bell.on.Store(d.Bell)
	return nil
}

func (s *Server) defaults(ctx context.Context) (wireDefaults, error) {
	out := wireDefaults{ApprovalTimeout: defaultTimeout, MaxActionsPerHour: defaultRateLimit}
	if v, ok, err := s.cfg.Store.Setting(ctx, settingTimeout); err != nil {
		return wireDefaults{}, err
	} else if _, valid := parseTimeout(v); ok && valid {
		out.ApprovalTimeout = v
	}
	if v, ok, err := s.cfg.Store.Setting(ctx, settingRateLimit); err != nil {
		return wireDefaults{}, err
	} else if n, err := strconv.Atoi(v); ok && err == nil && n >= 1 && n <= maxRateLimit {
		out.MaxActionsPerHour = n
	}
	v, _, err := s.cfg.Store.Setting(ctx, settingBell)
	if err != nil {
		return wireDefaults{}, err
	}
	out.Bell = v == "on"
	return out, nil
}

func (s *Server) getSettings(r *request) (any, error) {
	return s.defaults(r.Context())
}

// putSettings stores the defaults. The timeout may be up to an hour as in a mandate; the
// wait itself is still capped by HM_APPROVAL_TIMEOUT (internal/approval).
func (s *Server) putSettings(r *request) (any, error) {
	var in struct {
		ApprovalTimeout   *string `json:"approval_timeout"`
		MaxActionsPerHour *int    `json:"max_actions_per_hour"`
		Bell              *bool   `json:"bell"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.ApprovalTimeout == nil {
		return nil, failField(codeInvalidInput, "/approval_timeout")
	}
	if d, ok := parseTimeout(*in.ApprovalTimeout); !ok || d < minTimeout || d > maxTimeout {
		return nil, failField(codeInvalidInput, "/approval_timeout")
	}
	if in.MaxActionsPerHour == nil || *in.MaxActionsPerHour < 1 || *in.MaxActionsPerHour > maxRateLimit {
		return nil, failField(codeInvalidInput, "/max_actions_per_hour")
	}
	if in.Bell == nil {
		return nil, failField(codeInvalidInput, "/bell")
	}
	bell := "off"
	if *in.Bell {
		bell = "on"
	}
	ctx := r.Context()
	if err := s.cfg.Store.SetSettings(ctx, map[string]string{settingTimeout: *in.ApprovalTimeout,
		settingRateLimit: strconv.Itoa(*in.MaxActionsPerHour), settingBell: bell}); err != nil {
		return nil, err
	}
	s.bell.on.Store(*in.Bell)
	s.publish(event{Type: "settings.changed"})
	return s.defaults(ctx)
}

// putEmergencyStop switches the emergency stop. Switching it on revokes every token
// and ends every open approval request at once (decision F1).
func (s *Server) putEmergencyStop(r *request) (any, error) {
	var in struct {
		Active *bool `json:"active"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.Active == nil {
		return nil, failField(codeInvalidInput, "/active")
	}
	changed, err := s.cfg.Agents.SetEmergencyStop(r.Context(), *in.Active, s.actor(r))
	if err != nil {
		return nil, err
	}
	if *in.Active {
		// Also when it was on already: requests asked by another process meanwhile.
		if n := s.cfg.Approvals.CancelAll(); n > 0 {
			s.cfg.Logger.Info("open approval requests ended by the emergency stop", "requests", n)
		}
	}
	if changed {
		s.cfg.Logger.Warn("emergency stop switched", "active", *in.Active, "by", r.user)
		s.publishSystem(r.Context())
	}
	return s.stopState(r.Context())
}
