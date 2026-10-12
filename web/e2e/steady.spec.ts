// SPDX-License-Identifier: AGPL-3.0-or-later

// Marking the active entry moves nothing (GitHub issue #47): the settings index and the header
// tabs mark the active entry without bold text and without a border that takes room, so the
// content column next to the index and the neighbouring tabs keep their place to a fraction of
// a pixel, also in forced colours (Windows high contrast).
import type { Locator, Page } from '@playwright/test';
import { expect, test } from './support.ts';

const WIDTH = 1280;
const HEIGHT = 900;
const TOLERANCE = 0.5;
const MODES = ['none', 'active'] as const;

interface Span {
  x: number;
  width: number;
}

/** settled waits until the browser has laid out and painted the last change. */
const settled = (page: Page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));

/** spans returns the inline position of each element relative to origin, and its width. */
function spans(items: Locator, origin: string): Promise<Span[]> {
  return items.evaluateAll((els, sel) => {
    const base = els[0]?.closest(sel)?.getBoundingClientRect().left ?? 0;
    return els.map((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.left - base, width: r.width };
    });
  }, origin);
}

function expectSame(actual: Span[], expected: Span[], what: string) {
  expect(actual.length, what).toBe(expected.length);
  actual.forEach((s, i) => {
    expect(Math.abs(s.x - expected[i]!.x), `${what} #${i}: x`).toBeLessThanOrEqual(TOLERANCE);
    expect(Math.abs(s.width - expected[i]!.width), `${what} #${i}: width`).toBeLessThanOrEqual(TOLERANCE);
  });
}

for (const forcedColors of MODES) {
  const label = forcedColors === 'active' ? ', forced colours' : '';

  test(`marking a settings section keeps the content column in place${label} (issue #47)`, async ({ page }) => {
    await page.setViewportSize({ width: WIDTH, height: HEIGHT });
    await page.emulateMedia({ forcedColors, reducedMotion: 'reduce' });
    await page.goto('./#/settings');
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();

    const index = page.locator('main nav');
    const links = index.getByRole('link');
    const column = page.locator('main .sections');
    const count = await links.count();
    expect(count).toBeGreaterThan(1);

    let first: Span[] | undefined;
    for (let i = 0; i < count; i++) {
      const link = links.nth(i);
      await link.click();
      await expect(link).toHaveAttribute('aria-current', 'location');
      await settled(page);
      const now = await spans(column, 'main');
      if (first) expectSame(now, first, `content column with entry ${i} marked`);
      else first = now;
    }
  });

  test(`switching header tabs keeps the other tabs in place${label} (issue #47)`, async ({ page }) => {
    await page.setViewportSize({ width: WIDTH, height: HEIGHT });
    await page.emulateMedia({ forcedColors, reducedMotion: 'reduce' });
    await page.goto('./#/');
    const tabs = page.locator('header nav a');
    await expect(tabs.first()).toBeVisible();
    const count = await tabs.count();
    expect(count).toBeGreaterThan(1);

    let first: Span[] | undefined;
    for (let i = 0; i < count; i++) {
      const tab = tabs.nth(i);
      await tab.click();
      await expect(tab).toHaveAttribute('aria-current', 'page');
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await settled(page);
      const now = await spans(tabs, 'header nav');
      if (first) expectSame(now, first, `header tabs with tab ${i} active`);
      else first = now;
    }
  });
}

// Selecting an option moves nothing either: the decision buttons, the grouping of the preview,
// the toggle chips (actions, weekdays, the audit log's decision filter) and the approvers mark
// the selected option without bold text and without a thicker border, so every control of the
// group keeps its box to a fraction of a pixel (follow-up to issue #47).

interface Look {
  x: number;
  y: number;
  width: number;
  height: number;
  weight: string;
  border: string;
}

/** looks returns the box of each control relative to origin, its font weight and border widths. */
function looks(items: Locator, origin: string): Promise<Look[]> {
  return items.evaluateAll((els, sel) => {
    const base = els[0]?.closest(sel)?.getBoundingClientRect();
    return els.map((el) => {
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return {
        x: r.left - (base?.left ?? 0),
        y: r.top - (base?.top ?? 0),
        width: r.width,
        height: r.height,
        weight: s.fontWeight,
        border: [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth].join(' '),
      };
    });
  }, origin);
}

function expectSameLooks(actual: Look[], expected: Look[], what: string) {
  expect(actual.length, what).toBe(expected.length);
  actual.forEach((l, i) => {
    const e = expected[i]!;
    for (const key of ['x', 'y', 'width', 'height'] as const) {
      expect(Math.abs(l[key] - e[key]), `${what} #${i}: ${key}`).toBeLessThanOrEqual(TOLERANCE);
    }
    expect(l.weight, `${what} #${i}: font weight`).toBe(e.weight);
    expect(l.border, `${what} #${i}: border width`).toBe(e.border);
  });
}

/** clickEach clicks every control in turn and checks that all of them keep their place and look. */
async function clickEach(page: Page, controls: Locator, origin: string, what: string) {
  const count = await controls.count();
  expect(count, what).toBeGreaterThan(1);
  await settled(page);
  const first = await looks(controls, origin);
  for (let i = 0; i < count; i++) {
    await controls.nth(i).click();
    await settled(page);
    expectSameLooks(await looks(controls, origin), first, `${what} after clicking #${i}`);
  }
}

interface Mark {
  pressed: string | null;
  mark: string | null;
  icons: number;
  width: number;
  height: number;
  visible: boolean;
}

/** marks returns, per control, its state and the mark in its slot: a check or an x. */
function marks(controls: Locator): Promise<Mark[]> {
  return controls.evaluateAll((els) =>
    els.map((el) => {
      const slot = el.querySelector('.mark');
      const icon = slot?.querySelector('svg');
      const r = icon?.getBoundingClientRect();
      const s = icon ? getComputedStyle(icon) : undefined;
      return {
        pressed: el.getAttribute('aria-pressed'),
        mark: slot?.getAttribute('data-mark') ?? null,
        icons: slot?.querySelectorAll('svg').length ?? 0,
        width: r?.width ?? 0,
        height: r?.height ?? 0,
        visible: s?.visibility === 'visible' && s.opacity === '1' && s.display !== 'none',
      };
    }),
  );
}

/** expectMarks checks that every chip shows a check when pressed and an x when not. */
async function expectMarks(controls: Locator, what: string) {
  const found = await marks(controls);
  expect(found.length, what).toBeGreaterThan(1);
  found.forEach((m, i) => {
    expect(m.mark, `${what} #${i}: mark`).toBe(m.pressed === 'true' ? 'on' : 'off');
    expect(m.icons, `${what} #${i}: one icon`).toBe(1);
    expect(m.visible, `${what} #${i}: visible`).toBe(true);
    expect([m.width, m.height], `${what} #${i}: size`).toEqual([16, 16]);
  });
}

async function openRule(page: Page) {
  await page.goto('./#/mandates/mandate-voice');
  await expect(page.locator('h1')).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  await page.locator('main ol > li .head .btn.text').first().click();
  await expect(page.locator('main li.editing')).toBeVisible();
}

for (const forcedColors of MODES) {
  const label = forcedColors === 'active' ? ', forced colours' : '';

  test.describe(`selected options${label}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width: WIDTH, height: HEIGHT });
      await page.emulateMedia({ forcedColors, reducedMotion: 'reduce' });
    });

    test('choosing a decision keeps the decision buttons in place', async ({ page }) => {
      await openRule(page);
      const group = page.locator('main li.editing [role="radiogroup"]').first();
      await clickEach(page, group.getByRole('radio'), '[role="radiogroup"]', 'decision buttons');
    });

    test('toggling actions and weekdays keeps the chips in place', async ({ page }) => {
      await openRule(page);
      const rows = page.locator('main li.editing .chips');
      await clickEach(page, rows.first().locator('button.chip'), '.chips', 'action chips');
      await clickEach(page, rows.last().locator('button.chip'), '.chips', 'weekday chips');
      await expectMarks(rows.last().locator('button.chip'), 'weekday chips');
    });

    test('toggling approvers keeps the people in place', async ({ page }) => {
      // A template offers the approvers placeholder next to the people.
      await page.goto('./#/templates/voice-assistant');
      await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
      const people = page.locator('main .people').first().locator('button.person');
      await expectMarks(people, 'approvers');
      await clickEach(page, people, '.people', 'approvers');
      await expectMarks(people, 'approvers');
    });

    test('regrouping the preview keeps the grouping buttons in place', async ({ page }) => {
      await page.goto('./#/mandates/mandate-voice');
      await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
      const radios = page.locator('main .grouping [role="radio"]');
      await clickEach(page, radios, '.grouping', 'grouping buttons');
    });

    test('toggling the decision filter keeps the chips in place', async ({ page }) => {
      await page.goto('./#/audit');
      await expect(page.locator('h1')).toBeVisible();
      const chips = page.locator('main fieldset .chips').first().locator('button.chip');
      await expectMarks(chips, 'decision filter chips');
      await clickEach(page, chips, '.chips', 'decision filter chips');
      await expectMarks(chips, 'decision filter chips');
    });
  });
}
