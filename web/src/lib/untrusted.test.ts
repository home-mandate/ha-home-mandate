// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { cleanUntrusted, isolate, UNTRUSTED_MAX } from './untrusted.ts';

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

describe('isolate', () => {
  it('wraps cleaned text in a directional isolate that the text cannot close', () => {
    expect(isolate('Claude Code')).toBe('\u2068Claude Code\u2069');
    expect(isolate('a\u2069\u202Eb\u2068c')).toBe('\u2068abc\u2069');
    expect(isolate(null)).toBe('\u2068\u2069');
    expect([...isolate('y'.repeat(300), 10)]).toHaveLength(12);
  });
});
