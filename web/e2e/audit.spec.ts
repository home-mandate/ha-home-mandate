// SPDX-License-Identifier: AGPL-3.0-or-later

// Audit log against the mock build: filters in the URL, details next to the list, the
// keyboard path, a broken chain, and the mobile view with entries on their own page.
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: { events: 'Ereignisse', ask: 'Nachfragen', all: '15 Einträge', asks: '5 Einträge', detail: 'Eintrag Nr. 8', technical: 'Technische Details',
    filters: 'Filter', today: 'Heute', broken: 'Dieser Eintrag passt nicht zur Kette. Sein Inhalt könnte verändert sein.' },
  en: { events: 'Events', ask: 'Ask first', all: '15 entries', asks: '5 entries', detail: 'Entry no. 8', technical: 'Technical details',
    filters: 'Filters', today: 'Today', broken: 'This entry doesn’t fit the chain. Its content may have been altered.' },
} as const;

type Lang = keyof typeof text;

test('filters by decision and keeps the filter in the URL across a reload', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/audit');
  await expect(page.getByRole('search').getByRole('status')).toHaveText(t.all);
  await page.getByRole('button', { name: t.ask, exact: true }).click();
  await expect(page.getByRole('search').getByRole('status')).toHaveText(t.asks);
  await expect(page).toHaveURL(/#\/audit\?decision=ask$/);
  await page.reload();
  await expect(page.getByRole('search').getByRole('status')).toHaveText(t.asks);
  await expect(page.getByRole('button', { name: t.ask, exact: true })).toHaveAttribute('aria-pressed', 'true');
});

test('shows details next to the list, reachable with the keyboard only', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/audit');
  const entries = page.getByRole('region', { name: t.events });
  await expect(entries.getByRole('heading', { name: t.today })).toBeVisible();
  const row = entries.getByRole('link').filter({ hasText: /Nr\. 8|No\. 8/ });
  await row.focus();
  await page.keyboard.press('Enter');
  const detail = page.getByRole('complementary', { name: t.detail });
  await expect(detail).toBeVisible();
  await detail.getByText(t.technical).click();
  await expect(detail.getByText(/^sha256:/).first()).toBeVisible();
  await expect(page).toHaveURL(/#\/audit\?seq=8$/);
});

test('marks entries from a broken chain on', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/audit');
  await page.evaluate(() => (window as unknown as { hmMock: { breakChain(n: number): void } }).hmMock.breakChain(10));
  const entries = page.getByRole('region', { name: t.events });
  await expect(entries.getByRole('img', { name: t.broken })).toHaveCount(6);
});

test('mobile: filters fold away, an entry opens on its own page, nothing scrolls sideways', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('./#/audit');
  await expect(page.getByRole('search').getByRole('status')).toHaveText(t.all);
  const toggle = page.getByRole('button', { name: t.filters, exact: true });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  expect(await pageScroll(page)).toBe(0);
  await page.getByRole('region', { name: t.events }).getByRole('link').filter({ hasText: /Nr\. 8|No\. 8/ }).click();
  await expect(page).toHaveURL(/#\/audit\/8$/);
  await expect(page.getByRole('heading', { level: 1, name: t.detail })).toBeVisible();
  expect(await pageScroll(page)).toBe(0);
});
