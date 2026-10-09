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

-- Only a revoked agent or mandate is removed, and a removed one never becomes active again
-- (updating status or removed_at).
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

-- A row is never stored as removed unless it is revoked.
-- +goose StatementBegin
CREATE TRIGGER agents_insert_removed_revoked BEFORE INSERT ON agents
WHEN NEW.removed_at IS NOT NULL AND NEW.status <> 'revoked'
BEGIN
    SELECT RAISE(ABORT, 'only a revoked agent is removed');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER mandates_insert_removed_revoked BEFORE INSERT ON mandates
WHEN NEW.removed_at IS NOT NULL AND NEW.status <> 'revoked'
BEGIN
    SELECT RAISE(ABORT, 'only a revoked mandate is removed');
END;
-- +goose StatementEnd

-- A removal and the deletion of the data are never undone.
-- +goose StatementBegin
CREATE TRIGGER agents_removal_final BEFORE UPDATE OF removed_at, purged_at ON agents
WHEN (OLD.removed_at IS NOT NULL AND NEW.removed_at IS NULL) OR (OLD.purged_at IS NOT NULL AND NEW.purged_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'a removal is never undone');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER mandates_removal_final BEFORE UPDATE OF removed_at, purged_at ON mandates
WHEN (OLD.removed_at IS NOT NULL AND NEW.removed_at IS NULL) OR (OLD.purged_at IS NOT NULL AND NEW.purged_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'a removal is never undone');
END;
-- +goose StatementEnd

ALTER TABLE audit_log ADD COLUMN mandate_id TEXT GENERATED ALWAYS AS (json_extract(entry, '$.mandate.id')) VIRTUAL;
CREATE INDEX audit_log_mandate_id ON audit_log (mandate_id, seq);

-- +goose Down
DROP INDEX audit_log_mandate_id;
ALTER TABLE audit_log DROP COLUMN mandate_id;
DROP TRIGGER mandates_removal_final;
DROP TRIGGER agents_removal_final;
DROP TRIGGER mandates_insert_removed_revoked;
DROP TRIGGER agents_insert_removed_revoked;
DROP TRIGGER mandates_removed_stay_revoked;
DROP TRIGGER agents_removed_stay_revoked;
ALTER TABLE mandates DROP COLUMN purged_at;
ALTER TABLE mandates DROP COLUMN removed_by;
ALTER TABLE mandates DROP COLUMN removed_at;
ALTER TABLE agents DROP COLUMN purged_at;
ALTER TABLE agents DROP COLUMN removed_by;
ALTER TABLE agents DROP COLUMN removed_at;
