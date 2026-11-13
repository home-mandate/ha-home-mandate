-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Certificate Transparency monitoring of the public name (issue #66, RELAY-v0 section
-- 6.7). Times are RFC 3339 in UTC with milliseconds; digests are SHA-256 in base64url
-- without padding (43 characters).
--
-- ct_names: each public name watched, with the first start of monitoring, which takes
-- the place of the pairing of RELAY-v0.
-- ct_keys: the history of the gateway's server keys (digest of the SubjectPublicKeyInfo).
-- The row without replaced is the current key; replaced is set once and never changes,
-- because an earlier key counts only for certificates logged before it was replaced.
-- ct_certs: the certificates found for a watched name, identified by the digest of the
-- TBSCertificate without poison and SCT list (a precertificate and its certificate are
-- one). logged is the earliest time of logging any source told, NULL if none did.
-- notified_at: when the household was told of it.
-- ct_acks: acknowledgements. They name the digest and nothing of the certificate (no
-- name, issuer, serial or key); by and via say who confirmed it and how.
-- ct_sources: per source and name, the cursor of the next query and the outcome of the
-- last ones; monitoring is not possible when no source succeeded for 24 hours.

-- +goose Up
CREATE TABLE ct_names (
    name        TEXT PRIMARY KEY CHECK (length(name) BETWEEN 1 AND 253),
    watch_since TEXT NOT NULL
) STRICT;

CREATE TABLE ct_keys (
    id         INTEGER PRIMARY KEY,
    public_key TEXT NOT NULL CHECK (length(public_key) = 43),
    since      TEXT NOT NULL,
    replaced   TEXT
) STRICT;

CREATE UNIQUE INDEX ct_keys_one_current ON ct_keys (replaced IS NULL) WHERE replaced IS NULL;

-- +goose StatementBegin
CREATE TRIGGER ct_keys_replaced_once BEFORE UPDATE ON ct_keys
WHEN OLD.replaced IS NOT NULL OR NEW.public_key <> OLD.public_key OR NEW.since <> OLD.since
BEGIN
    SELECT RAISE(ABORT, 'ct_keys: an earlier key stays as it was replaced');
END;
-- +goose StatementEnd

CREATE TABLE ct_certs (
    digest      TEXT PRIMARY KEY CHECK (length(digest) = 43),
    names       TEXT NOT NULL CHECK (json_valid(names) AND json_type(names) = 'array'),
    issuer      TEXT NOT NULL CHECK (length(issuer) <= 1024),
    serial      TEXT NOT NULL CHECK (length(serial) <= 128),
    not_before  TEXT NOT NULL,
    not_after   TEXT NOT NULL,
    public_key  TEXT NOT NULL CHECK (length(public_key) = 43),
    logged      TEXT,
    first_seen  TEXT NOT NULL,
    notified_at TEXT
) STRICT;

CREATE TABLE ct_acks (
    digest TEXT PRIMARY KEY CHECK (length(digest) = 43),
    by     TEXT NOT NULL CHECK (length(by) BETWEEN 1 AND 64),
    via    TEXT NOT NULL CHECK (via IN ('push', 'ui')),
    at     TEXT NOT NULL
) STRICT;

CREATE TABLE ct_sources (
    source       TEXT NOT NULL CHECK (source IN ('certspotter', 'crtsh')),
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 253),
    cursor       TEXT NOT NULL DEFAULT '' CHECK (length(cursor) <= 64),
    last_success TEXT,
    last_failure TEXT,
    failure      TEXT NOT NULL DEFAULT '' CHECK (length(failure) <= 256),
    PRIMARY KEY (source, name)
) STRICT;

-- +goose Down
DROP TABLE ct_sources;
DROP TABLE ct_acks;
DROP TABLE ct_certs;
DROP TRIGGER ct_keys_replaced_once;
DROP INDEX ct_keys_one_current;
DROP TABLE ct_keys;
DROP TABLE ct_names;
