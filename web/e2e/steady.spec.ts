// SPDX-License-Identifier: AGPL-3.0-or-later

// Marking the active entry moves nothing (GitHub issue #47): the settings index and the header
// tabs mark the active entry without bold text and without a border that takes room, so the
// content column next to the index and the neighbouring tabs keep their place to a fraction of
// a pixel, also in forced colours (Windows high contrast).
import type { Locator, Page } from '@playwright/test';
import { expect, test } from './support.ts';

const WIDTH = 1280;
const HEIGHT = 900;
const TOLERANCE = 0.5;
const MODES = ['none', 'active'] as const;

interface Span {
  x: number;
  width: number;
}

/** settled waits until the browser has laid out and painted the last change. */
const settled = (page: Page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));

/** spans returns the inline position of each element relative to origin, and its width. */
function spans(items: Locator, origin: string): Promise<Span[]> {
  return items.evaluateAll((els, sel) => {
    const base = els[0]?.closest(sel)?.getBoundingClientRect().left ?? 0;
    return els.map((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.left - base, width: r.width };
    });
  }, origin);
}

function expectSame(actual: Span[], expected: Span[], what: string) {
  expect(actual.length, what).toBe(expected.length);
  actual.forEach((s, i) => {
    expect(Math.abs(s.x - expected[i]!.x), `${what} #${i}: x`).toBeLessThanOrEqual(TOLERANCE);
    expect(Math.abs(s.width - expected[i]!.width), `${what} #${i}: width`).toBeLessThanOrEqual(TOLERANCE);
  });
}

for (const forcedColors of MODES) {
  const label = forcedColors === 'active' ? ', forced colours' : '';

  test(`marking a settings section keeps the content column in place${label} (issue #47)`, async ({ page }) => {
    await page.setViewportSize({ width: WIDTH, height: HEIGHT });
    await page.emulateMedia({ forcedColors, reducedMotion: 'reduce' });
    await page.goto('./#/settings');
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();

    const index = page.locator('main nav');
    const links = index.getByRole('link');
    const column = page.locator('main .sections');
    const count = await links.count();
    expect(count).toBeGreaterThan(1);

    let first: Span[] | undefined;
    for (let i = 0; i < count; i++) {
      const link = links.nth(i);
      await link.click();
      await expect(link).toHaveAttribute('aria-current', 'location');
      await settled(page);
      const now = await spans(column, 'main');
      if (first) expectSame(now, first, `content column with entry ${i} marked`);
      else first = now;
    }
  });

  test(`switching header tabs keeps the other tabs in place${label} (issue #47)`, async ({ page }) => {
    await page.setViewportSize({ width: WIDTH, height: HEIGHT });
    await page.emulateMedia({ forcedColors, reducedMotion: 'reduce' });
    await page.goto('./#/');
    const tabs = page.locator('header nav a');
    await expect(tabs.first()).toBeVisible();
    const count = await tabs.count();
    expect(count).toBeGreaterThan(1);

    let first: Span[] | undefined;
    for (let i = 0; i < count; i++) {
      const tab = tabs.nth(i);
      await tab.click();
      await expect(tab).toHaveAttribute('aria-current', 'page');
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await settled(page);
      const now = await spans(tabs, 'header nav');
      if (first) expectSame(now, first, `header tabs with tab ${i} active`);
      else first = now;
    }
  });
}
