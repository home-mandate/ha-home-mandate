-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Audit mode (SPEC-v0 sections 4.6 and 11.6): a version of a mandate that switches audit
-- mode on waits here until a human of the household confirms exactly this version (its
-- digest). The current version stays in force meanwhile. base_digest is the version the
-- proposal was made on; once another version is in force, the proposal is stale.

-- +goose Up
CREATE TABLE mandate_pending (
    mandate_id TEXT PRIMARY KEY REFERENCES mandates (id) ON DELETE CASCADE,
    digest TEXT NOT NULL CHECK (length(digest) = 71),
    document TEXT NOT NULL CHECK (json_valid(document)),
    base_digest TEXT NOT NULL,
    proposed_by TEXT NOT NULL,
    proposed_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE mandate_pending;
