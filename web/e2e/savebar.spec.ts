// SPDX-License-Identifier: AGPL-3.0-or-later

// The save bar of the editors (issue #20) on scrolled pages, in de, en and the
// pseudo-localized build at 1280×800, 1440×900 and 375×812: when it appears, the page does
// not jump and the focus stays; it sticks to the bottom of the viewport with nothing below
// it; it never covers the focused element or any stop of the keyboard; the end of the page
// stays reachable above it; and when it goes after saving, the page does not jump either.
// No visible texts in the locators: the pseudo build translates them.
import type { Locator, Page } from '@playwright/test';
import { expect, test } from './support.ts';
import { a11yProblems, applyVariant, focusProblems, overflowProblems } from './sweep.ts';

const SIZES = [
  { width: 1280, height: 800 },
  { width: 1440, height: 900 },
  { width: 375, height: 812 },
] as const;

const EDITORS = [
  { name: 'mandate', path: './#/mandates/mandate-voice' },
  { name: 'template', path: './#/templates/voice-assistant' },
] as const;

const bar = (page: Page) => page.locator('section.savebar');
const scrollY = (page: Page) => page.evaluate(() => window.scrollY);

async function open(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator('h1')).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
}

/** uncovered says whether the element is fully inside the viewport and its centre is not covered by anything else. */
function uncovered(target: Locator): Promise<boolean> {
  return target.evaluate((el) => {
    const r = el.getBoundingClientRect();
    if (r.top < 0 || r.bottom > window.innerHeight + 0.5) return false;
    const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return !!top && (top === el || el.contains(top));
  });
}

/** sticksToBottom checks that the bar's bottom is the viewport's bottom: nothing below it. */
async function sticksToBottom(page: Page): Promise<void> {
  const box = await bar(page).boundingBox();
  const height = await page.evaluate(() => window.innerHeight);
  expect(box).not.toBeNull();
  expect(Math.abs(box!.y + box!.height - height)).toBeLessThanOrEqual(1);
}

for (const size of SIZES) {
  for (const editor of EDITORS) {
    test(`${editor.name} at ${size.width}×${size.height}: the save bar appears and goes without moving the page`, async ({ page }) => {
      test.setTimeout(60_000);
      await page.setViewportSize(size);
      await open(page, editor.path);
      const scrollable = await page.evaluate(() => document.documentElement.scrollHeight - window.innerHeight);
      expect(scrollable).toBeGreaterThan(200);

      // Rule 3, scrolled to the middle of the page or further, opened; then a change.
      const opener = page.locator('main ol > li .head .btn.text').nth(2);
      await page.evaluate((y) => window.scrollTo(0, y), Math.round(scrollable / 2));
      await opener.click();
      const radio = page.locator('main li.editing [role="radiogroup"]').first().getByRole('radio', { checked: false }).first();
      await radio.scrollIntoViewIfNeeded();
      // The locator would move on to the next unchecked choice once this one is checked.
      const choice = (await radio.elementHandle())!;
      const head = page.locator('main li.editing .head');
      const before = { y: await scrollY(page), at: (await choice.boundingBox())!.y, head: (await head.boundingBox())!.height };
      await choice.click();
      await expect(bar(page)).toBeVisible();
      // (b) The bar's appearance moves neither the page nor the focus. Only the rule's own
      // sentence above the form grows with the new decision: the choice moves down in the page
      // by exactly that, and the window either stays (scrollY unchanged) or the browser's
      // scroll anchoring makes up for exactly that growth. Nothing is owed to the bar.
      const grown = (await head.boundingBox())!.height - before.head;
      const after = { y: await scrollY(page), at: (await choice.boundingBox())!.y };
      expect(Math.abs(after.y + after.at - (before.y + before.at) - grown)).toBeLessThanOrEqual(1);
      const moved = after.y - before.y;
      expect(Math.abs(moved) <= 1 || Math.abs(moved - grown) <= 1).toBe(true);
      expect(await choice.evaluate((el) => el === document.activeElement)).toBe(true);
      // (a), (c) It sticks to the bottom of the viewport, with nothing below it.
      await sticksToBottom(page);

      await page.locator('main li.editing .btn.primary').click();
      await expect(page.locator('main li p.unsaved')).toBeVisible();
      // (d) The focused Edit button is fully visible, not under the bar.
      const focused = page.locator(':focus');
      await expect(focused).toHaveCount(1);
      expect(await uncovered(focused)).toBe(true);
      await sticksToBottom(page);

      // (f) At the very end of the page, the last rule lies fully above the bar.
      await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
      await sticksToBottom(page);
      const barTop = (await bar(page).boundingBox())!.y;
      const lastRule = await page.locator('main ol > li').last().boundingBox();
      expect(lastRule!.y + lastRule!.height).toBeLessThanOrEqual(barTop);
      const add = await page.locator('main ol + span + .add').boundingBox();
      expect(add!.y + add!.height).toBeLessThanOrEqual(barTop);

      // (e) The keyboard never lands on an element the bar covers.
      expect(await focusProblems(page)).toEqual([]);

      // Saving: the bar goes, and the page does not jump. Only what saving ends goes with it:
      // the room the bar took and the rule's unsaved hint. A page scrolled near its end may
      // move up by at most that much, never more (the longer labels of issue #37 made the
      // hint taller and showed that the hint was missing here).
      const hintBox = await page.locator('main li p.unsaved').boundingBox();
      const room = (await bar(page).boundingBox())!.height + (hintBox?.height ?? 0);
      const saving = await scrollY(page);
      await bar(page).locator('.btn.primary').click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      await dialog.locator('.btn.primary').click();
      await expect(bar(page)).toHaveCount(0);
      const saved = await scrollY(page);
      expect(saved).toBeLessThanOrEqual(saving + 1);
      expect(saving - saved).toBeLessThanOrEqual(room + 1);
    });
  }
}

// Issue #37: the save buttons name the review step ("Review and save", "Review and save now"),
// which makes them longer. At 320 px, in de, en and the pseudo build, in both directions, the
// editor header, the rule's unsaved hint and the save bar keep all of their text inside the
// viewport, nothing is clipped, the page does not scroll sideways, and axe finds nothing.
const NARROW = { width: 320, height: 700 } as const;
const NARROW_EDITORS = [
  ...EDITORS,
  // A base template cannot be saved in place: its bar offers the longest label, "save as new".
  { name: 'base template', path: './#/templates/hm-voice-cautious' },
] as const;

for (const editor of NARROW_EDITORS) {
  test(`${editor.name} at 320 px: the longer save labels fit and axe stays clean (issue #37)`, async ({ page }) => {
    test.setTimeout(60_000);
    await page.setViewportSize(NARROW);
    await open(page, editor.path);
    // Rule 1 to the other decision, then "Done": the hint with its save button and the bar appear.
    await page.locator('main ol > li .head .btn.text').first().click();
    await page.locator('main li.editing [role="radiogroup"]').first().getByRole('radio', { checked: false }).first().click();
    await page.locator('main li.editing .btn.primary').click();
    await expect(bar(page)).toBeVisible();
    await expect(page.locator('main li p.unsaved')).toBeVisible();

    for (const dir of ['ltr', 'rtl'] as const) {
      await applyVariant(page, { theme: 'light', dir, width: 375 });
      await page.setViewportSize(NARROW);
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
      const buttons = page.locator('main .head .actions .btn, main li p.unsaved .btn, section.savebar .btn');
      expect(await buttons.count()).toBeGreaterThanOrEqual(3);
      for (const target of await buttons.all()) {
        await target.scrollIntoViewIfNeeded();
        const box = (await target.boundingBox())!;
        expect(box.x, `${dir}: ${await target.innerText()}`).toBeGreaterThanOrEqual(-1);
        expect(box.x + box.width, `${dir}: ${await target.innerText()}`).toBeLessThanOrEqual(NARROW.width + 1);
      }
      expect([...(await overflowProblems(page)), ...(await a11yProblems(page))].map((p) => `[${dir}] ${p}`)).toEqual([]);
    }
  });
}
