// SPDX-License-Identifier: AGPL-3.0-or-later

// The first day of the week follows the household's country in Home Assistant (#43): the
// weekday picker of the rule form and the rule summaries in a household in the US (Sunday
// first) and in Germany (Monday first), and the country in the settings – in de, en and the
// pseudo-localized build. The weekday names come from Intl, so the pseudo build shows the
// German ones; its other texts are pseudo-localized and not looked up here.
import type { Page } from '@playwright/test';
import { expect, test } from './support.ts';

/** The US household of the mock (src/lib/api/fixtures.ts usHouseholdFixture). */
const US = {
  time_zone: 'America/New_York',
  language: 'en',
  unit_system: { temperature: '°F', length: 'mi', mass: 'lb', volume: 'gal', pressure: 'psi', wind_speed: 'mph' },
  country: 'US',
};

const days = {
  de: { sunday: ['So', 'Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa'], monday: ['Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa', 'So'], off: ['Mi', 'Do', 'Fr', 'Sa'] },
  en: { sunday: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'], monday: ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'], off: ['Wed', 'Thu', 'Fri', 'Sat'] },
} as const;

const text = {
  de: { days: days.de, sundayRun: /So\s–\sDi/, mondayList: /Mo, Di und So/, us: 'Vereinigte Staaten (US)', de: 'Deutschland (DE)' },
  en: { days: days.en, sundayRun: /Sun\s–\sTue/, mondayList: /Mon, Tue, Sun/, us: 'United States (US)', de: 'Germany (DE)' },
  pseudo: { days: days.de, sundayRun: /So\s–\sDi/, mondayList: /Mo, Di und So/, us: 'Véréïñïgté Stáátéñ (ÜS)', de: 'Déütsçhláñd (DÉ)' },
} as const;

type Lang = keyof typeof text;

const HOUSEHOLDS = [
  { name: 'a household in the US starts the week on Sunday', mock: { household: US }, order: 'sunday', summary: 'sundayRun', country: 'us' },
  { name: 'a household in Germany starts the week on Monday', mock: null, order: 'monday', summary: 'mondayList', country: 'de' },
] as const;

async function start(page: Page, mock: Record<string, unknown> | null, path: string): Promise<void> {
  if (mock) {
    await page.addInitScript((options) => {
      (window as unknown as { hmMockOptions: unknown }).hmMockOptions = options;
    }, mock);
  }
  await page.goto(path);
  await expect(page.locator('h1, h2').first()).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
}

for (const household of HOUSEHOLDS) {
  test(`weekdays: ${household.name} in the picker and the summary`, async ({ page }, info) => {
    const t = text[info.project.name as Lang];
    await start(page, household.mock, './#/mandates/mandate-voice');
    // Rule 1 has no weekday condition: every day is on.
    await page.locator('main ol > li .head .btn.text').first().click();
    const picker = page.locator('main li.editing [role="group"][aria-describedby$="-days"]');
    await expect(picker.getByRole('button')).toHaveText([...t.days[household.order]]);
    // Off: Wednesday to Saturday. Left: Sunday, Monday, Tuesday – a run only where the week starts on Sunday.
    for (const day of t.days.off) await picker.getByRole('button', { name: day, exact: true }).click();
    await expect(page.locator('main ol > li').first()).toContainText(t[household.summary]);
  });

  test(`weekdays: ${household.name}, and the settings show the country`, async ({ page }, info) => {
    const t = text[info.project.name as Lang];
    await start(page, household.mock, './#/settings/ha');
    const section = page.locator('main section:has(h2[id$="-ha"])');
    await expect(section.locator('dd').last()).toContainText(t[household.country]);
  });
}
