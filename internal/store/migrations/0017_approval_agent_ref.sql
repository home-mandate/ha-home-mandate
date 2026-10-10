-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The ID an agent knows its approval request by (issue #27): a random value of its own,
-- neither the nonce, nor the request ID the UI knows, nor the notification tag, so that
-- none of them can be derived from what an agent sees. It is bound to the agent: only
-- the agent with this client_id finds the request by it (approval_status,
-- approval_cancel). Empty for requests made before this migration.

-- +goose Up
ALTER TABLE approval_journal ADD COLUMN agent_ref TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX approval_journal_agent_ref ON approval_journal (agent_ref) WHERE agent_ref <> '';

-- +goose Down
DROP INDEX approval_journal_agent_ref;
ALTER TABLE approval_journal DROP COLUMN agent_ref;
