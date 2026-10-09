-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Removing revoked agents and mandates (SPEC-v0 section 11.3, issue #21). A removed one
-- is hidden from the lists at once (removed_at, removed_by) and stays revoked for good.
-- Once no audit entry refers to it any more, its data is deleted (purged_at): the row
-- stays as a tombstone with its ID, display name and, for a mandate, the highest version
-- issued, so that an older version is never accepted again (SPEC-v0 section 3.5).
-- audit_log.mandate_id lets the retention find mandates no entry refers to.

-- +goose Up
ALTER TABLE agents ADD COLUMN removed_at TEXT;
ALTER TABLE agents ADD COLUMN removed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN purged_at TEXT;
ALTER TABLE mandates ADD COLUMN removed_at TEXT;
ALTER TABLE mandates ADD COLUMN removed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE mandates ADD COLUMN purged_at TEXT;

-- Only a revoked agent or mandate is removed, and a removed one never becomes active again.
-- +goose StatementBegin
CREATE TRIGGER agents_removed_stay_revoked BEFORE UPDATE OF status, removed_at ON agents
WHEN NEW.removed_at IS NOT NULL AND NEW.status <> 'revoked'
BEGIN
    SELECT RAISE(ABORT, 'a removed agent stays revoked');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER mandates_removed_stay_revoked BEFORE UPDATE OF status, removed_at ON mandates
WHEN NEW.removed_at IS NOT NULL AND NEW.status <> 'revoked'
BEGIN
    SELECT RAISE(ABORT, 'a removed mandate stays revoked');
END;
-- +goose StatementEnd

ALTER TABLE audit_log ADD COLUMN mandate_id TEXT GENERATED ALWAYS AS (json_extract(entry, '$.mandate.id')) VIRTUAL;
CREATE INDEX audit_log_mandate_id ON audit_log (mandate_id, seq);

-- +goose Down
DROP INDEX audit_log_mandate_id;
ALTER TABLE audit_log DROP COLUMN mandate_id;
DROP TRIGGER mandates_removed_stay_revoked;
DROP TRIGGER agents_removed_stay_revoked;
ALTER TABLE mandates DROP COLUMN purged_at;
ALTER TABLE mandates DROP COLUMN removed_by;
ALTER TABLE mandates DROP COLUMN removed_at;
ALTER TABLE agents DROP COLUMN purged_at;
ALTER TABLE agents DROP COLUMN removed_by;
ALTER TABLE agents DROP COLUMN removed_at;
