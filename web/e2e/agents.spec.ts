// SPDX-License-Identifier: AGPL-3.0-or-later

// Agents against the mock build: the list with hostile names, pairing by code with the
// keyboard only, revoking with the safe default, and the mobile views without sideways
// scrolling.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: { agents: 'Agenten', add: 'Agent hinzufügen', code: /Mit Kopplungscode/, field: 'Kopplungscode', verify: 'Ist das der richtige Agent?',
    voice: /Sprachassistent/, done: /ist verbunden/, open: 'Zum Agenten', revoke: 'Zugriff entziehen', cancel: 'Abbrechen',
    identity: 'Identität', revoked: /Entzogen am/, approvers: 'Wer Rückfragen beantworten darf', you: '(du)',
    noChannel: 'kein Weg für Rückfragen eingerichtet', nobodyCritical: /Niemand kann Rückfragen zu kritischen Aktionen/,
    setup: 'Freigebende einrichten', unknown: /Es ließ sich nicht prüfen/ },
  en: { agents: 'Agents', add: 'Add agent', code: /With a pairing code/, field: 'Pairing code', verify: 'Is this the right agent?',
    voice: /Voice assistant/, done: /is connected/, open: 'Go to agent', revoke: 'Revoke access', cancel: 'Cancel',
    identity: 'Identity', revoked: /Revoked on/, approvers: 'Who may approve', you: '(you)',
    noChannel: 'no channel for approval requests', nobodyCritical: /Nobody can answer approval requests for critical actions/,
    setup: 'Set up approvers', unknown: /Could not check whether anyone/ },
} as const;

type Lang = keyof typeof text;

/** More arrow presses than there are templates in the mock. */
const TEMPLATE_STEPS = 12;
const CLAUDE = encodeURIComponent('https://claude.ai/oauth/claude-code-client-metadata');

test('lists agents and shows hostile names as text', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/agents');
  const table = page.getByRole('table', { name: t.agents });
  await expect(table.getByRole('row')).toHaveCount(6);
  await expect(table.getByText('<img src=x onerror=alert(1)>', { exact: false })).toBeVisible();
  await expect(table.locator('img')).toHaveCount(0);
});

test('pairs an agent by code with the keyboard only', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/agents');
  await page.getByRole('button', { name: t.add }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('link', { name: t.code })).toBeFocused();
  await page.keyboard.press('Enter');

  await page.getByLabel(t.field).focus();
  await page.keyboard.type('bcdf ghjk');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: t.verify })).toBeFocused();
  await page.keyboard.press('Tab'); // "This isn't my agent"
  await page.keyboard.press('Tab'); // Continue
  await page.keyboard.press('Enter');

  await page.keyboard.press('Tab'); // display name
  await page.keyboard.press('Tab'); // templates: the most cautious one is chosen
  // Arrow keys move through the templates, whatever their order, to the voice assistant.
  const voice = page.getByRole('radio', { name: t.voice });
  for (let i = 0; i < TEMPLATE_STEPS && !(await voice.isChecked()); i++) await page.keyboard.press('ArrowDown');
  await expect(voice).toBeChecked();
  await expect(voice).toBeFocused();
  await page.keyboard.press('Tab'); // Back
  await page.keyboard.press('Tab'); // Approve
  await page.keyboard.press('Enter');

  await expect(page.getByRole('heading', { name: t.done })).toBeFocused();
  await page.getByRole('link', { name: t.open }).click();
  await expect(page.getByRole('group', { name: t.identity })).toBeVisible();
});

/** toTemplates goes through the pairing to the choice of template, with mock options. */
async function toTemplates(page: Page, options: Record<string, unknown>, lang: Lang) {
  await page.addInitScript((o) => {
    (window as unknown as { hmMockOptions: unknown }).hmMockOptions = o;
  }, options);
  await page.goto('./#/agents/pair');
  await page.getByLabel(text[lang].field).fill('bcdf ghjk');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: text[lang].verify })).toBeFocused();
  await page.keyboard.press('Tab'); // "This isn't my agent"
  await page.keyboard.press('Tab'); // Continue
  await page.keyboard.press('Enter');
  await page.getByRole('radio', { name: text[lang].voice }).check();
}

test('pairing shows who may approve and warns, without blocking, when nobody can', async ({ page }, info) => {
  const lang = info.project.name as Lang;
  const t = text[lang];
  await toTemplates(page, { noApprovers: true }, lang);
  const region = page.getByRole('region', { name: t.approvers });
  await expect(region.getByRole('listitem')).toHaveCount(1);
  await expect(region.getByRole('listitem')).toContainText('Markus');
  await expect(region.getByRole('listitem')).toContainText(t.you);
  await expect(region.getByRole('listitem')).toContainText(t.noChannel);
  await expect(region.getByRole('alert')).toContainText(t.nobodyCritical);
  await expect(region.getByRole('link', { name: t.setup })).toHaveAttribute('href', '#/settings/approvers');
  // The human decides: approving still works.
  await page.getByRole('button', { name: lang === 'de' ? 'Agent zulassen' : 'Approve agent' }).click();
  await expect(page.getByRole('heading', { name: t.done })).toBeFocused();
});

test('pairing names a reachable approver without a warning, and says unknown when it cannot check', async ({ page }, info) => {
  const lang = info.project.name as Lang;
  const t = text[lang];
  await toTemplates(page, {}, lang);
  const region = page.getByRole('region', { name: t.approvers });
  await expect(region.getByRole('listitem')).toContainText('Markus');
  await expect(region.getByRole('alert')).toHaveCount(0);

  await toTemplates(page, { failures: { templateApprovers: 'unavailable' } }, lang);
  await expect(page.getByRole('region', { name: t.approvers }).getByRole('alert')).toContainText(t.unknown);
  await expect(page.getByRole('region', { name: t.approvers }).getByRole('listitem')).toHaveCount(0);
});

test('revokes an agent after the confirmation that starts on Cancel', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto(`./#/agents/id/${CLAUDE}`);
  await page.getByRole('button', { name: t.revoke }).focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('alertdialog');
  await expect(dialog.getByRole('button', { name: t.cancel })).toBeFocused();
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText(t.revoked)).toBeVisible();
  await expect(page.getByRole('button', { name: t.revoke })).toHaveCount(0);
});

test('mobile: list, pairing and detail without sideways scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  for (const path of ['./#/agents', './#/agents/pair', './#/agents/browser', `./#/agents/id/${CLAUDE}`, './#/agents/id/pair%3Along']) {
    await page.goto(path);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(await pageScroll(page), path).toBe(0);
  }
});
