// SPDX-License-Identifier: AGPL-3.0-or-later

// The sweep helpers must find what they are meant to find; otherwise a green sweep means nothing.
import { expect, test } from './support.ts';
import { a11yProblems, applyVariant, focusProblems, overflowProblems } from './sweep.ts';

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 600 });
});

test('overflow: finds sideways scrolling, clipped boxes and text outside the viewport', async ({ page }) => {
  await page.setContent(`
    <div style="width: 600px">wide</div>
    <p style="width: 80px; overflow: hidden; white-space: nowrap">clipped text that does not fit</p>
    <p style="width: 80px; height: 20px; overflow: hidden">many lines of text that wrap and are cut off at the bottom</p>`);
  const problems = (await overflowProblems(page)).join('\n');
  expect(problems).toMatch(/page scrolls sideways/);
  expect(problems).toMatch(/clipped sideways .*clipped text/);
  expect(problems).toMatch(/clipped vertically .*many lines/);
  expect(problems).toMatch(/text outside the viewport .*wide/);
});

test('overflow: accepts ellipsis, line clamps, scroll containers and visually hidden text', async ({ page }) => {
  await page.setContent(`
    <p style="width: 80px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis">a long name with an ellipsis</p>
    <p style="width: 80px; overflow: hidden; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2">many lines of text that wrap and are clamped to two lines</p>
    <div style="width: 200px; overflow-x: auto"><div style="width: 800px">a wide table in a scroller</div></div>
    <span style="position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%)">only for screen readers, quite long</span>`);
  expect(await overflowProblems(page)).toEqual([]);
});

test('axe: reports missing names and low contrast', async ({ page }) => {
  await page.setContent(`<html lang="en"><body><main><h1>Test</h1>
    <button></button><p style="color: #bbb; background: #fff">faint text</p></main></body></html>`);
  const problems = (await a11yProblems(page)).join('\n');
  expect(problems).toMatch(/button-name/);
  expect(problems).toMatch(/color-contrast/);
});

test('focus: reports a missing focus ring and focus in inert content', async ({ page }) => {
  await page.setContent(`<style>.bare:focus { outline: none }</style>
    <button>ok</button><button class="bare">no ring</button><div inert><button>inert</button></div>`);
  // Inert content cannot take focus at all; a focusable aria-hidden element can.
  await page.evaluate(() => document.body.insertAdjacentHTML('beforeend', '<div aria-hidden="true"><a href="#x">hidden link</a></div>'));
  const problems = (await focusProblems(page)).join('\n');
  expect(problems).toMatch(/no visible focus indicator: button#\|\|no ring/);
  expect(problems).toMatch(/focus inside inert or hidden content: a#\|\|hidden link/);
  expect(problems).not.toMatch(/\|ok$/m);
});

test('variants take effect in the app: dark colours, mirrored header, narrow layout', async ({ page }) => {
  await page.goto('./');
  await expect(page.locator('h1')).toBeVisible();
  const look = () =>
    page.evaluate(() => {
      const logo = document.querySelector('header')!.firstElementChild!.getBoundingClientRect();
      return { bg: getComputedStyle(document.documentElement).backgroundColor, logoLeft: logo.left < window.innerWidth / 2 };
    });
  await applyVariant(page, { theme: 'light', dir: 'ltr', width: 1280 });
  const light = await look();
  await applyVariant(page, { theme: 'dark', dir: 'rtl', width: 1280 });
  const dark = await look();
  expect(dark.bg).not.toBe(light.bg);
  expect(light.logoLeft).toBe(true);
  expect(dark.logoLeft).toBe(false);
  await applyVariant(page, { theme: 'light', dir: 'ltr', width: 375 });
  expect(await page.evaluate(() => document.documentElement.clientWidth)).toBe(375);
});
