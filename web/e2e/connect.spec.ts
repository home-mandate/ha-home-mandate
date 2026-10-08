// SPDX-License-Identifier: AGPL-3.0-or-later

// Connecting an agent against the mock build (issue #15): every entry point leads to the
// agents page with the choice of both ways open and the focus on its heading, whether or not
// agents are admitted already; each way leads to its flow.
import type { Page } from '@playwright/test';
import { expect, test } from './support.ts';

const text = {
  de: {
    welcome: 'Willkommen bei Home-Mandate',
    connect: 'Agent verbinden',
    how: 'Wie meldet sich dein Agent an?',
    code: /Mit Kopplungscode/,
    browser: /Mit Browser-Anmeldung/,
    field: 'Kopplungscode',
    browserTitle: 'Agent per Browser-Anmeldung verbinden',
    agents: 'Agenten',
    add: 'Agent hinzufügen',
    newMandate: 'Neues Mandat',
    toAgents: 'Zu den Agenten',
  },
  en: {
    welcome: 'Welcome to Home-Mandate',
    connect: 'Connect agent',
    how: 'How does your agent sign in?',
    code: /With a pairing code/,
    browser: /With browser sign-in/,
    field: 'Pairing code',
    browserTitle: 'Connect an agent with browser sign-in',
    agents: 'Agents',
    add: 'Add agent',
    newMandate: 'New mandate',
    toAgents: 'Go to agents',
  },
} as const;

type Lang = keyof typeof text;
type Text = (typeof text)[Lang];

/** An empty household: the overview welcomes with the first steps. */
async function emptyHousehold(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as unknown as { hmMockOptions: unknown }).hmMockOptions = { empty: true };
  });
}

/** connectFromOverview follows "Connect agent" from the welcome and checks the open choice. */
async function connectFromOverview(page: Page, t: Text): Promise<void> {
  await page.goto('./');
  await page.getByRole('region', { name: t.welcome }).getByRole('link', { name: t.connect }).click();
  await expect(page).toHaveURL(/#\/agents\?add$/);
  await expect(page.getByRole('heading', { level: 2, name: t.how })).toBeFocused();
  await expect(page.getByRole('link', { name: t.code })).toBeVisible();
  await expect(page.getByRole('link', { name: t.browser })).toBeVisible();
}

test('"Connect agent" on the overview offers both ways, the pairing code leads to pairing', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await emptyHousehold(page);
  await connectFromOverview(page, t);
  await page.getByRole('link', { name: t.code }).click();
  await expect(page).toHaveURL(/#\/agents\/pair$/);
  await expect(page.getByLabel(t.field)).toBeVisible();
});

test('"Connect agent" on the overview offers both ways, browser sign-in leads to its page', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await emptyHousehold(page);
  await connectFromOverview(page, t);
  await page.getByRole('link', { name: t.browser }).click();
  await expect(page).toHaveURL(/#\/agents\/browser$/);
  await expect(page.getByRole('heading', { level: 1, name: t.browserTitle })).toBeVisible();
});

test('the choice is open on arrival also with agents admitted, from the new mandate dialog', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/mandates');
  await page.getByRole('button', { name: t.newMandate }).click();
  await page.getByRole('dialog', { name: t.newMandate }).getByRole('link', { name: t.toAgents }).click();
  await expect(page).toHaveURL(/#\/agents\?add$/);
  await expect(page.getByRole('heading', { level: 2, name: t.how })).toBeFocused();
  await expect(page.getByRole('table', { name: t.agents })).toBeVisible();
  await expect(page.getByRole('button', { name: t.add })).toHaveAttribute('aria-expanded', 'true');
  await page.getByRole('link', { name: t.browser }).click();
  await expect(page.getByRole('heading', { level: 1, name: t.browserTitle })).toBeVisible();
});

test('the choice is open on arrival with agents admitted, and the pairing code leads to pairing', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/agents?add');
  await expect(page.getByRole('heading', { level: 2, name: t.how })).toBeFocused();
  await expect(page.getByRole('table', { name: t.agents })).toBeVisible();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('link', { name: t.code })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByLabel(t.field)).toBeVisible();
});
