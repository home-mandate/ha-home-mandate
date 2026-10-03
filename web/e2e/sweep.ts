// SPDX-License-Identifier: AGPL-3.0-or-later

// Helpers for the screen sweep (docs/TESTING.md section 3, "UI screen sweep"): every screen in light and dark,
// left-to-right and right-to-left, at 375 and 1280 px, checked for clipped text, WCAG 2.2 AA
// (axe) and a visible keyboard focus.
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';

export interface Variant {
  theme: 'light' | 'dark';
  dir: 'ltr' | 'rtl';
  width: 375 | 1280;
}

export const VARIANTS: readonly Variant[] = (['light', 'dark'] as const).flatMap((theme) =>
  (['ltr', 'rtl'] as const).flatMap((dir) => ([375, 1280] as const).map((width) => ({ theme, dir, width }))),
);

export const label = (v: Variant) => `${v.theme}/${v.dir}/${v.width}`;

const HEIGHT = 900;

/** applyVariant switches theme, direction and width without reloading the page. */
export async function applyVariant(page: Page, v: Variant): Promise<void> {
  await page.emulateMedia({ colorScheme: v.theme, reducedMotion: 'reduce' });
  await page.setViewportSize({ width: v.width, height: HEIGHT });
  await page.evaluate((dir) => {
    document.documentElement.dir = dir;
  }, v.dir);
  // One frame for layout, then fonts and media queries have settled.
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
}

/**
 * overflowProblems lists clipped or escaping content (decision P3):
 * the page scrolls sideways; an element hides content (overflow hidden/clip) without an
 * ellipsis or line clamp; or text reaches outside the viewport. Scroll containers
 * (overflow auto/scroll) and visually hidden helpers (1 px) are fine.
 */
export function overflowProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const problems: string[] = [];
    const root = document.documentElement;
    if (root.scrollWidth > root.clientWidth + 1) problems.push(`page scrolls sideways by ${root.scrollWidth - root.clientWidth}px`);

    const describe = (el: Element) => {
      const cls = typeof el.className === 'string' && el.className ? `.${el.className.trim().split(/\s+/).slice(0, 2).join('.')}` : '';
      const text = (el.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 40);
      return `<${el.tagName.toLowerCase()}${cls}> "${text}"`;
    };
    const visible = (el: Element) => {
      if (el.closest('[inert], [hidden], [aria-hidden="true"]')) return false;
      const r = el.getBoundingClientRect();
      if (r.width <= 1 || r.height <= 1) return false;
      const s = getComputedStyle(el);
      return s.visibility !== 'hidden' && s.display !== 'none' && s.opacity !== '0';
    };
    const clips = (v: string) => v === 'hidden' || v === 'clip';
    const scrolls = (v: string) => v === 'auto' || v === 'scroll';
    const insideScroller = (el: Element) => {
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const s = getComputedStyle(p);
        if (scrolls(s.overflowX) || clips(s.overflowX)) return true;
      }
      return false;
    };
    const width = window.innerWidth;

    for (const el of document.body.querySelectorAll('*')) {
      if (!(el instanceof HTMLElement) || !visible(el)) continue;
      const s = getComputedStyle(el);
      // Native controls scroll their own text (inputs, selects, textareas).
      const control = el.matches('input, select, textarea');
      if (!control && clips(s.overflowX) && el.scrollWidth > el.clientWidth + 1 && s.textOverflow !== 'ellipsis') {
        problems.push(`clipped sideways by ${el.scrollWidth - el.clientWidth}px: ${describe(el)}`);
      }
      const clamped = s.getPropertyValue('-webkit-line-clamp') !== 'none' && s.getPropertyValue('-webkit-line-clamp') !== '';
      if (!control && clips(s.overflowY) && !clamped && el.scrollHeight > el.clientHeight + 1) {
        problems.push(`clipped vertically by ${el.scrollHeight - el.clientHeight}px: ${describe(el)}`);
      }
      const ownText = [...el.childNodes].some((n) => n.nodeType === Node.TEXT_NODE && n.textContent?.trim());
      if (ownText && !insideScroller(el)) {
        const r = el.getBoundingClientRect();
        if (r.right > width + 1 || r.left < -1) problems.push(`text outside the viewport (${Math.round(r.left)}–${Math.round(r.right)}): ${describe(el)}`);
      }
    }
    return problems;
  });
}

/** WCAG 2.2 A and AA, the level docs/TESTING.md asks for. */
const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];

/** a11yProblems runs axe on the page and lists each violation with its elements. */
export async function a11yProblems(page: Page): Promise<string[]> {
  const result = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  return result.violations.flatMap((v) => v.nodes.map((n) => `${v.id} (${v.impact ?? '?'}): ${n.target.join(' ')} – ${n.failureSummary?.split('\n')[1]?.trim() ?? v.help}`));
}

/**
 * focusProblems tabs through the page and lists focus stops that are hidden, covered by an
 * overlay, inside inert content or without a visible focus indicator. It stops when focus
 * comes back to the first stop or after max stops.
 */
export async function focusProblems(page: Page, max = 120): Promise<string[]> {
  const problems: string[] = [];
  await page.evaluate(() => {
    (document.activeElement as HTMLElement | null)?.blur();
    window.scrollTo(0, 0);
  });
  let first: string | null = null;
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab');
    const stop = await page.evaluate(() => {
      const el = document.activeElement;
      if (!el || el === document.body) return null;
      const id = `${el.tagName.toLowerCase()}#${el.id}|${el.getAttribute('aria-label') ?? ''}|${(el.textContent ?? '').trim().slice(0, 30)}`;
      const r = el.getBoundingClientRect();
      // The focus colour as the browser writes it, to tell a focus shadow from a decorative one.
      const probe = document.createElement('span');
      probe.style.color = 'var(--hm-color-focus)';
      document.body.append(probe);
      const focusColor = getComputedStyle(probe).color;
      probe.remove();
      const ringOf = (s: CSSStyleDeclaration) =>
        (s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0) || s.boxShadow.includes(focusColor);
      // Some controls draw the ring on a wrapper (e.g. a visually hidden input in a switch).
      const ring = ringOf(getComputedStyle(el));
      const parentRing = !!el.parentElement && ringOf(getComputedStyle(el.parentElement));
      const cx = Math.min(Math.max(r.left + r.width / 2, 0), window.innerWidth - 1);
      const cy = Math.min(Math.max(r.top + r.height / 2, 0), window.innerHeight - 1);
      const top = document.elementFromPoint(cx, cy);
      return {
        id,
        inert: !!el.closest('[inert], [aria-hidden="true"]'),
        tiny: r.width < 1 || r.height < 1,
        ring: ring || parentRing,
        covered: !!top && top !== el && !el.contains(top) && !top.contains(el) && !(el as HTMLElement).closest('label')?.contains(top),
      };
    });
    if (!stop) continue;
    if (stop.id === first) break;
    first ??= stop.id;
    if (stop.inert) problems.push(`focus inside inert or hidden content: ${stop.id}`);
    if (stop.tiny) problems.push(`focus on an invisible element: ${stop.id}`);
    if (!stop.tiny && !stop.ring) problems.push(`no visible focus indicator: ${stop.id}`);
    if (!stop.tiny && stop.covered) problems.push(`focused element covered by another: ${stop.id}`);
  }
  return problems;
}
