// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import "log/slog"

const redacted = "[redacted]"

// Secret holds the Home Assistant access token. It formats and logs as "[redacted]";
// only the authentication message reads the actual value.
type Secret string

func (Secret) String() string { return redacted }

// GoString keeps %#v from printing the value.
func (Secret) GoString() string { return `ha.Secret("` + redacted + `")` }

// LogValue keeps slog from printing the value.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText keeps encoders (JSON, slog's JSON handler) from printing the value.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
