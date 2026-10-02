// SPDX-License-Identifier: AGPL-3.0-or-later

// Shared by the specs: every test fails on CSP violations, console errors and uncaught
// page errors (docs/TESTING.md section 4).
import { expect, test as base, type Page } from '@playwright/test';

/** watch collects CSP violations and console errors. */
async function watch(page: Page): Promise<string[]> {
  const problems: string[] = [];
  page.on('console', (msg) => msg.type() === 'error' && problems.push(msg.text()));
  page.on('pageerror', (err) => problems.push(err.message));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => console.error(`CSP violation: ${e.violatedDirective}`));
  });
  return problems;
}

export const test = base.extend<{ problems: string[] }>({
  problems: [
    async ({ page }, use) => {
      const problems = await watch(page);
      await use(problems);
      expect(problems).toEqual([]);
    },
    { auto: true },
  ],
});

/** pageScroll returns how far the page can scroll sideways; 0 means no horizontal scrolling. */
export function pageScroll(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
}

export { expect };
