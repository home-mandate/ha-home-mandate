-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Entities the household marked as critical (SPEC-v0 section 4, step 5): every action on
-- them except read needs a confirmation or allow_critical, whatever their category. A
-- switch that drives a door opener is the typical case.

-- +goose Up
CREATE TABLE critical_entities (
    entity_id TEXT PRIMARY KEY CHECK (length(entity_id) BETWEEN 1 AND 255),
    marked_at TEXT NOT NULL,
    marked_by TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE critical_entities;
