// SPDX-License-Identifier: AGPL-3.0-or-later

// Layout of the frame on desktop widths (GitHub issues #7 and #8): the page content and the
// header's content are centred and follow the window's width; the settings index is as
// wide as its entries and shows a scroll bar only when the window is too short for it,
// also once the page has scrolled and the header is gone.
import type { Page } from '@playwright/test';
import { expect, test } from './support.ts';

const WIDTHS = [1024, 1280, 1920] as const;
const HEIGHT = 900;
/** Too short for the eight entries of the settings index. */
const SHORT = 400;
const TOLERANCE = 1;

interface Box {
  left: number;
  right: number;
}

/** contentBox is an element's box without its inline padding. */
async function contentBox(page: Page, selector: string): Promise<Box> {
  return page.locator(selector).first().evaluate((el) => {
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return { left: r.left + parseFloat(s.paddingLeft), right: r.right - parseFloat(s.paddingRight) };
  });
}

const viewportWidth = (page: Page) => page.evaluate(() => document.documentElement.clientWidth);

interface NavState {
  scrollHeight: number;
  clientHeight: number;
  scrollWidth: number;
  clientWidth: number;
  top: number;
  bottom: number;
}

const settingsIndex = (page: Page) => page.locator('main nav');

function navState(page: Page): Promise<NavState> {
  return settingsIndex(page).evaluate((el) => {
    const r = el.getBoundingClientRect();
    return { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, top: r.top, bottom: r.bottom };
  });
}

async function scrollPage(page: Page, y: number) {
  await page.evaluate((to) => window.scrollTo(0, to), y);
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
}

for (const width of WIDTHS) {
  test(`${width} px: page content and header content are centred and use the same width (issue #7)`, async ({ page }) => {
    await page.setViewportSize({ width, height: HEIGHT });
    await page.goto('./#/mandates');
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    const vw = await viewportWidth(page);

    const main = await page.locator('main').evaluate((el) => {
      const r = el.getBoundingClientRect();
      return { left: r.left, right: r.right };
    });
    expect(Math.abs(main.left - (vw - main.right)), 'main is centred').toBeLessThanOrEqual(TOLERANCE);

    const content = await contentBox(page, 'main');
    const header = await contentBox(page, 'header');
    expect(Math.abs(header.left - (vw - header.right)), 'header content is centred').toBeLessThanOrEqual(TOLERANCE);
    expect(Math.abs(header.left - content.left), 'header content starts with the page content').toBeLessThanOrEqual(TOLERANCE);
    expect(Math.abs(header.right - content.right), 'header content ends with the page content').toBeLessThanOrEqual(TOLERANCE);
    // The header's background still spans the window.
    const bar = await page.locator('header').evaluate((el) => el.getBoundingClientRect().width);
    expect(Math.abs(bar - vw)).toBeLessThanOrEqual(TOLERANCE);
  });

  test(`${width} px: the settings index fits its entries and has no scroll bar, before and after scrolling (issue #8)`, async ({ page }) => {
    await page.setViewportSize({ width, height: HEIGHT });
    await page.goto('./#/settings');
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    const before = await navState(page);
    expect(before.scrollHeight, 'no vertical scroll bar').toBeLessThanOrEqual(before.clientHeight);
    expect(before.scrollWidth, 'entries are not cut off').toBeLessThanOrEqual(before.clientWidth);

    // Far down the page the header is gone; the index stays in view without a scroll bar.
    await scrollPage(page, 100_000);
    const after = await navState(page);
    expect(after.scrollHeight, 'no vertical scroll bar after scrolling').toBeLessThanOrEqual(after.clientHeight);
    expect(after.top).toBeGreaterThanOrEqual(0);
    expect(after.bottom).toBeLessThanOrEqual(HEIGHT);

    // The column is as wide as the longest entry needs, not a fixed width.
    const { width: column, needed } = await settingsIndex(page)
      .getByRole('link')
      .evaluateAll((links) => {
        const intrinsic = links.map((l) => {
          const range = document.createRange();
          range.selectNodeContents(l);
          const s = getComputedStyle(l);
          return range.getBoundingClientRect().width + parseFloat(s.paddingLeft) + parseFloat(s.paddingRight);
        });
        return { width: Math.max(...links.map((l) => l.getBoundingClientRect().width)), needed: Math.max(...intrinsic) };
      });
    expect(Math.abs(column - needed), 'index column fits its longest entry').toBeLessThanOrEqual(2);
  });
}

test('a short window: the settings index scrolls inside and stays within the window (issue #8)', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: SHORT });
  await page.goto('./#/settings');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  // Past the header, but not at the end, where the index goes up with the end of its column.
  await scrollPage(page, 600);
  const nav = await navState(page);
  expect(nav.scrollHeight, 'the entries need more room than the window has').toBeGreaterThan(nav.clientHeight);
  expect(nav.top).toBeGreaterThanOrEqual(0);
  expect(nav.bottom).toBeLessThanOrEqual(SHORT);
});
