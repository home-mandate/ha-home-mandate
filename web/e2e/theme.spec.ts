// SPDX-License-Identifier: AGPL-3.0-or-later

// Colour scheme switch in the header (issue #12), in de, en and the pseudo-localized build:
// switching changes the scheme at once and survives a reload, the stored choice is in place
// before the app renders, blocked storage or an unknown value falls back to the system
// scheme, the compact form fits next to the emergency stop at 375 px, and a forced scheme
// keeps WCAG 2.2 AA on the main screens even when the system scheme is the opposite one.
// support.ts fails every test on a CSP violation or console error.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';
import { a11yProblems, overflowProblems } from './sweep.ts';

const KEY = 'hm-theme';
const LIGHT_BG = 'rgb(245, 246, 248)'; // --hm-color-bg, light
const DARK_BG = 'rgb(17, 20, 25)'; // --hm-color-bg, dark

const toggle = (page: Page) => page.locator('header button.theme');
const attribute = (page: Page) => page.evaluate(() => document.documentElement.getAttribute('data-hm-theme'));
const background = (page: Page) => page.evaluate(() => getComputedStyle(document.documentElement).backgroundColor);
const colorScheme = (page: Page) => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme);
const stored = (page: Page) => page.evaluate((key) => window.localStorage.getItem(key), KEY);

async function settle(page: Page): Promise<void> {
  await expect(page.locator('h1, h2').first()).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
}

/** remember stores a choice before the page's own scripts run. */
async function remember(page: Page, value: string): Promise<void> {
  await page.addInitScript(([key, v]) => window.localStorage.setItem(key!, v!), [KEY, value]);
}

test.describe('colour scheme switch', () => {
  test('cycles System → Light → Dark → System, changes the scheme at once and announces it', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('./');
    await settle(page);
    const button = toggle(page);
    const system = await button.getAttribute('aria-label');
    expect(system).toBeTruthy();
    expect(await attribute(page)).toBeNull();
    expect(await background(page)).toBe(LIGHT_BG);

    await button.click();
    expect(await attribute(page)).toBe('light');
    expect(await stored(page)).toBe('light');
    expect(await colorScheme(page)).toBe('light');
    const light = await button.getAttribute('aria-label');
    expect(light).not.toBe(system);
    // The accessible name contains the visible text (WCAG 2.5.3) at desktop width.
    expect(light).toContain((await button.innerText()).trim());

    await button.click();
    expect(await attribute(page)).toBe('dark');
    expect(await stored(page)).toBe('dark');
    expect(await colorScheme(page)).toBe('dark');
    expect(await background(page)).toBe(DARK_BG);
    const dark = await button.getAttribute('aria-label');
    await expect(page.locator('header [aria-live="polite"]')).toHaveText(dark!);

    await button.click();
    expect(await attribute(page)).toBeNull();
    expect(await stored(page)).toBeNull();
    expect(await background(page)).toBe(LIGHT_BG);
    await expect(button).toHaveAttribute('aria-label', system!);
  });

  test('works with the keyboard and keeps its focus', async ({ page }) => {
    await page.goto('./');
    await settle(page);
    const button = toggle(page);
    await button.focus();
    await page.keyboard.press('Enter');
    expect(await attribute(page)).toBe('light');
    await page.keyboard.press('Space');
    expect(await attribute(page)).toBe('dark');
    await expect(button).toBeFocused();
    const ring = await button.evaluate((el) => getComputedStyle(el).outlineStyle);
    expect(ring).not.toBe('none');
  });

  test('survives a reload and is in place before the app renders', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('./');
    await settle(page);
    await toggle(page).click();
    await toggle(page).click();
    expect(await attribute(page)).toBe('dark');

    // Record the root attribute when the document has been parsed and the module scripts
    // have run their first statements, before the app has loaded any data.
    await page.addInitScript(() => {
      document.addEventListener('DOMContentLoaded', () => {
        (window as unknown as { themeAtStart: string | null }).themeAtStart = document.documentElement.getAttribute('data-hm-theme');
      });
    });
    await page.reload();
    await settle(page);
    expect(await page.evaluate(() => (window as unknown as { themeAtStart: string | null }).themeAtStart)).toBe('dark');
    expect(await attribute(page)).toBe('dark');
    expect(await background(page)).toBe(DARK_BG);
    const label = await toggle(page).getAttribute('aria-label');
    await toggle(page).click();
    await expect(toggle(page)).not.toHaveAttribute('aria-label', label!);
  });

  test('a forced scheme wins over the system scheme', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await remember(page, 'light');
    await page.goto('./');
    await settle(page);
    expect(await attribute(page)).toBe('light');
    expect(await background(page)).toBe(LIGHT_BG);
    expect(await colorScheme(page)).toBe('light');
  });

  for (const value of ['blue', 'system', '']) {
    test(`ignores the unknown stored value ${JSON.stringify(value)} and follows the system`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: 'dark' });
      await remember(page, value);
      await page.goto('./');
      await settle(page);
      expect(await attribute(page)).toBeNull();
      expect(await background(page)).toBe(DARK_BG);
    });
  }

  test('follows the system and still switches when storage is blocked', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(window, 'localStorage', {
        configurable: true,
        get() {
          throw new DOMException('blocked', 'SecurityError');
        },
      });
    });
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.goto('./');
    await settle(page);
    expect(await attribute(page)).toBeNull();
    expect(await background(page)).toBe(DARK_BG);
    await toggle(page).click();
    expect(await attribute(page)).toBe('light');
    expect(await background(page)).toBe(LIGHT_BG);
  });

  for (const [page, code] of [
    ['the sign-in page', 'unauthenticated'],
    ['the no-access page', 'forbidden'],
  ] as const) {
    test(`is offered on ${page} too`, async ({ page: p }) => {
      await p.addInitScript((c) => {
        (window as unknown as { hmMockOptions: unknown }).hmMockOptions = { failures: { session: c } };
      }, code);
      await p.goto('./');
      await settle(p);
      await expect(p.locator('header button.estop')).toHaveCount(0);
      await toggle(p).click();
      expect(await attribute(p)).toBe('light');
    });
  }
});

test.describe('compact form at 375 px', () => {
  for (const estop of [false, true]) {
    test(`fits next to the emergency stop without overflowing the header${estop ? ' (emergency stop on)' : ''}`, async ({ page }) => {
      await page.setViewportSize({ width: 375, height: 800 });
      await page.goto('./');
      await settle(page);
      if (estop) {
        await page.evaluate(async () => {
          await (window as unknown as { hmMock: { setEmergencyStop(a: boolean): Promise<void> } }).hmMock.setEmergencyStop(true);
        });
      }
      for (const dir of ['ltr', 'rtl']) {
        await page.evaluate((d) => {
          document.documentElement.dir = d;
        }, dir);
        const theme = await toggle(page).boundingBox();
        const stop = await page.locator('header button.estop').boundingBox();
        expect(theme && stop, dir).toBeTruthy();
        // Icon only, a full touch target, in one row right next to the emergency stop (the two
        // wrap together when the name and the emergency stop leave no room in the first row).
        expect(theme!.width, dir).toBeGreaterThanOrEqual(44);
        expect(theme!.height, dir).toBeGreaterThanOrEqual(44);
        expect(theme!.width, dir).toBeLessThan(60);
        expect(Math.abs(theme!.y + theme!.height / 2 - (stop!.y + stop!.height / 2)), dir).toBeLessThanOrEqual(1);
        const between = dir === 'ltr' ? stop!.x - (theme!.x + theme!.width) : theme!.x - (stop!.x + stop!.width);
        expect(between, dir).toBeGreaterThanOrEqual(0);
        expect(between, dir).toBeLessThanOrEqual(16);
        for (const box of [theme!, stop!]) {
          expect(box.x, dir).toBeGreaterThanOrEqual(0);
          expect(box.x + box.width, dir).toBeLessThanOrEqual(375);
        }
        // The visible name is hidden, the accessible name stays.
        expect((await toggle(page).innerText()).trim(), dir).toBe('');
        expect(await toggle(page).getAttribute('aria-label'), dir).toBeTruthy();
        expect(await pageScroll(page), dir).toBe(0);
        expect(await overflowProblems(page), dir).toEqual([]);
      }
    });
  }
});

/** Main screens, checked with the scheme forced against the system scheme. */
const SCREENS = [
  { name: 'overview', path: './' },
  { name: 'mandate editor', path: './#/mandates/mandate-voice' },
  { name: 'audit entry', path: './#/audit/8' },
  { name: 'agents', path: './#/agents' },
  { name: 'settings', path: './#/settings' },
] as const;

const FORCED = [
  { theme: 'dark', system: 'light' },
  { theme: 'light', system: 'dark' },
] as const;

for (const { theme, system } of FORCED) {
  for (const screen of SCREENS) {
    test(`forced ${theme} under a ${system} system: ${screen.name} keeps WCAG 2.2 AA and clips nothing`, async ({ page }) => {
      test.setTimeout(60_000);
      await page.emulateMedia({ colorScheme: system, reducedMotion: 'reduce' });
      await remember(page, theme);
      await page.goto(screen.path);
      await settle(page);
      expect(await attribute(page)).toBe(theme);
      const problems: string[] = [];
      for (const width of [375, 1280]) {
        await page.setViewportSize({ width, height: 900 });
        await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
        const found = [...(await overflowProblems(page)), ...(await a11yProblems(page))];
        problems.push(...found.map((p) => `[${width}] ${p}`));
      }
      expect(problems).toEqual([]);
    });
  }
}
