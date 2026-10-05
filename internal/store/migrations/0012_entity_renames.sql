-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Renamed entities (decision H-E1, extended): a rule on the old ID keeps applying to the
-- renamed entity – the stricter of both evaluations wins – until a human takes the rename
-- over into the mandates or dismisses it. entity_registry_ids is the last seen entity ID
-- per registry ID of Home Assistant, so that renames are found after an outage as well.

-- +goose Up
CREATE TABLE entity_renames (
    old_id TEXT NOT NULL CHECK (length(old_id) BETWEEN 1 AND 255),
    new_id TEXT NOT NULL CHECK (length(new_id) BETWEEN 1 AND 255),
    seen_at TEXT NOT NULL,
    resolved_at TEXT,
    resolved_by TEXT,
    resolution TEXT CHECK (resolution IN ('applied', 'dismissed')),
    PRIMARY KEY (old_id, new_id),
    CHECK (old_id <> new_id)
) STRICT;

CREATE TABLE entity_registry_ids (
    registry_id TEXT PRIMARY KEY CHECK (length(registry_id) BETWEEN 1 AND 255),
    entity_id TEXT NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 255)
) STRICT;

-- +goose Down
DROP TABLE entity_registry_ids;
DROP TABLE entity_renames;
