// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { resolveLocale } from './locale.ts';

const available = ['en', 'de'] as const;

describe('resolveLocale', () => {
  it.each([
    ['setting wins over the browser', 'de', ['en-US'], 'de'],
    ['setting with region', 'de-AT', ['en-US'], 'de'],
    ['unknown setting falls through to the browser', 'fr', ['de-DE'], 'de'],
    ['browser primary subtag', undefined, ['de-CH', 'en'], 'de'],
    ['browser exact tag, case-insensitive', undefined, ['EN'], 'en'],
    ['first supported browser language', undefined, ['fr-FR', 'de', 'en'], 'de'],
    ['fallback when nothing matches', undefined, ['fr-FR', 'ja'], 'en'],
    ['fallback without browser languages', undefined, [], 'en'],
    ['empty setting is ignored', '', ['de'], 'de'],
  ])('%s', (_name, setting, browser, want) => {
    expect(resolveLocale(setting, browser, available, 'en')).toBe(want);
  });
});
