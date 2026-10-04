// SPDX-License-Identifier: AGPL-3.0-or-later

// The UI against the real gateway (docs/TESTING.md section 3): signed in through Home
// Assistant's login flow at the Ingress stand-in, served from the binary under a random
// Ingress path with the gateway's CSP. Every test fails on CSP violations, console errors
// (a failed API call is one) and page errors (../e2e/support.ts).
import { test as plain, type Page } from '@playwright/test';
import { expect, test } from '../e2e/support.ts';

function env(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is not set; run through e2e/ui_test.go (make e2e-ui)`);
  return value;
}

const base = env('HM_LIVE_URL');
const ingress = env('HM_LIVE_PATH');

async function signIn(page: Page, user: string, password: string): Promise<void> {
  const res = await page.request.post(`${base}/login`, { form: { username: user, password } });
  expect(res.status()).toBe(200);
}

async function open(page: Page, hash = ''): Promise<void> {
  await page.goto(`${base}${ingress}${hash}`);
  await expect(page.locator('main h1').first()).toBeVisible();
}

test('the overview comes from the gateway, with the live event stream', async ({ page }) => {
  await signIn(page, env('HM_LIVE_USER'), env('HM_LIVE_PASSWORD'));
  // Frames are collected from the start: the first one may arrive before any await.
  const urls: string[] = [];
  const sent: string[] = [];
  const received: string[] = [];
  page.on('websocket', (ws) => {
    if (!ws.url().endsWith('/api/events')) return;
    urls.push(ws.url());
    ws.on('framesent', (f) => sent.push(String(f.payload)));
    ws.on('framereceived', (f) => received.push(String(f.payload)));
  });
  await open(page);
  await expect.poll(() => received.some((p) => p.includes('"type":"system"')), { timeout: 15_000 }).toBe(true);
  // The CSRF token goes as the first message, never in the URL.
  expect(sent[0]).toContain('"csrf"');
  expect(urls.every((u) => !u.includes('csrf'))).toBe(true);
});

test('agents and the audit log show what the scenarios did', async ({ page }) => {
  await signIn(page, env('HM_LIVE_USER'), env('HM_LIVE_PASSWORD'));
  await open(page, '#/agents');
  await expect(page.getByText('UI revoke').first()).toBeVisible();
  await open(page, '#/audit');
  await expect(page.locator('main li').first()).toBeVisible();
  await open(page, '#/settings');
  await open(page, '#/mandates');
});

// Negative catalog, UI: someone who is no administrator sees the no-access page and
// nothing of the household. The refused API call is expected here, so the plain test.
plain('someone who is no administrator gets no access', async ({ page }, info) => {
  const violations: string[] = [];
  page.on('console', (msg) => msg.text().includes('Content Security Policy') && violations.push(msg.text()));
  await signIn(page, env('HM_LIVE_PLAIN_USER'), env('HM_LIVE_PLAIN_PASSWORD'));
  await page.goto(`${base}${ingress}`);
  const title = info.project.name === 'de' ? 'Kein Zugriff' : 'No access';
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await expect(page.getByText('UI revoke')).toHaveCount(0);
  expect(violations).toEqual([]);
});
