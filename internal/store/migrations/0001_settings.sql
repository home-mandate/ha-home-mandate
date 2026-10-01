-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Migrations are never edited once released and never rolled back (no Down section):
-- the store refuses changed checksums and databases newer than the binary.

-- +goose Up
CREATE TABLE settings (
    key        TEXT PRIMARY KEY NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
