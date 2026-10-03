-- SPDX-License-Identifier: AGPL-3.0-or-later
-- What the local UI needs on top of the stored data:
-- * mandates.name: the display name of a mandate (decision D2), metadata next to the
--   document, so the document and its digest stay as the specification defines them.
-- * At most one ACTIVE mandate per agent instead of one mandate per agent ever: an agent
--   whose mandate was revoked gets a new mandate (POST api/mandates) under a new ID; the
--   revoked one stays as it was. SQLite cannot drop a UNIQUE constraint, so both mandate
--   tables are rebuilt (child table first; renaming rewrites the foreign keys).
-- * agents.redirect_uris (JSON array) and agents.revoked_by: the redirect URIs an agent
--   was admitted with (never widened later) and who revoked it.
-- * Virtual columns and indexes over the audit entries for the filters of the audit log,
--   and audit_search: the folded search text of each entry (decision B2), written in the
--   same transaction as the entry and deleted with it. Neither touches the hash chain.

-- +goose Up
CREATE TABLE mandates_new (
    id                   TEXT PRIMARY KEY NOT NULL,
    client_id            TEXT NOT NULL REFERENCES agents (client_id),
    status               TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    current_digest       TEXT NOT NULL,
    max_actions_per_hour INTEGER NOT NULL,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    name                 TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO mandates_new (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
    SELECT id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at FROM mandates ORDER BY rowid;

CREATE TABLE mandate_versions_new (
    version    INTEGER PRIMARY KEY,
    mandate_id TEXT NOT NULL REFERENCES mandates_new (id),
    digest     TEXT NOT NULL,
    document   TEXT NOT NULL,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL
) STRICT;
INSERT INTO mandate_versions_new (version, mandate_id, digest, document, created_at, created_by)
    SELECT version, mandate_id, digest, document, created_at, created_by FROM mandate_versions;

DROP INDEX mandate_versions_mandate;
DROP TABLE mandate_versions;
DROP TABLE mandates;
ALTER TABLE mandates_new RENAME TO mandates;
ALTER TABLE mandate_versions_new RENAME TO mandate_versions;
CREATE INDEX mandate_versions_mandate ON mandate_versions (mandate_id, digest);
CREATE INDEX mandates_client_id ON mandates (client_id);
CREATE UNIQUE INDEX mandates_one_active ON mandates (client_id) WHERE status = 'active';
ALTER TABLE agents ADD COLUMN redirect_uris TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agents ADD COLUMN revoked_by TEXT NOT NULL DEFAULT '';

ALTER TABLE audit_log ADD COLUMN client_id TEXT GENERATED ALWAYS AS (json_extract(entry, '$.agent.client_id')) VIRTUAL;
ALTER TABLE audit_log ADD COLUMN entity_id TEXT GENERATED ALWAYS AS (json_extract(entry, '$.request.resource.entity_id')) VIRTUAL;
ALTER TABLE audit_log ADD COLUMN area TEXT GENERATED ALWAYS AS (json_extract(entry, '$.request.resource.area')) VIRTUAL;
ALTER TABLE audit_log ADD COLUMN decision TEXT GENERATED ALWAYS AS (
    CASE WHEN json_extract(entry, '$.evaluation.reason') = 'no_match' THEN 'default'
         ELSE json_extract(entry, '$.evaluation.decision') END) VIRTUAL;

CREATE INDEX audit_log_client_id ON audit_log (client_id, seq);
CREATE INDEX audit_log_entity_id ON audit_log (entity_id, seq);
CREATE INDEX audit_log_area ON audit_log (area, seq);
CREATE INDEX audit_log_event ON audit_log (event, seq);

CREATE TABLE audit_search (
    seq  INTEGER PRIMARY KEY NOT NULL REFERENCES audit_log (seq) ON DELETE CASCADE,
    text TEXT NOT NULL
) STRICT;
