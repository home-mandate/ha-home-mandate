// SPDX-License-Identifier: AGPL-3.0-or-later

/**
 * resolveLocale picks the UI language: the user's setting in Home-Mandate, then the
 * browser languages (exact tag or primary subtag), then the fallback.
 */
export function resolveLocale<L extends string>(
  setting: string | undefined,
  browserLanguages: readonly string[],
  available: readonly L[],
  fallback: L,
): L {
  const find = (tag: string): L | undefined => {
    const lower = tag.toLowerCase();
    return (
      available.find((l) => l.toLowerCase() === lower) ??
      available.find((l) => l.toLowerCase() === lower.split('-')[0])
    );
  };
  if (setting) {
    const match = find(setting);
    if (match) return match;
  }
  for (const tag of browserLanguages) {
    const match = find(tag);
    if (match) return match;
  }
  return fallback;
}
