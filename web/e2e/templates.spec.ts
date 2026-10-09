// SPDX-License-Identifier: AGPL-3.0-or-later

// Templates against the mock build (Mandates → Templates): the list with base and own
// templates, a base template in the editor with the approvers placeholder, saving it as a
// new template, deleting that one, hiding a base template so pairing no longer offers it,
// and the mobile views without sideways scrolling.
import type { Page } from '@playwright/test';
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: {
    mandates: 'Mandate',
    templates: 'Vorlagen',
    builtin: 'Mitgelieferte Vorlagen',
    own: 'Eigene Vorlagen',
    readOnly: 'Nur lesen',
    lightClimate: 'Licht und Klima',
    badge: 'Mitgeliefert',
    hiddenBadge: 'Ausgeblendet',
    description: 'Darf den Zustand aller Geräte lesen, aber nichts schalten.',
    placeholder: 'Freigebende des Haushalts und wer den Agenten zulässt',
    rate: 'Tempolimit',
    unsaved: '1 ungespeicherte Änderung',
    saveAs: 'Als neue Vorlage speichern …',
    saveAsTitle: 'Als neue Vorlage speichern',
    name: 'Name der Vorlage',
    create: 'Vorlage anlegen',
    reserved: 'Namen, die mit „hm-“ beginnen, gehören den mitgelieferten Vorlagen.',
    save: 'Speichern …',
    del: 'Löschen',
    deleteTitle: /Vorlage .*meine-vorlage.* löschen\?/,
    cancel: 'Abbrechen',
    hide: 'Ausblenden',
    show: 'Einblenden',
    pairField: 'Kopplungscode',
    verify: 'Ist das der richtige Agent?',
    continue: 'Weiter',
    allows: 'Darf',
    startFrom: 'Mit einer Vorlage starten',
    changeMandate: 'Regeln aus einer Vorlage übernehmen',
    rest: 'Alles andere ist verboten.',
    saveTitle: 'Änderungen speichern?',
    saveTemplate: 'Vorlage speichern',
    rollout: 'Änderung auch in Mandate übernehmen?',
    onlyTemplate: 'Nur die Vorlage',
    takeOverOne: 'In 1 Mandat übernehmen',
    selectNone: 'Keine auswählen',
    takeOverNone: 'In 0 Mandate übernehmen',
    updatedOne: 'Aktualisiert: 1 · Nicht aktualisiert: 0',
    close: 'Schließen',
    rateOfMandate: /1 von 20 Aktionen/,
    conflict: 'Nicht aktualisiert: inzwischen geändert',
    loadAgain: /^.?Sprachassistent.? neu laden$/,
    reloaded: 'Inzwischen geändert – prüfe es und wähle es erneut aus',
    updated: 'Aktualisiert',
  },
  en: {
    mandates: 'Mandates',
    templates: 'Templates',
    builtin: 'Built-in templates',
    own: 'Your templates',
    readOnly: 'Read only',
    lightClimate: 'Light and climate',
    badge: 'Built in',
    hiddenBadge: 'Hidden',
    description: 'May read the state of every device, but switches nothing.',
    placeholder: 'The household’s approvers and whoever admits the agent',
    rate: 'Rate limit',
    unsaved: '1 unsaved change',
    saveAs: 'Save as new template …',
    saveAsTitle: 'Save as new template',
    name: 'Template name',
    create: 'Create template',
    reserved: 'Names starting with “hm-” belong to the built-in templates.',
    save: 'Save …',
    del: 'Delete',
    deleteTitle: /Delete template .*meine-vorlage.*\?/,
    cancel: 'Cancel',
    hide: 'Hide',
    show: 'Show',
    pairField: 'Pairing code',
    verify: 'Is this the right agent?',
    continue: 'Continue',
    allows: 'May',
    startFrom: 'Start from a template',
    changeMandate: 'Take over rules from a template',
    rest: 'Everything else is forbidden.',
    saveTitle: 'Save changes?',
    saveTemplate: 'Save template',
    rollout: 'Take the change over into mandates too?',
    onlyTemplate: 'Only the template',
    takeOverOne: 'Take over into 1 mandate',
    selectNone: 'Select none',
    takeOverNone: 'Take over into 0 mandates',
    updatedOne: 'Updated: 1 · Not updated: 0',
    close: 'Close',
    rateOfMandate: /1 of 20 actions/,
    conflict: 'Not updated: changed in the meantime',
    loadAgain: /^Load .?Sprachassistent.? again$/,
    reloaded: 'Changed in the meantime – check it and choose it again',
    updated: 'Updated',
  },
} as const;

type Lang = keyof typeof text;
const VOICE = encodeURIComponent('pair:voice-assistant');
const texts = (project: string) => text[project as Lang];

/** go navigates inside the app without reloading it, so the mock keeps its state. */
async function go(page: Page, hash: string) {
  await page.evaluate((h) => (window.location.hash = h), hash);
}

test('from the mandates to the templates: base and own templates in plain words', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/mandates');
  await page.getByRole('link', { name: t.templates, exact: true }).click();
  await expect(page).toHaveURL(/#\/templates$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.templates);
  const builtin = page.getByRole('region', { name: t.builtin });
  await expect(builtin.getByRole('article')).toHaveCount(3);
  const first = builtin.getByRole('article').first();
  await expect(first.getByRole('heading')).toHaveText(t.readOnly);
  await expect(first.getByText(t.badge)).toBeVisible();
  await expect(first.getByText(t.description)).toBeVisible();
  await expect(first.getByText(t.allows, { exact: true })).toBeVisible();
  await expect(first.getByText(t.rest)).toBeVisible();
  await expect(page.getByRole('region', { name: t.own }).getByRole('article')).toHaveCount(3);
});

test('a base template in the editor: placeholder kept, saved as a new template, which can be deleted', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/templates');
  await page.getByRole('link', { name: t.readOnly }).click();
  await expect(page).toHaveURL(/#\/templates\/hm-read-only$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.readOnly);
  await expect(page.getByText(t.badge, { exact: true })).toBeVisible();
  // The placeholder is a sentence, chosen, and never its raw value.
  await expect(page.getByRole('button', { name: t.placeholder })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('main')).not.toContainText('$approvers');
  // Base templates are not saved in place.
  await expect(page.getByRole('button', { name: t.save, exact: true })).toHaveCount(0);

  await page.getByLabel(t.rate).fill('30');
  await expect(page.locator('main').getByText(t.unsaved).first()).toBeVisible();
  await page.getByRole('button', { name: t.saveAs, exact: true }).click();
  const dialog = page.getByRole('dialog', { name: t.saveAsTitle });
  await expect(dialog.getByLabel(t.name)).toBeFocused();
  await page.keyboard.type('hm-meine');
  await page.keyboard.press('Enter');
  await expect(dialog.getByText(t.reserved)).toBeVisible();
  await dialog.getByLabel(t.name).fill('meine-vorlage');
  await dialog.getByRole('button', { name: t.create }).click();

  await expect(page).toHaveURL(/#\/templates\/meine-vorlage$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('meine-vorlage');
  await expect(page.getByLabel(t.rate)).toHaveValue('30');
  await expect(page.getByRole('button', { name: t.placeholder })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('button', { name: t.save, exact: true })).toBeVisible();

  await page.getByRole('button', { name: t.del, exact: true }).click();
  const confirm = page.getByRole('alertdialog', { name: t.deleteTitle });
  await expect(confirm.getByRole('button', { name: t.cancel })).toBeFocused();
  await confirm.getByRole('button', { name: t.del, exact: true }).click();
  await expect(page).toHaveURL(/#\/templates$/);
  await expect(page.getByRole('region', { name: t.own }).getByRole('link', { name: 'meine-vorlage' })).toHaveCount(0);
});

test('a hidden base template is marked, offered nowhere, and offered again once shown', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/templates/hm-light-climate');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(t.lightClimate);
  await page.getByRole('button', { name: t.hide }).click();
  await expect(page.getByText(t.hiddenBadge, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: t.show })).toBeVisible();

  // Pairing.
  await go(page, '#/agents/pair');
  await page.getByLabel(t.pairField).fill('bcdf ghjk');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: t.verify })).toBeFocused();
  await page.getByRole('button', { name: t.continue }).click();
  await expect(page.getByRole('radio', { name: t.readOnly })).toBeVisible();
  await expect(page.getByRole('radio', { name: t.lightClimate })).toHaveCount(0);

  // A new mandate from the list, and changing an agent's mandate.
  await go(page, '#/mandates');
  const start = page.getByRole('region', { name: t.startFrom });
  await expect(start.getByRole('heading', { name: t.readOnly })).toBeVisible();
  await expect(start.getByRole('heading', { name: t.lightClimate })).toHaveCount(0);
  await go(page, `#/agents/id/${VOICE}`);
  const change = page.getByLabel(t.changeMandate);
  await expect(change.locator('option', { hasText: t.readOnly })).toHaveCount(1);
  await expect(change.locator('option', { hasText: t.lightClimate })).toHaveCount(0);

  // Shown again: offered again.
  await go(page, '#/templates/hm-light-climate');
  await page.getByRole('button', { name: t.show }).click();
  await expect(page.getByText(t.hiddenBadge, { exact: true })).toHaveCount(0);
  await go(page, '#/mandates');
  await expect(page.getByRole('region', { name: t.startFrom }).getByRole('heading', { name: t.lightClimate })).toBeVisible();
  await go(page, `#/agents/id/${VOICE}`);
  await expect(page.getByLabel(t.changeMandate).locator('option', { hasText: t.lightClimate })).toHaveCount(1);
});

/** saveRate changes the rate limit of the open template and saves it in place. */
async function saveRate(page: Page, t: ReturnType<typeof texts>, value: string) {
  await page.getByLabel(t.rate).fill(value);
  await page.getByRole('button', { name: t.save, exact: true }).click();
  await page.getByRole('dialog', { name: t.saveTitle }).getByRole('button', { name: t.saveTemplate }).click();
}

test('a changed template is taken over into the chosen mandates, with the keyboard only (#18)', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/templates/voice-assistant');
  await saveRate(page, t, '20');
  const dialog = page.getByRole('dialog', { name: t.rollout });
  await expect(dialog).toBeVisible();
  const voice = dialog.getByRole('checkbox', { name: 'Sprachassistent' });
  await expect(voice).toBeChecked();
  // None, then the one again with the keyboard.
  await dialog.getByRole('button', { name: t.selectNone }).click();
  await expect(voice).not.toBeChecked();
  await expect(dialog.getByRole('button', { name: t.takeOverNone })).toHaveAttribute('aria-disabled', 'true');
  await voice.focus();
  await page.keyboard.press('Space');
  await expect(voice).toBeChecked();
  await dialog.getByRole('button', { name: t.takeOverOne }).focus();
  await page.keyboard.press('Enter');
  await expect(dialog.getByRole('status')).toHaveText(t.updatedOne);
  await dialog.getByRole('button', { name: t.close }).click();
  await expect(dialog).toHaveCount(0);
  // The agent's mandate has the template's new rate limit.
  await go(page, `#/agents/id/${VOICE}`);
  await expect(page.getByText(t.rateOfMandate)).toBeVisible();
});

test('“only the template” leaves the mandates as they are (#18)', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/templates/voice-assistant');
  await saveRate(page, t, '20');
  const dialog = page.getByRole('dialog', { name: t.rollout });
  await dialog.getByRole('button', { name: t.onlyTemplate }).click();
  await expect(dialog).toHaveCount(0);
  await go(page, `#/agents/id/${VOICE}`);
  await expect(page.getByText(t.rateOfMandate)).toHaveCount(0);
});

test('a mandate changed meanwhile is loaded again and must be chosen again before it is sent (#18)', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/templates/voice-assistant');
  await saveRate(page, t, '20');
  const dialog = page.getByRole('dialog', { name: t.rollout });
  await expect(dialog.getByRole('checkbox', { name: 'Sprachassistent' })).toBeChecked();
  // Another administrator edits the mandate after the list was shown.
  await page.evaluate(() => (window as unknown as { hmMock: { editMandate(id: string, n: number): void } }).hmMock.editMandate('mandate-voice', 3));
  await dialog.getByRole('button', { name: t.takeOverOne }).click();
  await expect(dialog.getByText(t.conflict)).toBeVisible();
  await dialog.getByRole('button', { name: t.loadAgain }).click();
  const voice = dialog.getByRole('checkbox', { name: 'Sprachassistent' });
  await expect(voice).toBeFocused();
  await expect(voice).not.toBeChecked();
  await expect(dialog.getByText(t.reloaded)).toBeVisible();
  await expect(dialog.getByRole('button', { name: t.takeOverNone })).toHaveAttribute('aria-disabled', 'true');
  await page.keyboard.press('Space');
  await dialog.getByRole('button', { name: t.takeOverOne }).click();
  await expect(dialog.getByText(t.updated, { exact: true })).toBeVisible();
  await dialog.getByRole('button', { name: t.close }).click();
  await go(page, `#/agents/id/${VOICE}`);
  await expect(page.getByText(t.rateOfMandate)).toBeVisible();
});

test('saving a template no mandate uses asks nothing (#18)', async ({ page }, info) => {
  const t = texts(info.project.name);
  await page.goto('./#/templates/empty');
  await saveRate(page, t, '20');
  await expect(page.getByText(t.unsaved)).toHaveCount(0);
  await expect(page.getByRole('dialog', { name: t.rollout })).toHaveCount(0);
});

test('mobile: template list and editor without sideways scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  for (const path of ['./#/templates', './#/templates/hm-voice-cautious', './#/templates/voice-assistant', './#/templates/_new']) {
    await page.goto(path);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(await pageScroll(page), path).toBe(0);
  }
});
