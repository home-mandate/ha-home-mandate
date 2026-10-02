// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// serve runs the gateway until ctx ends. It refuses to start with a broken audit log
// and ends with exitFailure when Home Assistant rejects the access token.
func serve(ctx context.Context, e env) int {
	s, err := openState(ctx, e)
	if ctx.Err() != nil {
		return exitOK // stopped during start-up
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", err)
		return exitFailure
	}
	defer s.store.Close()
	logger := slog.New(slog.NewJSONHandler(e.stderr, &slog.HandlerOptions{Level: s.cfg.LogLevel}))

	r, err := s.log.Verify(ctx)
	if err != nil || !r.Valid {
		logger.Error("audit log is broken, not starting", "broken_at", r.BrokenAt, "error", err)
		return exitFailure
	}

	client, err := ha.New(ha.Config{URL: s.cfg.HAURL, Token: s.cfg.HAToken, Logger: logger})
	if err != nil {
		logger.Error("invalid Home Assistant configuration", "error", err)
		return exitFailure
	}
	logger.Info("home-mandate started", "version", version, "mode", s.cfg.Mode, "household", s.household)
	if err := client.Run(ctx); errors.Is(err, ha.ErrAuthInvalid) {
		logger.Error("Home Assistant rejected the access token")
		return exitFailure
	}
	return exitOK
}
