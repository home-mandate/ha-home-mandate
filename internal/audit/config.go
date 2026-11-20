// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// configActor is the actor of config.changed: Home-Mandate notices the change when it
// reads its configuration or Home Assistant's.
var configActor = Actor{Kind: ActorSystem, ID: "config"}

// settingKey is the key under which the last recorded value of a setting is kept.
func settingKey(setting string) string { return "audit_config_" + setting }

// RecordSetting records a change of setting with config.changed (SPEC-v0 section 9.2):
// value is the value now, empty if the setting has none (it was removed). The first value
// seen is kept as the starting point without an entry: what it was before is not known. A
// value the log cannot record is an error, and the setting keeps its last recorded value.
func (l *Log) RecordSetting(ctx context.Context, setting, value string) error {
	if l.unchanged(ctx, setting, value) {
		return nil
	}
	return l.inTx(ctx, func(tx *sql.Tx) error {
		previous, known, err := lastSetting(ctx, tx, setting)
		if err != nil || known && previous == value {
			return err
		}
		if known {
			if _, err := l.AppendTx(ctx, tx, Entry{Event: EventConfigChanged, Actor: &configActor,
				Config: &Config{Setting: setting, Value: value, PreviousValue: previous}}); err != nil {
				return err
			}
		}
		return keepSetting(ctx, tx, setting, value, l.clock())
	})
}

// RecordSettings records a setting that is a set, such as the trusted proxies: one entry
// for each value removed (previous_value alone) and for each added (value alone), removals
// first, each in order. The first set seen is the starting point, as with RecordSetting.
func (l *Log) RecordSettings(ctx context.Context, setting string, values []string) error {
	now := slices.Compact(slices.Sorted(slices.Values(values)))
	list, err := json.Marshal(append([]string{}, now...))
	if err != nil {
		return fmt.Errorf("audit: write setting %s: %w", setting, err)
	}
	if l.unchanged(ctx, setting, string(list)) {
		return nil
	}
	return l.inTx(ctx, func(tx *sql.Tx) error {
		stored, known, err := lastSetting(ctx, tx, setting)
		if err != nil {
			return err
		}
		var before []string
		if known && json.Unmarshal([]byte(stored), &before) != nil {
			return fmt.Errorf("audit: read setting %s: not a list", setting)
		}
		if known {
			for _, v := range before {
				if !slices.Contains(now, v) {
					if err := l.appendConfig(ctx, tx, Config{Setting: setting, PreviousValue: v}); err != nil {
						return err
					}
				}
			}
			for _, v := range now {
				if !slices.Contains(before, v) {
					if err := l.appendConfig(ctx, tx, Config{Setting: setting, Value: v}); err != nil {
						return err
					}
				}
			}
		}
		return keepSetting(ctx, tx, setting, string(list), l.clock())
	})
}

func (l *Log) appendConfig(ctx context.Context, tx *sql.Tx, c Config) error {
	_, err := l.AppendTx(ctx, tx, Entry{Event: EventConfigChanged, Actor: &configActor, Config: &c})
	return err
}

// unchanged reports, without a write transaction, whether setting is known with value: the
// common case, which then takes no write lock. Anything else, a failed read included, goes
// on to the transaction, which reads again.
func (l *Log) unchanged(ctx context.Context, setting, value string) bool {
	stored, known, err := lastSetting(ctx, l.db, setting)
	return err == nil && known && stored == value
}

// lastSetting reads the last recorded value of setting; known is false if none was.
func lastSetting(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, setting string) (value string, known bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKey(setting)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("audit: read setting %s: %w", setting, err)
	}
	return value, true, nil
}

func keepSetting(ctx context.Context, tx *sql.Tx, setting, value string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		settingKey(setting), value, now.UTC().Format(timeFormat)); err != nil {
		return fmt.Errorf("audit: write setting %s: %w", setting, err)
	}
	return nil
}

// RecordCTStatus records a change of the state of Certificate Transparency monitoring with
// ct.status (audit/v1, SPEC-v0 section 9.2): ok, limited or unavailable. Unlike a setting,
// the first state is recorded too, without a previous one.
func (l *Log) RecordCTStatus(ctx context.Context, status string) error {
	if l.unchanged(ctx, "ct_status", status) {
		return nil
	}
	return l.inTx(ctx, func(tx *sql.Tx) error {
		previous, known, err := lastSetting(ctx, tx, "ct_status")
		if err != nil || known && previous == status {
			return err
		}
		if _, err := l.AppendTx(ctx, tx, Entry{Event: EventCTStatus, CT: &CT{Status: status, PreviousStatus: previous}}); err != nil {
			return err
		}
		return keepSetting(ctx, tx, "ct_status", status, l.clock())
	})
}
