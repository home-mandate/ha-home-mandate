-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Execution intents (SPEC-v0 section 9, recording before executing): a direct action (an
-- allow, no confirmation) is entered here and committed before Home Assistant is called,
-- and the row is deleted in the transaction of the audit entry that records the result.
-- Without the row, nothing is dispatched: if it cannot be written, the request is denied
-- with denied_by audit. At start, every row left by a previous process is recorded as
-- failed/outcome_unknown and deleted: the command may have reached Home Assistant.
-- Confirmed actions have their intent in approval_journal (state executing).
--
-- decision holds agent, request, mandate and evaluation of the audit entry as JSON; no
-- token, nonce or credential (SPEC-v0 section 9.1).

-- +goose Up
CREATE TABLE execution_intents (
    id         TEXT PRIMARY KEY NOT NULL CHECK (length(id) = 32),
    created_at TEXT NOT NULL,
    decision   TEXT NOT NULL CHECK (json_valid(decision))
) STRICT;

-- +goose Down
DROP TABLE execution_intents;
