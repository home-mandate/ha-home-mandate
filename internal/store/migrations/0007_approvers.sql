-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Approvers: Home Assistant users who receive approval requests, with the notify service
-- of their phone and optionally their language (ARCHITECTURE section 7, decision 5).
-- A mandate names approvers by user ID; only those configured here can be reached.

-- +goose Up
CREATE TABLE approvers (
    user_id        TEXT PRIMARY KEY NOT NULL,
    notify_service TEXT NOT NULL,
    language       TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
) STRICT;
