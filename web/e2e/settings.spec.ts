// SPDX-License-Identifier: AGPL-3.0-or-later

// Settings against the mock build: the section index, an approver's device switch with
// the keyboard, autosave of a default, lifting the emergency stop inline, the licenses
// file, and the mobile view without sideways scrolling.
import { expect, pageScroll, test } from './support.ts';

const text = {
  de: { sections: 'Abschnitte der Einstellungen', mcp: 'MCP-Endpunkt', approvers: 'Freigebende', critical: 'Auch kritische Rückfragen', normalOnly: 'Bekommt nur normale Rückfragen',
    defaults: 'Standards', rate: 'Tempolimit-Standard', saved: 'Gespeichert', estop: 'Not-Aus', trigger: 'Not-Aus auslösen …', confirm: 'Not-Aus auslösen', lift: 'Not-Aus aufheben …', liftYes: 'Aufheben', cancel: 'Abbrechen',
    about: 'Über', licenses: 'Lizenzen der enthaltenen Pakete' },
  en: { sections: 'Settings sections', mcp: 'MCP endpoint', approvers: 'Approvers', critical: 'Critical requests too', normalOnly: 'Gets normal requests only',
    defaults: 'Defaults', rate: 'Default rate limit', saved: 'Saved', estop: 'Emergency stop', trigger: 'Trigger emergency stop …', confirm: 'Trigger emergency stop', lift: 'Lift emergency stop …', liftYes: 'Lift', cancel: 'Cancel',
    about: 'About', licenses: 'Licenses of the included packages' },
} as const;

type Lang = keyof typeof text;

test('the section index moves to a section and its heading', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/settings');
  await page.getByRole('navigation', { name: t.sections }).getByRole('link', { name: t.mcp }).click();
  await expect(page).toHaveURL(/#\/settings\/mcp$/);
  await expect(page.getByRole('heading', { level: 2, name: t.mcp })).toBeFocused();
});

test('switches critical requests for a device with the keyboard, after the confirmation, and shows the new reach', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/settings/approvers');
  const approvers = page.getByRole('region', { name: t.approvers });
  const toggle = approvers.getByRole('switch', { name: t.critical }).first();
  await toggle.focus();
  await page.keyboard.press('Space');
  // The only way critical requests reach anyone: the change asks first (decision S10).
  await expect(approvers.getByRole('button', { name: t.cancel })).toBeFocused();
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  await expect(approvers.getByText(t.normalOnly)).toBeVisible();
});

test('saves a default after a pause', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/settings/defaults');
  const defaults = page.getByRole('region', { name: t.defaults });
  await defaults.getByLabel(t.rate).fill('30');
  await expect(defaults.getByRole('status').filter({ hasText: t.saved })).toBeVisible();
});

test('triggers the emergency stop from the settings and lifts it after the inline confirmation', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/settings/estop');
  const estop = page.getByRole('region', { name: t.estop });
  await estop.getByRole('button', { name: t.trigger }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: t.confirm, exact: true }).click();
  await estop.getByRole('button', { name: t.lift }).click();
  await expect(estop.getByRole('button', { name: t.cancel })).toBeFocused();
  await estop.getByRole('button', { name: t.liftYes, exact: true }).click();
  await expect(estop.getByRole('button', { name: t.trigger })).toBeVisible();
});

test('links the licenses of the included packages', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/settings/about');
  const link = page.getByRole('region', { name: t.about }).getByRole('link', { name: t.licenses });
  const response = await page.request.get(new URL((await link.getAttribute('href')) ?? '', page.url()).href);
  expect(response.ok()).toBe(true);
  expect(await response.text()).toContain('svelte');
});

test('mobile: settings without sideways scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('./#/settings');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  expect(await pageScroll(page)).toBe(0);
});

test('an invalid field keeps the focus ring (review a11y H2)', async ({ page }, info) => {
  const t = text[info.project.name as Lang];
  await page.goto('./#/settings/defaults');
  const rate = page.getByLabel(t.rate);
  await rate.fill('0');
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Tab');
  await expect(rate).toHaveAttribute('aria-invalid', 'true');
  await expect(rate).toBeFocused();
  const ring = await rate.evaluate((el) => {
    const s = getComputedStyle(el);
    return { style: s.outlineStyle, width: parseFloat(s.outlineWidth) };
  });
  expect(ring).toEqual({ style: 'solid', width: 2 });
});
