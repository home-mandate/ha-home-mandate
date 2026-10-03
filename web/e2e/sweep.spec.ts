// SPDX-License-Identifier: AGPL-3.0-or-later

// Screen sweep (docs/TESTING.md section 3, "UI screen sweep"): every screen and state in light and dark,
// left-to-right and right-to-left, at 375 and 1280 px – in de, en and the pseudo-localized
// build. Each variant must show no clipped text and no WCAG 2.2 AA violation (axe); two
// variants are also walked through with the keyboard only.
import type { Page } from '@playwright/test';
import { expect, test } from './support.ts';
import { a11yProblems, applyVariant, focusProblems, label, overflowProblems, VARIANTS, type Variant } from './sweep.ts';

interface Screen {
  name: string;
  path: string;
  /** Options for the mock client (src/lib/api/mock.ts MockOptions), set before the page loads. */
  mock?: Record<string, unknown>;
  /** Runs after the page has loaded, e.g. to open a dialog or change the live state. */
  setup?: (page: Page) => Promise<void>;
}

const CLAUDE = encodeURIComponent('https://claude.ai/oauth/claude-code-client-metadata');

type Mock = { hmMock: { setEmergencyStop(a: boolean): void; breakChain(n: number): void; setHaConnected(c: boolean): void } };

const SCREENS: Screen[] = [
  { name: 'overview', path: './' },
  { name: 'overview, empty household', path: './', mock: { empty: true } },
  {
    name: 'overview, every banner',
    path: './',
    mock: { eventsState: 'closed' },
    setup: (page) =>
      page.evaluate(() => {
        const mock = (window as unknown as Mock).hmMock;
        mock.setEmergencyStop(true);
        mock.breakChain(18342);
        mock.setHaConnected(false);
      }),
  },
  {
    name: 'emergency stop sheet',
    path: './',
    setup: async (page) => {
      await page.locator('header button.estop').click();
      await expect(page.getByRole('alertdialog')).toBeVisible();
    },
  },
  { name: 'mandates', path: './#/mandates' },
  { name: 'mandate editor', path: './#/mandates/mandate-voice' },
  { name: 'mandate editor, long names', path: './#/mandates/mandate-long' },
  { name: 'mandate versions', path: './#/mandates/mandate-voice/versions' },
  { name: 'audit log', path: './#/audit' },
  { name: 'audit entry', path: './#/audit/8' },
  { name: 'requests', path: './#/audit/requests' },
  { name: 'agents', path: './#/agents' },
  { name: 'agent detail', path: `./#/agents/${CLAUDE}` },
  { name: 'agent detail, revoked', path: './#/agents/pair%3Aold-bot' },
  { name: 'agent detail, long name', path: './#/agents/pair%3Along' },
  { name: 'agent detail, bidi name', path: './#/agents/pair%3Abidi' },
  { name: 'pairing', path: './#/agents/pair' },
  { name: 'browser sign-in', path: './#/agents/browser' },
  { name: 'settings', path: './#/settings' },
  { name: 'not found', path: './#/does-not-exist' },
  { name: 'no access', path: './', mock: { failures: { session: 'forbidden' } } },
  { name: 'start-up error', path: './', mock: { failures: { session: 'internal' } } },
];

/** The keyboard walk runs in two opposite variants to keep the sweep fast. */
const KEYBOARD: readonly Variant[] = [
  { theme: 'light', dir: 'ltr', width: 1280 },
  { theme: 'dark', dir: 'rtl', width: 375 },
];
const sameVariant = (a: Variant, b: Variant) => a.theme === b.theme && a.dir === b.dir && a.width === b.width;

async function open(page: Page, screen: Screen): Promise<void> {
  if (screen.mock) {
    await page.addInitScript((options) => {
      (window as unknown as { hmMockOptions: unknown }).hmMockOptions = options;
    }, screen.mock);
  }
  await page.goto(screen.path);
  await expect(page.locator('h1, h2').first()).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  await screen.setup?.(page);
}

for (const screen of SCREENS) {
  test(`sweep: ${screen.name}`, async ({ page }) => {
    test.setTimeout(120_000);
    await open(page, screen);
    const problems: string[] = [];
    for (const variant of VARIANTS) {
      await applyVariant(page, variant);
      const found = [...(await overflowProblems(page)), ...(await a11yProblems(page))];
      if (KEYBOARD.some((k) => sameVariant(k, variant))) found.push(...(await focusProblems(page)));
      problems.push(...found.map((p) => `[${label(variant)}] ${p}`));
    }
    expect(problems).toEqual([]);
  });
}
