-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Admission of agents via OAuth: the OAuth client an agent was admitted with (a Client
-- ID Metadata Document URL, verified, or a free identifier from a pairing code), and the
-- mandate templates a human picks from when admitting an agent.

-- +goose Up
ALTER TABLE agents ADD COLUMN oauth_client TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN client_verified INTEGER NOT NULL DEFAULT 0 CHECK (client_verified IN (0, 1));

CREATE TABLE mandate_templates (
    name       TEXT PRIMARY KEY NOT NULL,
    document   TEXT NOT NULL,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL
) STRICT;
