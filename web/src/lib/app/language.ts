// SPDX-License-Identifier: AGPL-3.0-or-later

// The user's language setting in Home-Mandate (README section 9 order: setting → browser →
// English). Paraglide messages are not reactive, so a setting that differs from the locale
// the page started with is remembered locally and the page reloads once; the next start
// uses it right away. Without storage there is no reload (it would repeat forever).

import type { Language } from '../api/types.ts';

const KEY = 'hm-language';

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem' | 'removeItem'>;

export function storedLanguage(storage: Storage): Language | undefined {
  try {
    const value = storage.getItem(KEY);
    return value === 'de' || value === 'en' ? value : undefined;
  } catch {
    return undefined;
  }
}

/** adoptLanguage stores the setting; true if the page must reload to show it. */
export function adoptLanguage(setting: Language | null, current: string, storage: Storage): boolean {
  try {
    if (setting === null) {
      storage.removeItem(KEY);
      return false;
    }
    storage.setItem(KEY, setting);
    // Only reload if the next start will really see the setting; otherwise it would loop.
    if (storage.getItem(KEY) !== setting) return false;
  } catch {
    return false;
  }
  return setting !== current;
}
