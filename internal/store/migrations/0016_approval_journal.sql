-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The approval journal (issue #27, SPEC-v0 section 11.1 item 9): every approval request
-- outside the process, so that a restart ends the waiting ones visibly and never
-- executes one afterwards. A row is written before anyone is notified (open), set to
-- executing and committed before Home Assistant is called after a confirmation, and
-- ended in the transaction of the audit entry that records the end. At start every open
-- row becomes cancelled/interrupted and every executing row failed/outcome_unknown.
-- Ended rows are deleted after 24 hours.
--
-- id is the request's random ID (the one the UI knows), never the nonce of the
-- notification: no token, nonce or credential is stored (SPEC-v0 section 9.1), so a
-- journal read from a backup confirms nothing. tag is the Companion App's notification
-- tag, a separate random value, with which the notifications are cleared or replaced.
-- params_digest is the SHA-256 of the effective call (domain, service and data,
-- RFC 8785). notified lists the devices asked (user, notify service, language) as JSON;
-- decision holds agent, request, mandate and evaluation of the audit entry as JSON;
-- result the audit result once ended. notice is the notification still owed to the
-- approvers after a restart (interrupted, outcome_unknown), empty once sent.

-- +goose Up
CREATE TABLE approval_journal (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(id) = 32),
    client_id     TEXT NOT NULL,
    entity_id     TEXT NOT NULL,
    action        TEXT NOT NULL,
    params_digest TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('open', 'executing', 'ended')),
    tag           TEXT NOT NULL,
    notified      TEXT NOT NULL,
    decision      TEXT NOT NULL,
    outcome       TEXT NOT NULL DEFAULT '',
    cause         TEXT NOT NULL DEFAULT '',
    answered_by   TEXT NOT NULL DEFAULT '',
    answered_via  TEXT NOT NULL DEFAULT '',
    answered_at   TEXT,
    result        TEXT,
    notice        TEXT NOT NULL DEFAULT '' CHECK (notice IN ('', 'interrupted', 'outcome_unknown')),
    ended_at      TEXT,
    CHECK ((state = 'ended') = (ended_at IS NOT NULL))
) STRICT;
CREATE INDEX approval_journal_state ON approval_journal (state, ended_at);

-- A request only moves forward: open → executing → ended, or open → ended.
-- +goose StatementBegin
CREATE TRIGGER approval_journal_forward BEFORE UPDATE OF state ON approval_journal
WHEN NOT (OLD.state = NEW.state OR (OLD.state = 'open' AND NEW.state IN ('executing', 'ended'))
          OR (OLD.state = 'executing' AND NEW.state = 'ended'))
BEGIN
    SELECT RAISE(ABORT, 'an approval request only moves forward');
END;
-- +goose StatementEnd

-- A request enters the journal open, never already executing or ended.
-- +goose StatementBegin
CREATE TRIGGER approval_journal_insert_open BEFORE INSERT ON approval_journal
WHEN NEW.state <> 'open'
BEGIN
    SELECT RAISE(ABORT, 'an approval request enters the journal open');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER approval_journal_insert_open;
DROP TRIGGER approval_journal_forward;
DROP INDEX approval_journal_state;
DROP TABLE approval_journal;
