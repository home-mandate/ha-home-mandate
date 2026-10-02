// SPDX-License-Identifier: AGPL-3.0-or-later

// Overview against the mock build: status tiles, pending approvals with countdown and the
// agent's claim, recent activity, keyboard use, the mobile layout and hostile agent text.
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: {
    status: 'Status auf einen Blick',
    ha: 'Home Assistant',
    pending: /Offene Rückfragen/,
    activity: 'Letzte Aktivität',
    audit: 'Zum Protokoll',
    claim: 'Angabe des Agenten, ungeprüft',
    phone: 'Freigeben oder ablehnen kannst du in der Benachrichtigung auf deinem Handy.',
  },
  en: {
    status: 'Status at a glance',
    ha: 'Home Assistant',
    pending: /Pending approvals/,
    activity: 'Recent activity',
    audit: 'Open audit log',
    claim: 'Stated by the agent, unverified',
    phone: 'Approve or decline from the notification on your phone.',
  },
} as const;

type Lang = keyof typeof text;

test('shows status, pending approvals and recent activity', async ({ page }, testInfo) => {
  const t = text[testInfo.project.name as Lang];
  await page.goto('./');
  const tiles = page.getByRole('region', { name: t.status });
  await expect(tiles.getByText(t.ha)).toBeVisible();

  const pending = page.getByRole('region', { name: t.pending });
  const card = pending.getByRole('article').first();
  await expect(card).toBeVisible();
  await expect(card.getByRole('timer')).toBeVisible();
  await expect(card.getByTitle(t.claim).first()).toBeVisible();
  await expect(pending.getByText(t.phone)).toBeVisible();

  const activity = page.getByRole('region', { name: t.activity });
  await expect(activity.getByRole('listitem')).toHaveCount(5);
  await expect(activity.getByRole('link', { name: t.audit })).toHaveAttribute('href', '#/audit');
});

test('hostile agent text stays text: no links, no hidden characters, no line breaks', async ({ page }, testInfo) => {
  const t = text[testInfo.project.name as Lang];
  await page.goto('./');
  const card = page.getByRole('region', { name: t.pending }).getByRole('article').first();
  await expect(card).toBeVisible();
  expect(await card.locator('a').count()).toBe(0);
  const quote = await card.locator('blockquote').innerText();
  expect(quote).not.toMatch(/[\n\u202A-\u202E\u2066-\u2069]/);
});

test('the keyboard reaches the audit log link with a visible focus', async ({ page }, testInfo) => {
  const t = text[testInfo.project.name as Lang];
  await page.goto('./');
  const link = page.getByRole('link', { name: t.audit });
  await expect(link).toBeVisible();
  for (let i = 0; i < 40 && !(await link.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press('Tab');
  await expect(link).toBeFocused();
  const outline = await link.evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(outline).not.toBe('none');
});

test('at 375 px nothing scrolls sideways', async ({ page }, testInfo) => {
  const t = text[testInfo.project.name as Lang];
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('./');
  await expect(page.getByRole('region', { name: t.activity }).getByRole('listitem').first()).toBeVisible();
  expect(await pageScroll(page)).toBe(0);
});
