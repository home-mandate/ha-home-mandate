// SPDX-License-Identifier: AGPL-3.0-or-later

// The colour scheme chosen in the header (issue #12): System (default), Light or Dark. It is
// a per-browser convenience kept in localStorage, never on the server. An explicit choice
// sets data-hm-theme on the root element, which overrides the system scheme in tokens.css
// together with color-scheme; System removes it. main.ts applies the stored choice as its
// first statement, before the app renders: the CSP forbids an inline script in index.html.

export const THEMES = ['system', 'light', 'dark'] as const;
export type Theme = (typeof THEMES)[number];

export const THEME_KEY = 'hm-theme';
const ATTRIBUTE = 'data-hm-theme';

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem' | 'removeItem'>;

const forced = (value: string | null): value is 'light' | 'dark' => value === 'light' || value === 'dark';

/** browserStorage returns localStorage, or a stand-in that keeps nothing where it is blocked. */
export function browserStorage(): Storage {
  try {
    return window.localStorage;
  } catch {
    return { getItem: () => null, setItem: () => {}, removeItem: () => {} };
  }
}

/** storedTheme reads the choice; blocked storage or an unknown value means System. */
export function storedTheme(storage: Storage): Theme {
  try {
    const value = storage.getItem(THEME_KEY);
    return forced(value) ? value : 'system';
  } catch {
    return 'system';
  }
}

/** storeTheme remembers Light or Dark and forgets the key for System; blocked storage is ignored. */
export function storeTheme(theme: Theme, storage: Storage): void {
  try {
    if (theme === 'system') storage.removeItem(THEME_KEY);
    else storage.setItem(THEME_KEY, theme);
  } catch {
    // The choice then lasts until the page is reloaded.
  }
}

/** applyTheme sets the attribute for Light or Dark on root and removes it for System. */
export function applyTheme(theme: Theme, root: HTMLElement): void {
  if (theme === 'system') root.removeAttribute(ATTRIBUTE);
  else root.setAttribute(ATTRIBUTE, theme);
}

/** appliedTheme reads the theme root shows now. */
export function appliedTheme(root: HTMLElement): Theme {
  const value = root.getAttribute(ATTRIBUTE);
  return forced(value) ? value : 'system';
}

/** nextTheme is the theme after theme in the cycle System → Light → Dark → System. */
export function nextTheme(theme: Theme): Theme {
  return THEMES[(THEMES.indexOf(theme) + 1) % THEMES.length]!;
}
