-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Repeated approval requests (issue #27, part C). state_before is the device's state when
-- the request was made, as the approvers were shown it (SPEC-v0 section 11.1 item 10);
-- empty when it was unknown. idempotency_key is the optional key an agent gave
-- perform_action; the same agent with the same key is tied to this request for as long as
-- the row exists (24 hours after the end). The index finds the last executed request of an
-- agent for a device and call (replay window).

-- +goose Up
ALTER TABLE approval_journal ADD COLUMN state_before TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_journal ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX approval_journal_idempotency ON approval_journal (client_id, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX approval_journal_call ON approval_journal (client_id, entity_id, params_digest, ended_at);

-- +goose Down
DROP INDEX approval_journal_call;
DROP INDEX approval_journal_idempotency;
ALTER TABLE approval_journal DROP COLUMN idempotency_key;
ALTER TABLE approval_journal DROP COLUMN state_before;
