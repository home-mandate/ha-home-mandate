-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Audit log (SPEC-v0 section 9). entry is the canonical JSON (RFC 8785) of the entry,
-- digest its SHA-256; the hash chain is verified with mandate-spec/audit.

-- +goose Up
CREATE TABLE audit_log (
    seq         INTEGER PRIMARY KEY NOT NULL,
    recorded_at TEXT NOT NULL,
    event       TEXT NOT NULL,
    entry       TEXT NOT NULL,
    digest      TEXT NOT NULL
) STRICT;

CREATE INDEX audit_log_recorded_at ON audit_log (recorded_at);
