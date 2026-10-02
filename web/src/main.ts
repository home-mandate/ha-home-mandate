// SPDX-License-Identifier: AGPL-3.0-or-later

import { mount } from 'svelte';
import App from './App.svelte';
import { baseLocale, locales, setLocale } from './lib/paraglide/runtime.js';
import { m } from './lib/i18n.ts';
import { resolveLocale } from './lib/locale.ts';
import './app.css';

// The user's setting from the Home-Mandate API joins with internal/api (week 4).
const locale = resolveLocale(undefined, navigator.languages, locales, baseLocale);
setLocale(locale, { reload: false });
document.documentElement.lang = locale;
document.title = m.app_name();

const target = document.getElementById('app');
if (!target) {
  throw new Error('missing #app element');
}
mount(App, { target });
