// SPDX-License-Identifier: AGPL-3.0-or-later

// The frame against the mock build: header, emergency stop sheet, banners, mobile layout.
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: {
    chainBanner: 'Protokollkette beschädigt bei Eintrag Nr. 18.342',
    estop: 'Not-Aus',
    estopOn: 'Not-Aus aktiv',
    sheet: 'Not-Aus auslösen?',
    hold: /Gedrückt halten/,
    banner: 'Not-Aus aktiv',
    chain: 'Zum Eintrag',
    cancel: 'Abbrechen',
  },
  en: {
    chainBanner: 'Audit chain broken at entry no. 18,342',
    estop: 'Emergency stop',
    estopOn: 'Emergency stop on',
    sheet: 'Trigger emergency stop?',
    hold: /Press and hold/,
    banner: 'Emergency stop active',
    chain: 'Go to entry',
    cancel: 'Cancel',
  },
} as const;

test('emergency stop with the keyboard only: hold Space for 2 s', async ({ page }, info) => {
  const t = text[info.project.name as keyof typeof text];
  await page.goto('./');
  await page.getByRole('button', { name: t.estop, exact: true }).focus();
  await page.keyboard.press('Enter');
  const sheet = page.getByRole('alertdialog', { name: t.sheet });
  await expect(sheet).toBeVisible();
  await expect(sheet.getByRole('button', { name: t.hold })).toBeFocused();

  // Released early: nothing happens.
  await page.keyboard.down(' ');
  await page.waitForTimeout(800);
  await page.keyboard.up(' ');
  await expect(sheet).toBeVisible();

  await page.keyboard.down(' ');
  await page.waitForTimeout(2300);
  await page.keyboard.up(' ');
  await expect(sheet).toBeHidden();
  await expect(page.getByRole('button', { name: t.estopOn })).toBeVisible();
  await expect(page.getByRole('alert').filter({ hasText: t.banner })).toBeVisible();
});

test('Escape and Cancel close the sheet and return focus', async ({ page }, info) => {
  const t = text[info.project.name as keyof typeof text];
  await page.goto('./');
  const trigger = page.getByRole('button', { name: t.estop, exact: true });
  await trigger.click();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('alertdialog')).toBeHidden();
  await expect(trigger).toBeFocused();
  await trigger.click();
  await page.getByRole('button', { name: t.cancel }).click();
  await expect(page.getByRole('alertdialog')).toBeHidden();
});

test('a broken audit chain shows a banner that leads to the entry', async ({ page }, info) => {
  const t = text[info.project.name as keyof typeof text];
  await page.goto('./');
  await page.evaluate(() => (window as unknown as { hmMock: { breakChain(n: number): void } }).hmMock.breakChain(18342));
  await expect(page.getByRole('alert').filter({ hasText: t.chainBanner })).toBeVisible();
  await page.getByRole('button', { name: t.chain }).click();
  await expect(page).toHaveURL(/#\/audit\/18342$/);
});

test('mobile: sections in a scrolling tab bar, no horizontal page scroll', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 760 });
  await page.goto('./#/settings');
  const nav = page.getByRole('navigation', { name: /^(Bereiche|Sections)$/ });
  await expect(nav.getByRole('link')).toHaveCount(5);
  expect(await pageScroll(page)).toBe(0);
  const box = await page.getByRole('button', { name: /^(Not-Aus|Emergency stop)$/ }).boundingBox();
  expect(box?.height).toBeGreaterThanOrEqual(44);
});
