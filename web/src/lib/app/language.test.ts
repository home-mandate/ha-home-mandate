// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { adoptLanguage, storedLanguage } from './language.ts';

function memory(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
    data,
  };
}

const broken = {
  getItem: () => {
    throw new Error('blocked');
  },
  setItem: () => {
    throw new Error('blocked');
  },
  removeItem: () => {
    throw new Error('blocked');
  },
};

describe('storedLanguage', () => {
  it('returns a remembered de or en, nothing else', () => {
    expect(storedLanguage(memory({ 'hm-language': 'de' }))).toBe('de');
    expect(storedLanguage(memory({ 'hm-language': 'fr' }))).toBeUndefined();
    expect(storedLanguage(memory())).toBeUndefined();
    expect(storedLanguage(broken)).toBeUndefined();
  });
});

describe('adoptLanguage', () => {
  it('remembers the setting and asks for a reload when it differs from the current locale', () => {
    const storage = memory();
    expect(adoptLanguage('de', 'en', storage)).toBe(true);
    expect(storage.data.get('hm-language')).toBe('de');
    expect(adoptLanguage('de', 'de', storage)).toBe(false);
  });

  it('forgets the setting for "automatic" without a reload of its own', () => {
    const storage = memory({ 'hm-language': 'de' });
    expect(adoptLanguage(null, 'de', storage)).toBe(false);
    expect(storage.data.has('hm-language')).toBe(false);
  });

  it('never reloads when storage is blocked (it would loop)', () => {
    expect(adoptLanguage('de', 'en', broken)).toBe(false);
  });

  it('never reloads when storage silently drops writes (stand-in in a sandboxed iframe)', () => {
    const dropping = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
    expect(adoptLanguage('de', 'en', dropping)).toBe(false);
  });
});
