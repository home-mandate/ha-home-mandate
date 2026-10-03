// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { m, PSEUDO, pseudo, pseudoMessages } from './i18n.ts';

describe('pseudo', () => {
  it('accents letters, keeps digits and punctuation, and lengthens by about 40 %', () => {
    const out = pseudo('Overview');
    expect(out).toBe('[Övérvïéw ~]');
    const long = pseudo('Mandates for your AI agents and more');
    expect(long.length).toBeGreaterThanOrEqual(Math.round('Mandates for your AI agents and more'.length * 1.4));
    expect(pseudo('12:30, 3 items')).toMatch(/^\[12:30, 3 ïtéms ~+\]$/);
  });

  it('pads in word-sized pieces, so long texts can still wrap like real ones', () => {
    const text = 'Home-Mandate checks every action of your agents against the mandate before it reaches Home Assistant.';
    const padding = pseudo(text).slice(text.length + 2, -1);
    expect(padding.replace(/ /g, '').length).toBe(Math.round(text.length * 0.4) - 2);
    expect(Math.max(...padding.split(' ').map((w) => w.length))).toBeLessThanOrEqual(8);
  });

  it('marks even empty text, so missing padding is visible', () => {
    expect(pseudo('')).toBe('[ ~]');
  });
});

describe('pseudoMessages', () => {
  it('wraps message functions and passes the inputs through', () => {
    const source = { greet: (i: { name: string }) => `Hello ${i.name}`, plain: () => 'Back' };
    const wrapped = pseudoMessages(source);
    expect(wrapped.greet({ name: 'Ann' })).toBe(pseudo('Hello Ann'));
    expect(wrapped.plain()).toBe(pseudo('Back'));
  });

  it('leaves non-function members alone', () => {
    const source = { version: 3 } as unknown as Record<string, () => string>;
    expect((pseudoMessages(source) as unknown as { version: number }).version).toBe(3);
  });
});

describe('m', () => {
  it('is the plain catalog outside the pseudo build', () => {
    expect(PSEUDO).toBe(false);
    expect(m.app_name()).toBe('Home-Mandate');
  });
});
