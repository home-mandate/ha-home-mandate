-- SPDX-License-Identifier: AGPL-3.0-or-later
-- DPoP (RFC 9449, issues #62-#65): a token family can be bound to the key of the agent's
-- client. tokens.dpop_jkt is the RFC 7638 thumbprint of that key, base64url of a SHA-256
-- (43 characters); NULL for a Bearer token. agents.binding says whether the agent's tokens
-- must be bound: it changes from none to dpop with the first bound token and never back,
-- so that a bound agent cannot be downgraded to Bearer. Back to Bearer only by removing the
-- agent and pairing anew.

-- +goose Up
ALTER TABLE tokens ADD COLUMN dpop_jkt TEXT CHECK (dpop_jkt IS NULL OR length(dpop_jkt) = 43);
ALTER TABLE agents ADD COLUMN binding TEXT NOT NULL DEFAULT 'none' CHECK (binding IN ('none', 'dpop'));

-- +goose StatementBegin
CREATE TRIGGER agents_binding_only_to_dpop BEFORE UPDATE OF binding ON agents
WHEN OLD.binding = 'dpop' AND NEW.binding <> 'dpop'
BEGIN
    SELECT RAISE(ABORT, 'agents.binding: dpop never changes back');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tokens_dpop_jkt_fixed BEFORE UPDATE OF dpop_jkt ON tokens
WHEN OLD.dpop_jkt IS NOT NULL AND (NEW.dpop_jkt IS NULL OR NEW.dpop_jkt <> OLD.dpop_jkt)
BEGIN
    SELECT RAISE(ABORT, 'tokens.dpop_jkt: a bound token stays bound to its key');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER tokens_dpop_jkt_fixed;
DROP TRIGGER agents_binding_only_to_dpop;
ALTER TABLE agents DROP COLUMN binding;
ALTER TABLE tokens DROP COLUMN dpop_jkt;
