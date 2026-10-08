// SPDX-License-Identifier: AGPL-3.0-or-later

// Unsaved changes in the mandate and the template editor (issue #20), in de, en and the
// pseudo-localized build: after "Done" a changed rule says that it is not saved yet (and the
// live region says it too), a save bar with Save and Discard stays at the bottom of the
// viewport without hiding the end of the page, an edit left earlier comes back with a
// warning, saving ends all of it with a new version, and reloading with unsaved changes asks
// first. No visible texts in the locators: the pseudo build translates them.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';

const MANDATE = './#/mandates/mandate-voice';
const TEMPLATE = './#/templates/voice-assistant';

const bar = (page: Page) => page.locator('section.savebar');
const hint = (page: Page) => page.locator('main li p.unsaved');
const restored = (page: Page) => page.locator('main .banner.warning');
const chip = (page: Page) => page.locator('main button.chip');

async function open(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator('h1')).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
}

/** changeFirstRule opens rule 1, picks the second decision ("ask") and closes it with "Done". */
async function changeFirstRule(page: Page): Promise<void> {
  await page.locator('main ol > li .head .btn.text').first().click();
  await page.locator('main li.editing [role="radiogroup"]').first().getByRole('radio').nth(1).click();
  await page.locator('main li.editing .btn.primary').click();
}

/** The number in the version chip, before its digest. */
async function versionShown(page: Page): Promise<string> {
  const text = (await chip(page).innerText()).split('·')[0] ?? '';
  return text.replace(/\D/g, '');
}

for (const width of [1280, 375] as const) {
  test(`mandate at ${width} px: hint after Done, save bar at the bottom, saving ends both with a new version`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    await open(page, MANDATE);
    expect(await versionShown(page)).toBe('1');
    await expect(bar(page)).toHaveCount(0);

    await changeFirstRule(page);
    await expect(hint(page)).toBeVisible();
    // Announced in the rules' live region, with the same words as shown.
    const said = (await hint(page).locator('> span').innerText()).trim();
    await expect(page.locator('main [aria-live="polite"]').filter({ hasText: said })).toHaveCount(1);

    await expect(bar(page)).toBeVisible();
    const box = await bar(page).boundingBox();
    expect(box).not.toBeNull();
    // At the bottom of the viewport, below the frame the page scrolls in: it covers nothing.
    expect(Math.round(box!.y + box!.height)).toBe(800);
    const frame = await page.evaluate(() => {
      const el = document.getElementById('hm-scroll')!;
      return { bottom: el.getBoundingClientRect().bottom, sideways: el.scrollWidth - el.clientWidth, scrolls: el.scrollHeight > el.clientHeight };
    });
    expect(frame.bottom).toBeLessThanOrEqual(box!.y + 1);
    expect(frame.sideways).toBe(0);
    expect(frame.scrolls).toBe(true);
    expect(await pageScroll(page)).toBe(0);

    // The end of the page can be reached and lies above the bar.
    await page.locator('main ol + span + .add .btn').first().scrollIntoViewIfNeeded();
    const add = await page.locator('main ol + span + .add .btn').first().boundingBox();
    expect(add!.y + add!.height).toBeLessThanOrEqual(box!.y);

    await bar(page).locator('.btn.primary').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.locator('.btn.primary').click();
    await expect(dialog).toBeHidden();
    await expect(bar(page)).toHaveCount(0);
    await expect(hint(page)).toHaveCount(0);
    expect(await versionShown(page)).toBe('2');
  });
}

test('mandate: an edit left earlier comes back with a warning; discarding ends it', async ({ page }) => {
  await open(page, MANDATE);
  await expect(restored(page)).toHaveCount(0);
  await changeFirstRule(page);
  await page.locator('main a[href="#/mandates"]').first().click();
  await expect(page.locator('main a[href="#/mandates/mandate-voice"]').first()).toBeVisible();
  await page.locator('main a[href="#/mandates/mandate-voice"]').first().click();

  await expect(restored(page)).toBeVisible();
  await expect(restored(page)).toHaveAttribute('role', 'alert');
  await expect(bar(page)).toBeVisible();
  await bar(page).locator('.btn:not(.primary)').click();
  await expect(bar(page)).toHaveCount(0);
  await expect(restored(page)).toHaveCount(0);
  await expect(page.locator('main h1')).toBeFocused();
});

test('reloading with unsaved changes asks first; after saving it does not', async ({ page }) => {
  const dialogs: string[] = [];
  page.on('dialog', (dialog) => {
    dialogs.push(dialog.type());
    void dialog.accept();
  });
  await open(page, MANDATE);
  await changeFirstRule(page);
  await expect(bar(page)).toBeVisible();
  await page.reload();
  expect(dialogs).toEqual(['beforeunload']);
  // Accepted: the reload discarded the edit.
  await expect(page.locator('h1')).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  await expect(bar(page)).toHaveCount(0);

  await changeFirstRule(page);
  await bar(page).locator('.btn.primary').click();
  await page.getByRole('dialog').locator('.btn.primary').click();
  await expect(bar(page)).toHaveCount(0);
  await page.reload();
  await expect(page.locator('h1')).toBeVisible();
  expect(dialogs).toEqual(['beforeunload']);
});

test('template: save bar, an edit left earlier with a warning, reload asks first', async ({ page }) => {
  const dialogs: string[] = [];
  page.on('dialog', (dialog) => {
    dialogs.push(dialog.type());
    void dialog.accept();
  });
  await open(page, TEMPLATE);
  await expect(bar(page)).toHaveCount(0);
  await page.locator('main .rate input[type="number"]').fill('20');
  await expect(bar(page)).toBeVisible();

  await page.locator('main a[href="#/templates"]').first().click();
  await page.locator('main a[href="#/templates/voice-assistant"]').first().click();
  await expect(restored(page)).toBeVisible();
  await expect(page.locator('main .rate input[type="number"]')).toHaveValue('20');

  await page.reload();
  expect(dialogs).toEqual(['beforeunload']);
  await expect(page.locator('h1')).toBeVisible();
  await expect(bar(page)).toHaveCount(0);
});
