// SPDX-License-Identifier: AGPL-3.0-or-later

import { mount } from 'svelte';
import App from './App.svelte';
import { createHttpClient, type ApiClient } from './lib/api/client.ts';
import { adoptLanguage, storedLanguage } from './lib/app/language.ts';
import { AppState } from './lib/app/state.svelte.ts';
import { m } from './lib/i18n.ts';
import { resolveLocale } from './lib/locale.ts';
import { baseLocale, getLocale, locales, setLocale } from './lib/paraglide/runtime.js';
import './app.css';

/** localStorage, or a stand-in where it is blocked (private mode, sandboxed iframe). */
function storage(): Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> {
  try {
    return window.localStorage;
  } catch {
    return { getItem: () => null, setItem: () => {}, removeItem: () => {} };
  }
}

// The dev server and the test builds run against the mock client; the release build never
// contains it (VITE_MOCK is only set in .env.development, .env.mock and .env.pseudo).
async function client(): Promise<ApiClient> {
  if (import.meta.env.VITE_MOCK === '1') {
    const { createMockClient } = await import('./lib/api/mock.ts');
    // Playwright sets hmMockOptions before the page loads to start in a given state.
    const mock = createMockClient(window.hmMockOptions ?? {});
    window.hmMock = mock.control;
    return mock;
  }
  return createHttpClient();
}

const locale = resolveLocale(storedLanguage(storage()), navigator.languages, locales, baseLocale);
setLocale(locale, { reload: false });
document.documentElement.lang = locale;
document.title = m.app_name();

const target = document.getElementById('app');
if (!target) {
  throw new Error('missing #app element');
}

const app = new AppState(await client());
mount(App, { target, props: { app } });
await app.start();
if (app.session && adoptLanguage(app.session.language, getLocale(), storage())) {
  window.location.reload();
}
