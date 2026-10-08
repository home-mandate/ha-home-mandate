// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { applyTheme, appliedTheme, browserStorage, nextTheme, storedTheme, storeTheme, THEME_KEY, THEMES, type Theme } from './theme.ts';

function memory(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
    data,
  };
}

const blocked = () => {
  throw new Error('blocked');
};
const broken = { getItem: blocked, setItem: blocked, removeItem: blocked };

describe('storedTheme', () => {
  it.each([
    ['light', 'light'],
    ['dark', 'dark'],
  ] as const)('reads a stored %s', (value, want) => {
    expect(storedTheme(memory({ [THEME_KEY]: value }))).toBe(want);
  });

  it.each(['', 'system', 'Dark', 'blue', ' dark', '{"theme":"dark"}'])('ignores the unknown value %j and falls back to system', (value) => {
    expect(storedTheme(memory({ [THEME_KEY]: value }))).toBe('system');
  });

  it('falls back to system without a stored choice', () => {
    expect(storedTheme(memory())).toBe('system');
  });

  it('falls back to system when storage is blocked', () => {
    expect(storedTheme(broken)).toBe('system');
  });
});

describe('storeTheme', () => {
  it('writes light and dark under its one key', () => {
    const storage = memory();
    storeTheme('dark', storage);
    expect([...storage.data]).toEqual([[THEME_KEY, 'dark']]);
    storeTheme('light', storage);
    expect([...storage.data]).toEqual([[THEME_KEY, 'light']]);
  });

  it('forgets the choice for system instead of storing it', () => {
    const storage = memory({ [THEME_KEY]: 'dark', other: 'kept' });
    storeTheme('system', storage);
    expect([...storage.data]).toEqual([['other', 'kept']]);
  });

  it('does not throw when storage is blocked', () => {
    for (const theme of THEMES) expect(() => storeTheme(theme, broken)).not.toThrow();
  });

  it('round-trips every theme', () => {
    const storage = memory();
    for (const theme of THEMES) {
      storeTheme(theme, storage);
      expect(storedTheme(storage)).toBe(theme);
    }
  });
});

describe('applyTheme', () => {
  it('sets the attribute for light and dark and removes it for system', () => {
    const root = document.createElement('html');
    applyTheme('dark', root);
    expect(root.getAttribute('data-hm-theme')).toBe('dark');
    expect(appliedTheme(root)).toBe('dark');
    applyTheme('light', root);
    expect(root.getAttribute('data-hm-theme')).toBe('light');
    expect(appliedTheme(root)).toBe('light');
    applyTheme('system', root);
    expect(root.hasAttribute('data-hm-theme')).toBe(false);
    expect(appliedTheme(root)).toBe('system');
  });

  it('never writes an inline style (the CSP forbids inline styles)', () => {
    const root = document.createElement('html');
    for (const theme of THEMES) {
      applyTheme(theme, root);
      expect(root.hasAttribute('style')).toBe(false);
    }
  });

  it('reads a foreign attribute value as system', () => {
    const root = document.createElement('html');
    root.setAttribute('data-hm-theme', 'sepia');
    expect(appliedTheme(root)).toBe('system');
  });
});

describe('nextTheme', () => {
  it('cycles system → light → dark → system', () => {
    const seen: Theme[] = [];
    let theme: Theme = 'system';
    for (let i = 0; i < THEMES.length; i++) {
      theme = nextTheme(theme);
      seen.push(theme);
    }
    expect(seen).toEqual(['light', 'dark', 'system']);
  });
});

describe('browserStorage', () => {
  it('returns localStorage where it is available', () => {
    expect(browserStorage()).toBe(window.localStorage);
  });

  it('returns a stand-in that keeps nothing where localStorage throws', () => {
    const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage');
    Object.defineProperty(window, 'localStorage', { configurable: true, get: blocked });
    try {
      const storage = browserStorage();
      storage.setItem(THEME_KEY, 'dark');
      expect(storage.getItem(THEME_KEY)).toBeNull();
      expect(() => storage.removeItem(THEME_KEY)).not.toThrow();
    } finally {
      if (descriptor) Object.defineProperty(window, 'localStorage', descriptor);
    }
  });
});
