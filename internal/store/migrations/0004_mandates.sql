-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Mandates (one per agent) and all their versions. A version is never changed; the
-- audit log refers to versions by digest (SPEC-v0 section 9.3).

-- +goose Up
CREATE TABLE mandates (
    id                   TEXT PRIMARY KEY NOT NULL,
    client_id            TEXT NOT NULL UNIQUE REFERENCES agents (client_id),
    status               TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    current_digest       TEXT NOT NULL,
    max_actions_per_hour INTEGER NOT NULL,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
) STRICT;

CREATE TABLE mandate_versions (
    version    INTEGER PRIMARY KEY,
    mandate_id TEXT NOT NULL REFERENCES mandates (id),
    digest     TEXT NOT NULL,
    document   TEXT NOT NULL,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL
) STRICT;

CREATE INDEX mandate_versions_mandate ON mandate_versions (mandate_id, digest);
