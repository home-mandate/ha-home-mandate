// SPDX-License-Identifier: AGPL-3.0-or-later

// Hostile agent text (docs/TESTING.md section 4, "Agent text"): the mock option "hostile"
// gives the voice assistant WORST_NAME (bidi override, markup, line breaks, blank letters,
// stacked marks, one long word, more than 500 characters) everywhere, plus a request with
// WORST_REASON and a pairing candidate with that name. Every screen that shows agent text
// must show it cleaned, as text, isolated and without breaking the layout.
import type { Page } from '@playwright/test';
import { expect, test } from './support.ts';
import { applyVariant, overflowProblems, type Variant } from './sweep.ts';

/** Parts of WORST_NAME and WORST_REASON that survive cleaning; they find the rendered text. */
const MARKERS = ['tnetsissa', 'ÜberlängeÜberlänge', 'esrever', 'Dringend Dringend'];

// Characters that must never reach the page from agent text, written as code points so the
// source holds no invisible characters: bidi embeddings and overrides (LRE…RLO), LRI and RLI
// (the UI isolates with FSI/PDI itself), LRM, RLM, ALM, zero-width space, Hangul filler,
// braille blank, tab, line breaks.
const FORBIDDEN = [0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x200e, 0x200f, 0x061c, 0x200b, 0x3164, 0x2800, 0x09, 0x0a, 0x0d];

const WIDTHS: readonly Variant[] = [
  { theme: 'light', dir: 'ltr', width: 375 },
  { theme: 'light', dir: 'rtl', width: 1280 },
];

interface Screen {
  name: string;
  path: string;
  setup?: (page: Page) => Promise<void>;
}

const VOICE = encodeURIComponent('pair:voice-assistant');

const SCREENS: Screen[] = [
  { name: 'overview', path: './' },
  { name: 'requests', path: './#/audit/requests' },
  {
    name: 'requests, approval confirmation',
    path: './#/audit/requests',
    setup: async (page) => {
      // Second button of the hostile card: approve (decline comes first); it asks to confirm.
      await page.getByRole('article').filter({ hasText: 'tnetsissa' }).getByRole('button').nth(1).click();
    },
  },
  { name: 'audit log', path: './#/audit' },
  { name: 'audit entry', path: './#/audit/8' },
  { name: 'agents', path: './#/agents' },
  { name: 'agent detail', path: `./#/agents/id/${VOICE}` },
  {
    name: 'revoke dialog',
    path: `./#/agents/id/${VOICE}`,
    setup: async (page) => {
      await page.locator('main button.btn.danger').first().click();
      await expect(page.getByRole('alertdialog')).toBeVisible();
    },
  },
  { name: 'mandates', path: './#/mandates' },
  { name: 'mandate editor', path: './#/mandates/mandate-voice' },
  {
    name: 'pairing candidate',
    path: './#/agents/pair',
    setup: async (page) => {
      await page.locator('main input').first().fill('BCDF-GHJK');
      await page.keyboard.press('Enter');
    },
  },
];

/** agentTexts returns every text node and attribute value on the page that holds agent text. */
function agentTexts(page: Page): Promise<string[]> {
  return page.evaluate((markers) => {
    const found: string[] = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      if (markers.some((m) => n.textContent?.includes(m))) found.push(n.textContent ?? '');
    }
    for (const el of document.body.querySelectorAll('*')) {
      for (const attr of el.attributes) if (markers.some((m) => attr.value.includes(m))) found.push(attr.value);
    }
    return found;
  }, MARKERS);
}

for (const screen of SCREENS) {
  test(`hostile agent text: ${screen.name}`, async ({ page }) => {
    await page.addInitScript(() => {
      (window as unknown as { hmMockOptions: unknown }).hmMockOptions = { hostile: true };
    });
    await page.goto(screen.path);
    await expect(page.locator('h1, h2').first()).toBeVisible();
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
    await screen.setup?.(page);
    await expect(page.getByText(/tnetsissa/).filter({ visible: true }).first()).toBeVisible();

    const texts = await agentTexts(page);
    expect(texts.length, 'no agent text found on the page').toBeGreaterThan(0);
    const forbidden = new RegExp(`[${FORBIDDEN.map((c) => String.fromCodePoint(c)).join('')}]`, 'u');
    for (const text of texts) {
      expect(forbidden.test(text), `hidden or breaking characters in ${JSON.stringify(text.slice(0, 60))}`).toBe(false);
      expect([...text].length, 'agent text longer than the limit').toBeLessThanOrEqual(520);
      expect(/\p{M}{3}/u.test(text), 'more than two stacked marks').toBe(false);
    }
    // Markup stays text: no element, handler or javascript: link comes from agent text.
    await expect(page.locator('img[src="x"], main b, a[href^="javascript"]')).toHaveCount(0);

    const problems: string[] = [];
    for (const v of WIDTHS) {
      await applyVariant(page, v);
      problems.push(...(await overflowProblems(page)).map((p) => `[${v.dir}/${v.width}] ${p}`));
    }
    expect(problems).toEqual([]);
  });
}
