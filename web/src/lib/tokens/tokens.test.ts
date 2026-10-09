// SPDX-License-Identifier: AGPL-3.0-or-later
/// <reference types="node" />

// The colour tokens in both schemes (issue #12): a forced theme ([data-hm-theme]) must give
// the same values as the system scheme, set the matching color-scheme, and every text and
// control pair keeps WCAG 2.2 AA contrast.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// Vitest stubs CSS imports, so the file is read from disk (tests run in web/).
const CSS = readFileSync(join(process.cwd(), 'src/lib/tokens/tokens.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

/** selectorOf returns the selector of a rule chunk, without a surrounding @media prelude. */
const selectorOf = (rule: string) =>
  (rule.slice(0, rule.lastIndexOf('{')).split('{').pop() ?? '').replace(/\s+/g, ' ').replace(/ ?, ?/g, ', ').trim();

/** body returns the declarations of every rule whose selector is exactly selector, in order. */
function body(selector: string): string {
  const rules = CSS.split(/(?<=})/).filter((rule) => rule.includes('{') && selectorOf(rule) === selector);
  if (rules.length === 0) throw new Error(`no rule for ${selector}`);
  return rules.map((rule) => rule.slice(rule.lastIndexOf('{') + 1, rule.lastIndexOf('}'))).join(';');
}

function declarations(block: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const part of block.split(';')) {
    const colon = part.indexOf(':');
    if (colon < 0) continue;
    out.set(part.slice(0, colon).trim(), part.slice(colon + 1).trim());
  }
  return out;
}

const colours = (m: Map<string, string>) => new Map([...m].filter(([k]) => k.startsWith('--hm-color-') || k.startsWith('--hm-shadow-')));

const LIGHT = declarations(body(':root, [data-hm-theme="light"]'));
const DARK = declarations(body('[data-hm-theme="dark"]'));
const SYSTEM_DARK = declarations(body(':root:not([data-hm-theme="light"])'));

function luminance(hex: string): number {
  const v = hex.replace('#', '');
  const [r, g, b] = [0, 2, 4].map((i) => {
    const c = parseInt(v.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
}

const TEXT = 4.5;
const NON_TEXT = 3;

/** [foreground, background, minimum ratio]: text 4.5:1, controls and focus 3:1. */
const PAIRS: readonly [string, string, number][] = [
  ...['bg', 'surface', 'surface-raised', 'surface-sunken', 'surface-hover', 'surface-pressed'].flatMap(
    (bg): [string, string, number][] => [
      ['text', bg, TEXT],
      ['text-muted', bg, TEXT],
    ],
  ),
  ...['bg', 'surface', 'surface-raised', 'surface-hover'].map((bg): [string, string, number] => ['text-subtle', bg, TEXT]),
  ['accent-text', 'surface', TEXT],
  ['accent-text', 'bg', TEXT],
  ['accent-text', 'accent-subtle', TEXT],
  ['accent-text', 'surface-hover', TEXT],
  ['text-on-accent', 'accent', TEXT],
  ['text-on-accent', 'accent-hover', TEXT],
  ['text-on-accent', 'accent-pressed', TEXT],
  ...['allow', 'ask', 'deny', 'default', 'critical'].flatMap((d): [string, string, number][] => [
    [`${d}-fg`, `${d}-bg`, TEXT],
    [`${d}-fg`, 'surface', TEXT],
    ['on-decision', `${d}-solid`, TEXT],
  ]),
  ...['info', 'warning', 'danger'].map((f): [string, string, number] => [`${f}-fg`, `${f}-bg`, TEXT]),
  ['positive-fg', 'positive-bg', TEXT],
  ['danger-fg', 'surface', TEXT],
  ['danger-fg', 'bg', TEXT],
  ['on-danger', 'danger-solid', TEXT],
  ['on-danger', 'danger-solid-hover', TEXT],
  ['border-strong', 'surface', NON_TEXT],
  ['border-strong', 'bg', NON_TEXT],
  ['default-border', 'surface', NON_TEXT],
  ['focus', 'surface', NON_TEXT],
  ['focus', 'bg', NON_TEXT],
  ['accent', 'surface', NON_TEXT],
  ['danger-fg', 'surface', NON_TEXT],
];

describe('tokens.css schemes', () => {
  it('forces each scheme together with its color-scheme', () => {
    expect(declarations(body(':root')).get('color-scheme')).toBe('light dark');
    expect(declarations(body('[data-hm-theme="light"]')).get('color-scheme')).toBe('light');
    expect(declarations(body('[data-hm-theme="dark"]')).get('color-scheme')).toBe('dark');
  });

  it('gives forced dark exactly the values of the system dark scheme', () => {
    expect(colours(DARK)).toEqual(colours(SYSTEM_DARK));
  });

  it('defines every colour token in both schemes', () => {
    expect([...colours(DARK).keys()].sort()).toEqual([...colours(LIGHT).keys()].sort());
    expect(colours(LIGHT).size).toBeGreaterThan(50);
  });

  for (const [scheme, tokens] of [
    ['light', LIGHT],
    ['dark', DARK],
  ] as const) {
    it.each(PAIRS)(`${scheme}: %s on %s reaches %d:1`, (fg, bg, min) => {
      const a = tokens.get(`--hm-color-${fg}`);
      const b = tokens.get(`--hm-color-${bg}`);
      expect(a, fg).toMatch(/^#[0-9a-f]{6}$/i);
      expect(b, bg).toMatch(/^#[0-9a-f]{6}$/i);
      expect(contrast(a!, b!)).toBeGreaterThanOrEqual(min);
    });
  }
});

describe('contrast', () => {
  it('matches the WCAG formula at its ends', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 5);
    expect(contrast('#777777', '#777777')).toBe(1);
  });
});
