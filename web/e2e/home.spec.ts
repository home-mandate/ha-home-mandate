// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test, type Page } from '@playwright/test';

const text = {
  de: { heading: 'Mandate für deine KI-Agenten', release: '31. Oktober 2026', notFound: 'Seite nicht gefunden', back: 'Zurück zur Übersicht' },
  en: { heading: 'Mandates for your AI agents', release: 'October 31, 2026', notFound: 'Page not found', back: 'Back to the overview' },
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

  await page.goto('./');

  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.heading);
  await expect(page.locator('html')).toHaveAttribute('lang', testInfo.project.name);
  await expect(page.getByText(t.release)).toBeVisible();
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
