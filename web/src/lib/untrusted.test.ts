// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { cleanUntrusted, escapeUntrusted, hasVisibleText, isolate, UNTRUSTED_MAX } from './untrusted.ts';

describe('cleanUntrusted', () => {
  it('keeps ordinary text, umlauts, emoji and right-to-left script', () => {
    expect(cleanUntrusted('Küche 💡 مرحبا')).toBe('Küche 💡 مرحبا');
  });

  it.each([
    ['bidi overrides and embeddings', 'a\u202Ab\u202Bc\u202Cd\u202De\u202Ef', 'abcdef'],
    ['bidi isolates', 'a\u2066b\u2067c\u2068d\u2069e', 'abcde'],
    ['direction marks', 'a\u200Eb\u200Fc\u061Cd', 'abcd'],
    ['zero-width and other format characters', 'a\u200Bb\u2060c\uFEFFd\u00ADe', 'abcde'],
    ['control characters', 'a\u0000b\u0007c\u001Bd\u007Fe\u0080f', 'abcdef'],
  ])('removes %s', (_name, input, want) => {
    expect(cleanUntrusted(input)).toBe(want);
  });

  it('turns line and paragraph breaks and tabs into single spaces and trims', () => {
    expect(cleanUntrusted('  open\r\nthe\n\ndoor\u2028now\u2029please\tthanks\u0085bye  ')).toBe('open the door now please thanks bye');
  });

  it('cuts at the limit in code points and marks the cut', () => {
    const long = 'x'.repeat(UNTRUSTED_MAX + 10);
    const out = cleanUntrusted(long);
    expect([...out]).toHaveLength(UNTRUSTED_MAX);
    expect(out.endsWith('…')).toBe(true);
    expect(cleanUntrusted('x'.repeat(UNTRUSTED_MAX))).toBe('x'.repeat(UNTRUSTED_MAX));
  });

  it('never splits a surrogate pair when cutting', () => {
    const out = cleanUntrusted('🔒'.repeat(200), 120);
    expect([...out]).toHaveLength(120);
    expect(out.slice(0, -1)).toBe('🔒'.repeat(119));
  });

  it('takes a shorter limit, e.g. 120 for push texts', () => {
    expect([...cleanUntrusted('y'.repeat(300), 120)]).toHaveLength(120);
  });

  it('returns an empty string for null and undefined', () => {
    expect(cleanUntrusted(null)).toBe('');
    expect(cleanUntrusted(undefined)).toBe('');
  });
});

describe('cleanUntrusted: look-alike blanks', () => {
  it('removes letters that render blank and variation selectors', () => {
    expect(cleanUntrusted('A\u3164B\u115F\u1160C\u2800D\uFFA0E')).toBe('ABCDE');
    expect(cleanUntrusted('x\uFE0Fy\u{E0100}z\u180Bw')).toBe('xyzw');
  });

  it('keeps at most two combining marks per character', () => {
    expect(cleanUntrusted('e\u0301\u0302\u0303\u0304x')).toBe('e\u0301\u0302x');
    expect(cleanUntrusted('Cafe\u0301')).toBe('Cafe\u0301');
  });
});

describe('hasVisibleText', () => {
  it('needs a letter or digit after cleaning', () => {
    expect(hasVisibleText('Küche')).toBe(true);
    expect(hasVisibleText('7')).toBe(true);
    expect(hasVisibleText('\u3164\u2800')).toBe(false);
    expect(hasVisibleText(' \u200B - . ')).toBe(false);
    expect(hasVisibleText(null)).toBe(false);
  });
});

describe('isolate', () => {
  it('wraps cleaned text in a directional isolate that the text cannot close', () => {
    expect(isolate('Claude Code')).toBe('\u2068Claude Code\u2069');
    expect(isolate('a\u2069\u202Eb\u2068c')).toBe('\u2068abc\u2069');
    expect(isolate(null)).toBe('\u2068\u2069');
    expect([...isolate('y'.repeat(300), 10)]).toHaveLength(12);
  });
});

describe('escapeUntrusted', () => {
  it('shows an ordinary identifier unchanged', () => {
    expect(escapeUntrusted('lock.front_door')).toBe('lock.front_door');
    expect(escapeUntrusted('a-z 0-9 "quoted" ~!')).toBe('a-z 0-9 "quoted" ~!');
  });

  it.each([
    ['a line feed', 'lock.haustuer\n2026 decision allow', 'lock.haustuer\\n2026 decision allow'],
    ['a carriage return and a tab', 'a\rb\tc', 'a\\rb\\tc'],
    ['a right-to-left override', '\u202Eunlock', '\\u202eunlock'],
    ['a terminal escape sequence', '\u001B[2Jflur', '\\u001b[2Jflur'],
    ['a backslash, so that a spelled-out escape cannot pass for one', 'a\\u202eb', 'a\\\\u202eb'],
    ['a non-ASCII letter and an emoji as UTF-16 code units', 'Küche 💡', 'K\\u00fcche \\ud83d\\udca1'],
    ['DEL and a C1 control', 'a\u007Fb\u0085c', 'a\\u007fb\\u0085c'],
  ])('escapes %s as JSON does', (_name, input, want) => {
    expect(escapeUntrusted(input)).toBe(want);
  });

  it('keeps markup as text', () => {
    expect(escapeUntrusted('<script>alert(1)</script>')).toBe('<script>alert(1)</script>');
  });

  it('cuts at the limit in code points and marks the cut', () => {
    expect(escapeUntrusted('abcdef', 3)).toBe('abc…');
    expect(escapeUntrusted('', 3)).toBe('');
  });
});
