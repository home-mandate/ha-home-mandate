// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { compareKeys, comparePlaceholders, compareUsage, findHardcodedText, run, usedKeys } from './check-i18n.ts';

describe('compareKeys', () => {
  it('accepts identical key sets', () => {
    expect(compareKeys({ en: { a: 'A', b: 'B' }, de: { b: 'B', a: 'A' } })).toEqual([]);
  });
  it('reports keys missing in any catalog', () => {
    expect(compareKeys({ en: { a: 'A', b: 'B' }, de: { a: 'A', c: 'C' } })).toEqual([
      'en: missing key "c"',
      'de: missing key "b"',
    ]);
  });
  it('ignores $schema', () => {
    expect(compareKeys({ en: { $schema: 'x', a: 'A' }, de: { a: 'A' } })).toEqual([]);
  });
});

describe('comparePlaceholders', () => {
  it('accepts the same placeholders in any order', () => {
    expect(comparePlaceholders({ en: { a: '{x} and {y}' }, de: { a: '{y} und {x}' } })).toEqual([]);
  });
  it('reports renamed or missing placeholders', () => {
    expect(comparePlaceholders({ en: { a: 'at {date}' }, de: { a: 'am {datum}' } })).toEqual([
      'de: "a" has placeholders {datum}, en has {date}',
    ]);
    expect(comparePlaceholders({ en: { a: 'at {date}' }, de: { a: 'am' } })).toHaveLength(1);
  });
  it('looks into structured (plural) messages', () => {
    const en = { a: [{ match: { 'n=one': '{n} agent', 'n=other': '{n} agents' } }] };
    const de = { a: [{ match: { 'n=one': '{n} Agent', 'n=other': '{count} Agenten' } }] };
    expect(comparePlaceholders({ en, de })).toHaveLength(1);
  });
  it('handles no catalogs', () => {
    expect(comparePlaceholders({})).toEqual([]);
  });
});

describe('usedKeys and compareUsage', () => {
  it('finds m.key() and m["key"] references', () => {
    expect(usedKeys('m.home_heading() + m.a_b ( ) + m["x-y"]')).toEqual(['home_heading', 'a_b', 'x-y']);
  });
  it('reports orphaned and unknown keys', () => {
    const problems = compareUsage({ used: 'U', orphan: 'O' }, { 'App.svelte': '{m.used()} {m.missing()}' });
    expect(problems).toEqual(['orphaned key "orphan"', 'App.svelte: unknown key "missing"']);
  });
});

describe('findHardcodedText', () => {
  it('accepts text from catalogs, punctuation and whitespace', () => {
    const source = '<script>let x = "ignored in scripts";</script>\n<p>{m.a()}</p> · <span>{x}: 42</span>\n<style>p::after { content: "x"; }</style>';
    expect(findHardcodedText(source, 'Ok.svelte')).toEqual([]);
  });
  it('reports visible text in markup, blocks and visible attributes', () => {
    const source = '<h1>Hello</h1>{#if a}<p>Größe</p>{/if}<img alt="Logo" src="x.svg" /><button aria-label="Close" class="big">{m.x()}</button>';
    expect(findHardcodedText(source, 'Bad.svelte')).toEqual([
      'Bad.svelte: hard-coded text "Hello"',
      'Bad.svelte: hard-coded text "Größe"',
      'Bad.svelte: hard-coded alt "Logo"',
      'Bad.svelte: hard-coded aria-label "Close"',
    ]);
  });
});

describe('run', () => {
  it('passes on this project', () => {
    expect(run(process.cwd())).toEqual([]);
  });
});
