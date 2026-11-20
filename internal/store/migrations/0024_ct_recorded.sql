-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Certificates recorded with cert.unexpected (audit/v1, SPEC-v0 section 9.2): once for each
-- certificate and status, not at every check. status is alarm, not_yet_valid or
-- past_exposure; digest as in ct_certs.

-- +goose Up
CREATE TABLE ct_recorded (
    digest TEXT NOT NULL CHECK (length(digest) = 43),
    status TEXT NOT NULL CHECK (status IN ('alarm', 'not_yet_valid', 'past_exposure')),
    PRIMARY KEY (digest, status)
) STRICT;

-- +goose Down
DROP TABLE ct_recorded;
