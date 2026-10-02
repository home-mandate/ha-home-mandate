// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const settingHousehold = "household"

// Setting returns the value of key and whether it exists.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: read setting %s: %w", key, err)
	}
	return value, true, nil
}

// SetSetting stores value under key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: write setting %s: %w", key, err)
	}
	return nil
}

// Household returns the principal of this installation ("household:hm-<12 hex>",
// SPEC-v0 section 3), creating it with crypto/rand on first use.
func (s *Store) Household(ctx context.Context) (string, error) {
	var b [6]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	candidate := "household:hm-" + hex.EncodeToString(b[:])
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO NOTHING`, settingHousehold, candidate, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("store: create household: %w", err)
	}
	value, ok, err := s.Setting(ctx, settingHousehold)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("store: household missing after creation")
	}
	return value, nil
}
