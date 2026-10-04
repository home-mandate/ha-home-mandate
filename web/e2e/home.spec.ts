// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test, type Page } from '@playwright/test';
import { CSP } from '../scripts/serve-ingress.ts';

const text = {
  de: { heading: 'Übersicht', notFound: 'Seite nicht gefunden', back: 'Zur Übersicht' },
  en: { heading: 'Overview', notFound: 'Page not found', back: 'Go to overview' },
} as const;

/** collectViolations fails the test on CSP violations and console errors. */
function collectViolations(page: Page): string[] {
  const problems: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') problems.push(msg.text());
  });
  page.on('pageerror', (err) => problems.push(err.message));
  return problems;
}

test('overview under a random Ingress path', async ({ page }, testInfo) => {
  const t = text[testInfo.project.name as keyof typeof text];
  const problems = collectViolations(page);
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`));
  });

  const response = await page.goto('./');

  expect(response?.headers()['content-security-policy']).toBe(CSP);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.heading);
  await expect(page.locator('html')).toHaveAttribute('lang', testInfo.project.name);
  expect(problems).toEqual([]);
});

test('hash routing keeps the Ingress path', async ({ page }, testInfo) => {
  const t = text[testInfo.project.name as keyof typeof text];
  await page.goto('./#/does-not-exist');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.notFound);
  const path = new URL(page.url()).pathname;

  await page.getByRole('link', { name: t.back }).click();

  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.heading);
  expect(new URL(page.url()).pathname).toBe(path);
});

test('nothing is served outside the Ingress path', async ({ request }) => {
  const response = await request.get('/index.html');
  expect(response.status()).toBe(404);
});

test('the CSP blocks injected inline scripts', async ({ page }) => {
  await page.goto('./');
  const violation = page.evaluate(
    () =>
      new Promise<string>((resolve) => {
        document.addEventListener('securitypolicyviolation', (e) => resolve(e.violatedDirective));
        const script = document.createElement('script');
        script.textContent = 'window.injected = true';
        document.body.append(script);
      }),
  );
  expect(await violation).toMatch(/^script-src/);
  expect(await page.evaluate(() => 'injected' in window)).toBe(false);
});
