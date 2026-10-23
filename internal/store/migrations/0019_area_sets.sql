-- SPDX-License-Identifier: AGPL-3.0-or-later
-- A resource has a set of areas (SPEC-v0 section 3.4): request.resource.areas replaces
-- request.resource.area in decision entries. The column of the single area goes; filters
-- and the search look into the set (json_each).
--
-- directory_areas is the set of areas last seen per entity, area_moves an entity whose set
-- changed since a human last looked: rules by area stop or start applying to it, and the
-- household is told which mandates that affects. previous is the set when the move was
-- found; a later move keeps it, a move back removes the row.

-- +goose Up
DROP INDEX audit_log_area;
ALTER TABLE audit_log DROP COLUMN area;

CREATE TABLE directory_areas (
    entity_id TEXT PRIMARY KEY CHECK (length(entity_id) BETWEEN 1 AND 255),
    areas TEXT NOT NULL CHECK (json_valid(areas) AND json_type(areas) = 'array')
) STRICT;

CREATE TABLE area_moves (
    entity_id TEXT PRIMARY KEY CHECK (length(entity_id) BETWEEN 1 AND 255),
    previous TEXT NOT NULL CHECK (json_valid(previous) AND json_type(previous) = 'array'),
    current TEXT NOT NULL CHECK (json_valid(current) AND json_type(current) = 'array'),
    moved_at TEXT NOT NULL,
    CHECK (previous <> current)
) STRICT;

-- +goose Down
DROP TABLE area_moves;
DROP TABLE directory_areas;
ALTER TABLE audit_log ADD COLUMN area TEXT GENERATED ALWAYS AS (json_extract(entry, '$.request.resource.area')) VIRTUAL;
CREATE INDEX audit_log_area ON audit_log (area, seq);
