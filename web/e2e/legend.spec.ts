// SPDX-License-Identifier: AGPL-3.0-or-later

// The legend of the preview (issue #41): a definition list with an icon, a term and one
// sentence per mark, no tooltips; in the grid on wide screens and in the cards on a phone,
// in de, en and the pseudo-localized build, without clipped text and without axe findings.
// No visible texts in the locators: the pseudo build translates them.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';
import { a11yProblems, overflowProblems } from './sweep.ts';

const HEIGHT = 900;
const ENTRIES = 3;

async function openPreview(page: Page, width: number) {
  await page.setViewportSize({ width, height: HEIGHT });
  await page.goto('./#/mandates/mandate-voice');
  await expect(page.locator('h1')).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  // On a phone the preview is behind the second tab.
  const tabs = page.locator('main [role="tablist"] [role="tab"]');
  if (await tabs.count()) await tabs.nth(1).click();
}

for (const width of [1280, 375] as const) {
  test(`at ${width} px the legend explains each mark in a sentence (issue #41)`, async ({ page }) => {
    await openPreview(page, width);
    const legend = page.locator('main .legend');
    await legend.scrollIntoViewIfNeeded();
    await expect(legend).toBeVisible();
    // Grid with cells on a wide screen, cards on a phone: the legend is there in both.
    await expect(page.locator('main [role="grid"]')).toHaveCount(width === 1280 ? 2 : 0);

    const terms = legend.locator('dl dt');
    const sentences = legend.locator('dl dd');
    await expect(terms).toHaveCount(ENTRIES);
    await expect(sentences).toHaveCount(ENTRIES);
    for (let i = 0; i < ENTRIES; i++) {
      await expect(terms.nth(i)).not.toBeEmpty();
      await expect(sentences.nth(i)).not.toBeEmpty();
    }
    // The cell of a critical action shows its decision icon and the shield; both are in the legend.
    await expect(terms.first().locator('svg[aria-hidden="true"]')).toHaveCount(2);
    await expect(legend.locator('[title]')).toHaveCount(0);

    expect(await pageScroll(page)).toBe(0);
    expect(await overflowProblems(page)).toEqual([]);
    expect(await a11yProblems(page)).toEqual([]);
  });
}
