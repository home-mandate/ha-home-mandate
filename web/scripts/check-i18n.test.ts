// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { checkMessages, compareKeys, comparePlaceholders, compareUsage, findHardcodedText, findPhysicalCss, run, usedKeys } from './check-i18n.ts';

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
  it('reads ICU arguments, also inside plural and select cases', () => {
    const en = { a: '{count, plural, one {{count, number} agent of {total}} other {{count, number} agents}}' };
    const de = { a: '{count, plural, one {{count, number} Agent} other {{count, number} Agenten von {gesamt}}}' };
    expect(comparePlaceholders({ en, de })).toEqual(['de: "a" has placeholders {count}, {gesamt}, en has {count}, {total}']);
    expect(comparePlaceholders({ en: { s: '{kind, select, a {{x}} other {}}' }, de: { s: '{kind, select, a {} other {{x}}}' } })).toEqual([]);
  });
  it('handles no catalogs', () => {
    expect(comparePlaceholders({})).toEqual([]);
  });
});

describe('checkMessages (ICU MessageFormat)', () => {
  it('accepts plain text, arguments, plurals with other and number formatting', () => {
    expect(
      checkMessages('en', {
        a: 'Plain',
        b: 'Hello {name}',
        c: '{count, plural, one {{count, number} entry} other {{count, number} entries}}',
        d: "It''s quoted",
      }),
    ).toEqual([]);
  });

  it('reports syntax errors, plurals without other and the unformatted #', () => {
    expect(
      checkMessages('de', {
        broken: 'Hallo {name',
        noOther: '{count, plural, one {eins}}',
        pound: '{count, plural, one {# Eintrag} other {# Einträge}}',
        notString: 42,
      }),
    ).toEqual([
      'de: "broken" is not valid ICU MessageFormat',
      'de: "noOther" has a plural or select without "other"',
      'de: "pound" uses #; write {count, number} so the number is formatted for the locale',
      'de: "notString" is not a string',
    ]);
  });
});

describe('usedKeys and compareUsage', () => {
  it('finds m.key() and m["key"] references', () => {
    expect(usedKeys('m.home_heading() + m.a_b ( ) + m["x-y"]')).toEqual(['home_heading', 'a_b', 'x-y']);
  });
  it('ignores other objects named like m', () => {
    expect(usedKeys('foo.m.bar() + item.x() + $m.y()')).toEqual([]);
  });
  it('accepts unused keys only while they are pending, and pending keys only while unused', () => {
    const catalog = { used: 'U', waiting: 'W', forgotten: 'F', done: 'D' };
    const sources = { 'a.svelte': 'm.used() m.done()' };
    expect(compareUsage(catalog, sources, ['waiting', 'done', 'gone'])).toEqual([
      'orphaned key "forgotten"',
      'pending key "done" is used now; remove it from scripts/i18n-pending.json',
      'pending key "gone" is not in the catalog',
    ]);
  });

  it('reports orphaned and unknown keys', () => {
    const problems = compareUsage({ used: 'U', orphan: 'O' }, { 'App.svelte': '{m.used()} {m.missing()}' });
    expect(problems).toEqual(['orphaned key "orphan"', 'App.svelte: unknown key "missing"']);
  });
});

describe('findHardcodedText', () => {
  const check = (source: string) => findHardcodedText(source, 'X.svelte');

  it('accepts text from catalogs, punctuation, conditions, classes and non-text attributes', () => {
    const source = [
      '<script>let x = "ignored in scripts";</script>',
      '<p>{m.a()}</p> · <span>{x}: 42</span>',
      '<p>{m.release({ date: format(d, "de") })}</p>',
      '{#if role === "admin"}<b class={on ? "active" : "idle"}>{m.b()}</b>{/if}',
      '<a href="#/agents" rel="noopener" target="_self">{m.c()}</a>',
      '<input type="text" value="ignored" autocomplete="off" />',
      '<Button variant="primary" label={m.d()} />',
      '<style>p::after { content: "x"; }</style>',
    ].join('\n');
    expect(check(source)).toEqual([]);
  });

  it('reports text nodes in markup and blocks', () => {
    expect(check('<h1>Hello</h1>{#if a}<p>Größe</p>{/if}{#each xs as x}<li>Item</li>{/each}')).toEqual([
      'X.svelte: hard-coded text "Hello"',
      'X.svelte: hard-coded text "Größe"',
      'X.svelte: hard-coded text "Item"',
    ]);
  });

  it('reports string literals in expressions', () => {
    expect(check("<p>{'Hello'}</p><p>{`Hi ${name}`}</p><p>{ok ? 'Yes' : 'No'}</p>")).toEqual([
      'X.svelte: hard-coded text "Hello"',
      'X.svelte: hard-coded text "Hi"',
      'X.svelte: hard-coded text "Yes"',
      'X.svelte: hard-coded text "No"',
    ]);
    expect(check('{@const x = "Hello"}<p>{x}</p>')).toEqual(['X.svelte: hard-coded text "Hello"']);
    expect(check('{@render row("Hello")}')).toEqual(['X.svelte: hard-coded text "Hello"']);
  });

  it('reports text attributes, static or as expression', () => {
    expect(
      check('<img alt="Logo" src="x.svg" /><button aria-label={"Close"} class="big">{m.x()}</button>'),
    ).toEqual(['X.svelte: hard-coded alt "Logo"', 'X.svelte: hard-coded aria-label "Close"']);
    expect(check('<div role="slider" aria-valuetext="Half" aria-roledescription="Dial"></div>')).toHaveLength(2);
    expect(check('<input type="submit" value="Send" />')).toEqual(['X.svelte: hard-coded value "Send"']);
  });

  it('reports text props on components', () => {
    expect(check('<Card heading="Hello" text={"World"} variant="primary" />')).toEqual([
      'X.svelte: hard-coded heading "Hello"',
      'X.svelte: hard-coded text "World"',
    ]);
  });

  it('reports text in svelte:head', () => {
    expect(check('<svelte:head><title>Home</title></svelte:head>')).toEqual(['X.svelte: hard-coded text "Home"']);
  });
});

describe('findPhysicalCss', () => {
  it('accepts logical properties', () => {
    expect(findPhysicalCss('p { margin-inline-start: 1rem; inset-inline-end: 0; text-align: start; }', 'a.css')).toEqual([]);
  });
  it('reports physical properties outside comments', () => {
    const css = '/* margin-left: 0 */ p { margin-left: 1rem; padding-right:0; border-left-width: 1px; left: 0; text-align: right; float: left }';
    expect(findPhysicalCss(css, 'a.css')).toEqual([
      'a.css: physical CSS "margin-left", use a logical property',
      'a.css: physical CSS "padding-right", use a logical property',
      'a.css: physical CSS "border-left-width", use a logical property',
      'a.css: physical CSS "left", use a logical property',
      'a.css: physical CSS "text-align: right", use a logical property',
      'a.css: physical CSS "float: left", use a logical property',
    ]);
  });
});

describe('run', () => {
  it('passes on this project', () => {
    expect(run(process.cwd())).toEqual([]);
  });
});
