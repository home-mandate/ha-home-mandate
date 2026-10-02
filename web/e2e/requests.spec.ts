// SPDX-License-Identifier: AGPL-3.0-or-later

// Approval requests against the mock build: pending and history, answering in the UI
// (decision F2) with the inline confirmation, the keyboard path and the mobile switch.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: { open: /^Offen/, history: 'Verlauf', decline: 'Ablehnen', approve: 'Freigeben', yes: 'Ja, freigeben', confirm: 'Freigabe bestätigen',
    declined: /^Abgelehnt von \u2068?Markus\u2069? · in Home-Mandate/, approved: /^Freigegeben von \u2068?Markus\u2069? · in Home-Mandate/ },
  en: { open: /^Pending/, history: 'History', decline: 'Decline', approve: 'Approve', yes: 'Yes, approve', confirm: 'Confirm approval',
    declined: /^Declined by \u2068?Markus\u2069? · in Home-Mandate/, approved: /^Approved by \u2068?Markus\u2069? · in Home-Mandate/ },
} as const;

type Lang = keyof typeof text;

/** answerable opens a request the signed-in person may answer here. */
async function answerable(page: Page, id: string) {
  await page.evaluate((requestId) => {
    const mock = (window as unknown as { hmMock: { openApproval(r: unknown): void } }).hmMock;
    const now = Date.now();
    mock.openApproval({
      id: requestId,
      agent: { client_id: 'https://claude.ai/oauth/claude-code-client-metadata', display_name: 'Claude Code' },
      entity_id: 'lock.front_door',
      device_name: 'Haustür',
      area: 'hallway',
      action: 'unlock',
      critical: true,
      reason: 'Der Paketbote ist da',
      recipients: ['Markus'],
      created_at: new Date(now).toISOString(),
      expires_at: new Date(now + 120_000).toISOString(),
      can_answer: true,
    });
  }, id);
}

test('declines in the UI: the card leaves, the history grows', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/audit/requests');
  const pending = page.getByRole('region', { name: t.open });
  const history = page.getByRole('region', { name: t.history });
  await expect(history.getByRole('listitem')).toHaveCount(5);
  await answerable(page, 'apr-e2e');
  await expect(pending.getByRole('article')).toHaveCount(2);
  await pending.getByRole('button', { name: t.decline }).click();
  await expect(pending.getByRole('article')).toHaveCount(1);
  await expect(history.getByRole('listitem')).toHaveCount(6);
  await expect(history.getByText(t.declined)).toBeVisible();
});

test('approves with the keyboard only, after the inline confirmation', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/audit/requests');
  await answerable(page, 'apr-keys');
  const pending = page.getByRole('region', { name: t.open });
  const approve = pending.getByRole('button', { name: t.approve, exact: true });
  await approve.focus();
  await page.keyboard.press('Enter');
  const group = pending.getByRole('group', { name: t.confirm });
  await expect(group.getByRole('button', { name: t.yes })).toBeFocused();
  // The confirmation counts only after a short pause.
  await page.waitForTimeout(700);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('region', { name: t.history }).getByText(t.approved)).toBeVisible();
});

test('a quick second Enter (held key, double press) does not approve', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/audit/requests');
  await answerable(page, 'apr-held');
  const pending = page.getByRole('region', { name: t.open });
  const history = page.getByRole('region', { name: t.history });
  await expect(history.getByRole('listitem')).toHaveCount(5);
  await pending.getByRole('button', { name: t.approve, exact: true }).focus();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
  await page.keyboard.down('Enter');
  await page.keyboard.up('Enter');
  await page.waitForTimeout(300);
  await expect(history.getByRole('listitem')).toHaveCount(5);
  await expect(pending.getByRole('group', { name: t.confirm })).toBeVisible();
});

test('mobile: a switch between pending and history, nothing scrolls sideways', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('./#/audit/requests');
  const tabs = page.getByRole('tablist');
  await expect(tabs.getByRole('tab')).toHaveCount(2);
  expect(await pageScroll(page)).toBe(0);
  await tabs.getByRole('tab', { name: t.history }).click();
  await expect(page.getByRole('tabpanel', { name: t.history }).getByRole('listitem')).toHaveCount(5);
  expect(await pageScroll(page)).toBe(0);
});
