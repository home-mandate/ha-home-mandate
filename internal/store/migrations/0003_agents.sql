-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Agents and their access tokens. Only the SHA-256 hash of a token is stored.

-- +goose Up
CREATE TABLE agents (
    client_id    TEXT PRIMARY KEY NOT NULL,
    display_name TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    created_at   TEXT NOT NULL,
    created_by   TEXT NOT NULL,
    revoked_at   TEXT
) STRICT;

CREATE TABLE agent_tokens (
    token_hash BLOB PRIMARY KEY NOT NULL,
    client_id  TEXT NOT NULL REFERENCES agents (client_id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
) STRICT;

CREATE INDEX agent_tokens_client_id ON agent_tokens (client_id);
