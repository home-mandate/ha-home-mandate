-- SPDX-License-Identifier: AGPL-3.0-or-later
-- OAuth tokens replace the long-lived tokens of the administration commands. Only the
-- SHA-256 hash of a token is stored. Tokens of one authorization share a family_id:
-- reusing a rotated refresh token revokes the whole family. resource is the RFC 8707
-- resource the tokens are bound to.

-- +goose Up
DROP INDEX agent_tokens_client_id;
DROP TABLE agent_tokens;

CREATE TABLE tokens (
    token_hash BLOB PRIMARY KEY NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    client_id  TEXT NOT NULL REFERENCES agents (client_id),
    family_id  TEXT NOT NULL,
    resource   TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    revoked_at TEXT
) STRICT;

CREATE INDEX tokens_client_id ON tokens (client_id);
CREATE INDEX tokens_family_id ON tokens (family_id);
