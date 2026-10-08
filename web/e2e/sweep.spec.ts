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

type Mock = { hmMock: { setEmergencyStop(a: boolean): Promise<void>; breakChain(n: number): void; setHaConnected(c: boolean): void } };

const SCREENS: Screen[] = [
  { name: 'overview', path: './' },
  { name: 'overview, empty household', path: './', mock: { empty: true } },
  {
    name: 'overview, every banner',
    path: './',
    mock: { eventsState: 'closed' },
    setup: async (page) => {
      await page.evaluate(async () => {
        const mock = (window as unknown as Mock).hmMock;
        await mock.setEmergencyStop(true);
        mock.breakChain(18342);
        mock.setHaConnected(false);
      });
      // Measure only once all four banners are there: emergency stop, broken chain, Home
      // Assistant and the lost connection (shown after 5 s).
      await expect(page.locator('.stack .banner')).toHaveCount(4, { timeout: 10_000 });
    },
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
  { name: 'templates', path: './#/templates' },
  { name: 'template editor, base template', path: './#/templates/hm-voice-cautious' },
  { name: 'template editor, own template', path: './#/templates/voice-assistant' },
  { name: 'template editor, new template', path: './#/templates/_new' },
  {
    name: 'template saved, take the change over into mandates',
    path: './#/templates/voice-assistant',
    setup: async (page) => {
      // No texts here: the pseudo build translates them. The rate limit, Save, then the summary's confirmation.
      await page.locator('main .rate input[type="number"]').fill('20');
      await page.locator('main button.btn.primary').first().click();
      await page.getByRole('dialog').locator('button.btn.primary').click();
      await expect(page.getByRole('dialog').getByRole('checkbox').first()).toBeVisible();
    },
  },
  { name: 'audit log', path: './#/audit' },
  { name: 'audit entry', path: './#/audit/8' },
  { name: 'requests', path: './#/audit/requests' },
  { name: 'agents', path: './#/agents' },
  { name: 'agent detail', path: `./#/agents/id/${CLAUDE}` },
  { name: 'agent detail, revoked', path: './#/agents/id/pair%3Aold-bot' },
  { name: 'agent detail, long name', path: './#/agents/id/pair%3Along' },
  { name: 'agent detail, bidi name', path: './#/agents/id/pair%3Abidi' },
  { name: 'pairing', path: './#/agents/pair' },
  {
    name: 'pairing, choosing the template',
    path: './#/agents/pair',
    setup: async (page) => {
      await page.getByRole('textbox').first().fill('bcdf ghjk');
      await page.keyboard.press('Enter');
      await expect(page.getByRole('heading', { level: 2 })).toBeFocused();
      await page.keyboard.press('Tab'); // "This isn't my agent"
      await page.keyboard.press('Tab'); // Continue
      await page.keyboard.press('Enter');
      await expect(page.getByRole('radio').first()).toBeVisible();
    },
  },
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
