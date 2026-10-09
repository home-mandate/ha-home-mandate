-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Where the rules of a mandate version came from, for display only (the evaluation does
-- not read it): 'template' names the template and the digest of its content, 'edit' is a
-- human's edit (editor, command line, a rename taken over). Versions stored before have
-- no known origin.

-- +goose Up
ALTER TABLE mandate_versions ADD COLUMN origin TEXT NOT NULL DEFAULT 'unknown'
    CHECK (origin IN ('unknown', 'template', 'edit'));
ALTER TABLE mandate_versions ADD COLUMN template_name TEXT NOT NULL DEFAULT '';
ALTER TABLE mandate_versions ADD COLUMN template_digest TEXT NOT NULL DEFAULT ''
    CHECK ((origin = 'template') = (template_name <> '' AND template_digest <> '')
        AND (origin = 'template' OR (template_name = '' AND template_digest = '')));
CREATE INDEX mandate_versions_template ON mandate_versions (mandate_id, version) WHERE origin = 'template';

-- +goose Down
DROP INDEX mandate_versions_template;
ALTER TABLE mandate_versions DROP COLUMN template_digest;
ALTER TABLE mandate_versions DROP COLUMN template_name;
ALTER TABLE mandate_versions DROP COLUMN origin;
