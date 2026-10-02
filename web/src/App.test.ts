// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import App from './App.svelte';
import { setLocale } from './lib/paraglide/runtime.js';

afterEach(() => {
  cleanup();
  window.location.hash = '';
});

describe('App', () => {
  it('shows the overview in English', () => {
    setLocale('en', { reload: false });
    render(App);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Overview');
    expect(screen.getByRole('navigation', { name: 'Sections' })).toBeTruthy();
  });

  it('shows the overview in German', () => {
    setLocale('de', { reload: false });
    render(App);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Übersicht');
  });

  it('shows a not-found page for unknown routes and follows hash changes', async () => {
    setLocale('en', { reload: false });
    window.location.hash = '#/nowhere';
    render(App);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Page not found');

    window.location.hash = '#/';
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await Promise.resolve();
    expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe('Overview');
  });
});
