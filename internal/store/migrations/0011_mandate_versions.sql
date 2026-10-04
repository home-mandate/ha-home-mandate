-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The highest version Home-Mandate issued for a mandate (SPEC-v0 section 3.5). It stays
-- after a revocation, so an older version is never accepted again (rollback). Mandates
-- stored before versions existed start at 0: their next change gets version 1.

-- +goose Up
ALTER TABLE mandates ADD COLUMN highest_version INTEGER NOT NULL DEFAULT 0 CHECK (highest_version >= 0);

-- +goose Down
ALTER TABLE mandates DROP COLUMN highest_version;
