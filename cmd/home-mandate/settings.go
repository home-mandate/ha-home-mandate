// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/config"
	"github.com/home-mandate/ha-home-mandate/internal/dpop"
)

// recordSettings records with config.changed what changed since the last start among the
// settings that come from the configuration (SPEC-v0 section 9.2): the key that signs
// checkpoints and, in audit/v1, the public origin, remote access, whether unbound tokens
// are refused and the trusted proxy. The time zone comes from Home Assistant
// (gateway.readConfig). A setting that cannot be recorded is logged and stays as it was.
func recordSettings(ctx context.Context, log *audit.Log, cfg config.Config, signer *audit.Signer, logger *slog.Logger) {
	kid := ""
	if signer != nil {
		kid = signer.KeyID
	}
	remote, unbound := "off", "allowed"
	if cfg.PublicURL != "" {
		remote = "on"
	}
	if cfg.DPoP == dpop.ModeRequired {
		unbound = "refused"
	}
	for _, s := range []struct{ setting, value string }{
		{audit.SettingCheckpointKey, kid},
		{audit.SettingPublicOrigin, cfg.PublicURL},
		{audit.SettingRemoteAccess, remote},
		{audit.SettingUnboundTokens, unbound},
	} {
		if err := log.RecordSetting(ctx, s.setting, s.value); err != nil {
			logger.Warn("cannot record a change of a setting in the audit log", "setting", s.setting, "error", err)
		}
	}
	var proxies []string
	if cfg.Proxy.IsValid() {
		proxies = append(proxies, cfg.Proxy.String())
	}
	if err := log.RecordSettings(ctx, audit.SettingTrustedProxy, proxies); err != nil {
		logger.Warn("cannot record a change of a setting in the audit log", "setting", audit.SettingTrustedProxy, "error", err)
	}
}
