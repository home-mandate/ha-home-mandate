-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Approval channels per person (decision F2): any number of mobile_app devices (phone,
-- Mac app, tablet) instead of exactly one, and optionally answering in the Home-Mandate
-- UI, for critical actions only with ui_critical. At least one channel is checked in Go.

-- +goose Up
CREATE TABLE approver_devices (
    user_id        TEXT NOT NULL REFERENCES approvers (user_id) ON DELETE CASCADE,
    notify_service TEXT NOT NULL,
    PRIMARY KEY (user_id, notify_service)
) STRICT;

INSERT INTO approver_devices (user_id, notify_service) SELECT user_id, notify_service FROM approvers;

ALTER TABLE approvers DROP COLUMN notify_service;
ALTER TABLE approvers ADD COLUMN ui INTEGER NOT NULL DEFAULT 0 CHECK (ui IN (0, 1));
ALTER TABLE approvers ADD COLUMN ui_critical INTEGER NOT NULL DEFAULT 0 CHECK (ui_critical IN (0, 1) AND ui_critical <= ui);
