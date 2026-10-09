// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { setLocale } from '../paraglide/runtime.js';
import { THEME_KEY } from '../app/theme.ts';
import ThemeSwitch from './ThemeSwitch.svelte';

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

let root: HTMLElement;

beforeEach(() => {
  setLocale('en', { reload: false });
  root = document.createElement('html');
});
afterEach(cleanup);

const live = (c: HTMLElement) => c.querySelector('[aria-live="polite"]') as HTMLElement;

describe('ThemeSwitch', () => {
  it('is a real button that names the current scheme and shows it', () => {
    const { container } = render(ThemeSwitch, { root, storage: memory() });
    const button = screen.getByRole('button', { name: 'Colour scheme: System' });
    expect(button.tagName).toBe('BUTTON');
    expect(button.getAttribute('type')).toBe('button');
    expect(button.textContent?.trim()).toBe('System');
    // Nothing is said before the user changes anything.
    expect(live(container).textContent).toBe('');
  });

  it('starts from the theme the root element shows (applied by main.ts)', () => {
    root.setAttribute('data-hm-theme', 'dark');
    render(ThemeSwitch, { root, storage: memory({ [THEME_KEY]: 'dark' }) });
    expect(screen.getByRole('button', { name: 'Colour scheme: Dark' })).toBeTruthy();
  });

  it('cycles System → Light → Dark → System, applies and stores each choice and announces it', async () => {
    const storage = memory();
    const { container } = render(ThemeSwitch, { root, storage });
    const button = screen.getByRole('button');

    await fireEvent.click(button);
    await tick();
    expect(button.getAttribute('aria-label')).toBe('Colour scheme: Light');
    expect(button.textContent?.trim()).toBe('Light');
    expect(root.getAttribute('data-hm-theme')).toBe('light');
    expect(storage.data.get(THEME_KEY)).toBe('light');
    expect(live(container).textContent).toBe('Colour scheme: Light');

    await fireEvent.click(button);
    await tick();
    expect(button.getAttribute('aria-label')).toBe('Colour scheme: Dark');
    expect(root.getAttribute('data-hm-theme')).toBe('dark');
    expect(storage.data.get(THEME_KEY)).toBe('dark');
    expect(live(container).textContent).toBe('Colour scheme: Dark');

    await fireEvent.click(button);
    await tick();
    expect(button.getAttribute('aria-label')).toBe('Colour scheme: System');
    expect(root.hasAttribute('data-hm-theme')).toBe(false);
    expect(storage.data.has(THEME_KEY)).toBe(false);
    expect(live(container).textContent).toBe('Colour scheme: System');
  });

  it('still switches the scheme when storage is blocked', async () => {
    render(ThemeSwitch, { root, storage: { getItem: blocked, setItem: blocked, removeItem: blocked } });
    await fireEvent.click(screen.getByRole('button'));
    expect(root.getAttribute('data-hm-theme')).toBe('light');
    expect(screen.getByRole('button', { name: 'Colour scheme: Light' })).toBeTruthy();
  });

  it('works with the keyboard like any button', async () => {
    render(ThemeSwitch, { root, storage: memory() });
    const button = screen.getByRole('button');
    button.focus();
    expect(document.activeElement).toBe(button);
    // A native button turns Enter and Space into a click; jsdom does not, so the click stands in.
    await fireEvent.click(button);
    expect(document.activeElement).toBe(button);
  });

  it('takes its texts from the catalog of the current language', () => {
    setLocale('de', { reload: false });
    root.setAttribute('data-hm-theme', 'light');
    render(ThemeSwitch, { root, storage: memory() });
    const button = screen.getByRole('button', { name: 'Farbschema: Hell' });
    expect(button.textContent?.trim()).toBe('Hell');
  });

  it('defaults to the document root and the browser storage', async () => {
    window.localStorage.removeItem(THEME_KEY);
    render(ThemeSwitch);
    await fireEvent.click(screen.getByRole('button'));
    expect(document.documentElement.getAttribute('data-hm-theme')).toBe('light');
    expect(window.localStorage.getItem(THEME_KEY)).toBe('light');
    document.documentElement.removeAttribute('data-hm-theme');
    window.localStorage.removeItem(THEME_KEY);
  });
});
