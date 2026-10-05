-- SPDX-License-Identifier: AGPL-3.0-or-later
-- directory.changed entries name a device in directory.entity_id and, for a rename, its
-- former ID in directory.previous_entity_id: the filter by device of the audit log finds
-- them as well.

-- +goose Up
ALTER TABLE audit_log ADD COLUMN directory_entity_id TEXT GENERATED ALWAYS AS (json_extract(entry, '$.directory.entity_id')) VIRTUAL;
ALTER TABLE audit_log ADD COLUMN directory_previous_id TEXT GENERATED ALWAYS AS (json_extract(entry, '$.directory.previous_entity_id')) VIRTUAL;
CREATE INDEX audit_log_directory_entity_id ON audit_log (directory_entity_id, seq);
CREATE INDEX audit_log_directory_previous_id ON audit_log (directory_previous_id, seq);

-- +goose Down
DROP INDEX audit_log_directory_previous_id;
DROP INDEX audit_log_directory_entity_id;
ALTER TABLE audit_log DROP COLUMN directory_previous_id;
ALTER TABLE audit_log DROP COLUMN directory_entity_id;
