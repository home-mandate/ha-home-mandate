// SPDX-License-Identifier: AGPL-3.0-or-later

// Mandates against the mock build: list, editor with save summary, the separate
// confirmation for critical actions, preview matrix, versions, the mobile layout and
// hostile names from agents and Home Assistant.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: {
    list: 'Mandate',
    templates: 'Mit einer Vorlage starten',
    edit: (n: number) => `Bearbeiten: Regel ${n}`,
    ask: 'Nachfragen',
    allow: 'Erlauben',
    unsaved: '1 ungespeicherte Änderung',
    saved: 'Alles gespeichert',
    save: 'Speichern …',
    summary: 'Änderungen speichern?',
    keep: 'Weiter bearbeiten',
    confirm: 'Als Version 2 speichern',
    toast: 'Mandat gespeichert · Version 2',
    effect: /Neu mit Rückfrage · 8/,
    versions: 'Versionen',
    compare: 'Version v1 mit v2 vergleichen',
    override: 'Auch kritische Aktionen ohne Rückfrage erlauben',
    question: 'Kritische Aktionen ohne Rückfrage erlauben?',
    keepApproval: 'Rückfrage behalten',
    allowWithout: 'Ohne Rückfrage erlauben',
    criticalFlag: 'Enthält kritische Aktionen ohne Rückfrage',
    preview: 'Vorschau',
    rules: 'Regeln',
    lights: 'Licht',
    scripts: /^Skript/,
    script: 'Skript',
    turnOn: /Küchenlicht.*einschalten: Erlaubt/,
    turnOff: /Küchenlicht.*ausschalten: Erlaubt/,
    fromRule: /Regel 1: Licht.*1 Regel greift/,
    done: 'Fertig',
    addRule: 'Regel hinzufügen',
    errors: /1 Fehler/,
  },
  en: {
    list: 'Mandates',
    templates: 'Start from a template',
    edit: (n: number) => `Edit: Rule ${n}`,
    ask: 'Ask',
    allow: 'Allow',
    unsaved: '1 unsaved change',
    saved: 'All saved',
    save: 'Save …',
    summary: 'Save changes?',
    keep: 'Keep editing',
    confirm: 'Save as version 2',
    toast: 'Mandate saved · version 2',
    effect: /Newly needs approval · 8/,
    versions: 'Versions',
    compare: 'Compare version v1 with v2',
    override: 'Also allow critical actions without approval',
    question: 'Allow critical actions without approval?',
    keepApproval: 'Keep approval',
    allowWithout: 'Allow without approval',
    criticalFlag: 'Contains critical actions without approval',
    preview: 'Preview',
    rules: 'Rules',
    lights: 'Lights',
    scripts: /^Script/,
    script: 'Script',
    turnOn: /Küchenlicht.*turn on: Allowed/,
    turnOff: /Küchenlicht.*turn off: Allowed/,
    fromRule: /Rule 1: Lights.*1 rule applies/,
    done: 'Done',
    addRule: 'Add rule',
    errors: /1 error/,
  },
} as const;

type Texts = (typeof text)[keyof typeof text];
const texts = (project: string): Texts => text[project as keyof typeof text];
const MANDATE = 'Sprachassistent Küche';

async function openEditor(page: Page) {
  await page.goto('./#/mandates/mandate-voice');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(MANDATE);
}

test('from the list into the editor: change a rule, read the summary, save a version, compare', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/mandates');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.list);
  await expect(page.getByRole('region', { name: t.templates }).getByRole('article')).toHaveCount(3);
  await page.getByRole('link', { name: MANDATE }).click();
  await expect(page).toHaveURL(/#\/mandates\/mandate-voice$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(MANDATE);

  await page.getByRole('button', { name: t.edit(1) }).click();
  await page.getByRole('radio', { name: t.ask, exact: true }).click();
  await expect(page.getByText(t.unsaved)).toBeVisible();
  // The preview follows the draft at once.
  await expect(page.getByRole('gridcell', { name: /Küchenlicht/ }).first()).toContainText(info.project.name === 'de' ? 'Nachfragen' : 'Ask first');

  await page.getByRole('button', { name: t.save }).click();
  const dialog = page.getByRole('dialog', { name: t.summary });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: t.keep })).toBeFocused();
  await expect(dialog.getByText(t.effect)).toBeVisible();
  await dialog.getByRole('button', { name: t.confirm }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(t.toast)).toBeVisible();
  // Shown next to the save button and, for screen readers, as a status.
  await expect(page.getByRole('status').filter({ hasText: t.saved })).toHaveCount(1);
  await expect(page.getByText(t.saved).first()).toBeVisible();

  await page.getByRole('link', { name: t.versions }).click();
  await expect(page).toHaveURL(/#\/mandates\/mandate-voice\/versions$/);
  await expect(page.getByRole('heading', { name: t.compare })).toBeVisible();
});

test('critical actions without approval need the separate confirmation, with the keyboard only', async ({ page }, info) => {
  const t = texts(info.project.name);
  await openEditor(page);
  await page.getByRole('button', { name: t.edit(3) }).focus();
  await page.keyboard.press('Enter');
  await page.getByRole('radio', { name: t.ask, exact: true }).focus();
  // Arrow keys move and select in the decision group: back from "ask" to "allow".
  await page.keyboard.press('ArrowLeft');
  await expect(page.getByRole('radio', { name: t.allow, exact: true })).toBeChecked();

  const toggle = page.getByRole('switch', { name: t.override });
  await toggle.focus();
  await page.keyboard.press('Space');
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  const question = page.getByRole('group', { name: t.question });
  await expect(question.getByRole('button', { name: t.keepApproval })).toBeFocused();
  // The safe choice has the focus: Enter keeps the approval.
  await page.keyboard.press('Enter');
  await expect(question).toBeHidden();
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  await expect(toggle).toBeFocused();

  await page.keyboard.press('Space');
  await page.keyboard.press('Tab');
  await expect(question.getByRole('button', { name: t.allowWithout })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-checked', 'true');

  await page.keyboard.press('Control+s');
  const dialog = page.getByRole('alertdialog', { name: t.summary });
  await expect(dialog.getByText(t.criticalFlag)).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(page.getByText(t.unsaved)).toBeVisible();
});

test('the preview matrix is a grid: arrow keys move, the selection follows and is explained', async ({ page }, info) => {
  const t = texts(info.project.name);
  await openEditor(page);
  const grid = page.getByRole('grid', { name: t.lights, exact: true });
  const first = grid.getByRole('gridcell').first();
  await first.focus();
  await page.keyboard.press('ArrowRight');
  const turnOn = grid.getByRole('gridcell', { name: t.turnOn });
  await expect(turnOn).toBeFocused();
  await expect(turnOn).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByText(t.fromRule)).toBeVisible();
  await page.keyboard.press('ArrowRight');
  await expect(grid.getByRole('gridcell', { name: t.turnOff })).toBeFocused();
  // One cell of the grid is in the tab order.
  expect(await grid.locator('[role="gridcell"][tabindex="0"]').count()).toBe(1);
});

test('a save attempt with errors lists them instead of saving', async ({ page }, info) => {
  const t = texts(info.project.name);
  await openEditor(page);
  await page.getByRole('button', { name: t.addRule }).click();
  // The new rule starts with "read"; without any action it is invalid.
  await page.getByRole('button', { name: info.project.name === 'de' ? 'lesen' : 'read', exact: true }).click();
  await page.getByRole('button', { name: t.save }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  // The list of problems takes the focus; its count is announced once.
  await expect(page.getByRole('group').filter({ hasText: t.errors })).toBeFocused();
  await expect(page.getByRole('alert').filter({ hasText: t.errors })).toHaveCount(1);
});

test('mobile: rules and preview behind tabs, cards instead of a grid, no horizontal page scroll', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.setViewportSize({ width: 375, height: 760 });
  await page.goto('./#/mandates');
  await expect(page.getByRole('link', { name: new RegExp(MANDATE) })).toBeVisible();
  await expect(page.getByRole('table')).toHaveCount(0);
  expect(await pageScroll(page)).toBe(0);

  await openEditor(page);
  expect(await pageScroll(page)).toBe(0);
  await page.getByRole('button', { name: t.edit(3) }).click();
  expect(await pageScroll(page)).toBe(0);
  for (const name of [t.done, t.save]) {
    const box = await page.getByRole('button', { name, exact: true }).boundingBox();
    expect(box?.height).toBeGreaterThanOrEqual(44);
  }
  await page.getByRole('button', { name: t.done, exact: true }).click();

  await page.getByRole('tab', { name: t.preview }).click();
  await expect(page.getByRole('tabpanel', { name: t.preview })).toBeVisible();
  await expect(page.getByRole('grid')).toHaveCount(0);
  await expect(page.getByText('light.kitchen')).toBeVisible();
  expect(await pageScroll(page)).toBe(0);

  await page.goto('./#/mandates/mandate-voice/versions');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.versions);
  expect(await pageScroll(page)).toBe(0);
});

test('hostile names from agents and Home Assistant are shown as text', async ({ page }, info) => {
  const t = texts(info.project.name);
  let dialogs = 0;
  page.on('dialog', (dialog) => {
    dialogs++;
    void dialog.dismiss();
  });
  await page.goto('./#/mandates');
  // An agent name with bidi overrides: the override characters are gone.
  await expect(page.getByText('Helfer gnalnegrom x')).toBeVisible();

  await openEditor(page);
  await page.getByRole('button', { name: t.scripts }).click();
  const hostile = page.getByRole('grid', { name: t.script, exact: true }).getByRole('rowheader');
  await expect(hostile).toContainText('<img src=x onerror=alert(1)>');
  expect(await page.locator('main img, main b').count()).toBe(0);
  expect(dialogs).toBe(0);
});
