// SPDX-License-Identifier: AGPL-3.0-or-later

/**
 * safeLink returns value as an absolute http(s) URL fit for href, or null. URLs from the
 * server (pairing link, client metadata URLs chosen by agents) are untrusted: the UI is
 * same-origin with the Home Assistant frontend, so a javascript: link would run there.
 */
export function safeLink(value: string | null): string | null {
  if (!value || value !== value.trim()) return null;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return null; // relative or malformed
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return null;
  if (url.username !== '' || url.password !== '') return null;
  return url.href;
}
