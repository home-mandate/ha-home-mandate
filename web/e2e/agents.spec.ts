// SPDX-License-Identifier: AGPL-3.0-or-later

// Agents against the mock build: the list with hostile names, pairing by code with the
// keyboard only, revoking with the safe default, and the mobile views without sideways
// scrolling.
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: { agents: 'Agenten', add: 'Agent hinzufügen', code: /Mit Kopplungscode/, field: 'Kopplungscode', verify: 'Ist das der richtige Agent?',
    voice: /Sprachassistent/, done: /ist verbunden/, open: 'Zum Agenten', revoke: 'Zugriff entziehen', cancel: 'Abbrechen',
    identity: 'Identität', revoked: /Entzogen am/ },
  en: { agents: 'Agents', add: 'Add agent', code: /With a pairing code/, field: 'Pairing code', verify: 'Is this the right agent?',
    voice: /Voice assistant/, done: /is connected/, open: 'Go to agent', revoke: 'Revoke access', cancel: 'Cancel',
    identity: 'Identity', revoked: /Revoked on/ },
} as const;

type Lang = keyof typeof text;

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
  await page.keyboard.press('Tab'); // templates: "Empty" is chosen
  await page.keyboard.press('ArrowUp');
  await expect(page.getByRole('radio', { name: t.voice })).toBeChecked();
  await page.keyboard.press('Tab'); // Back
  await page.keyboard.press('Tab'); // Approve
  await page.keyboard.press('Enter');

  await expect(page.getByRole('heading', { name: t.done })).toBeFocused();
  await page.getByRole('link', { name: t.open }).click();
  await expect(page.getByRole('region', { name: t.identity })).toBeVisible();
});

test('revokes an agent after the confirmation that starts on Cancel', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto(`./#/agents/${CLAUDE}`);
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
  for (const path of ['./#/agents', './#/agents/pair', './#/agents/browser', `./#/agents/${CLAUDE}`, './#/agents/pair%3Along']) {
    await page.goto(path);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(await pageScroll(page), path).toBe(0);
  }
});
