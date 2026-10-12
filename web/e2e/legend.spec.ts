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
const TOLERANCE = 0.5;

interface Row {
  centres: number[];
  column: { x: number; width: number };
}

/** rows returns, per legend row, the vertical centre of each icon and the box of the icon column. */
function rows(page: Page): Promise<Row[]> {
  return page.locator('main .legend .icons').evaluateAll((columns) =>
    columns.map((column) => {
      const box = column.getBoundingClientRect();
      const centres = [...column.querySelectorAll('svg, .dot')].map((icon) => {
        const r = icon.getBoundingClientRect();
        return r.top + r.height / 2;
      });
      return { centres, column: { x: box.left, width: box.width } };
    }),
  );
}

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

    // The icons of a row sit on one centre line, and the icon column has the same place and
    // width in every row, so all terms start at the same x.
    const found = await rows(page);
    expect(found.length).toBe(ENTRIES + 1);
    found.forEach((row, i) => {
      const spread = Math.max(...row.centres) - Math.min(...row.centres);
      expect(spread, `legend row ${i}: vertical centres of the icons`).toBeLessThanOrEqual(TOLERANCE);
      expect(Math.abs(row.column.x - found[0]!.column.x), `legend row ${i}: icon column x`).toBeLessThanOrEqual(TOLERANCE);
      expect(Math.abs(row.column.width - found[0]!.column.width), `legend row ${i}: icon column width`).toBeLessThanOrEqual(TOLERANCE);
    });

    expect(await pageScroll(page)).toBe(0);
    expect(await overflowProblems(page)).toEqual([]);
    expect(await a11yProblems(page)).toEqual([]);
  });
}
