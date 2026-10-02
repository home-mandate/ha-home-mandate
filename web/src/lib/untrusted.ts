// SPDX-License-Identifier: AGPL-3.0-or-later

// Text that agents choose (names, the reason of an approval request) or that comes from
// Home Assistant entities is untrusted (design README section 7). Before display it loses
// control, format and bidi characters (no reordering tricks such as "\u202Egnalnegrom"),
// its line breaks, and its length beyond the limit. Display still goes through Svelte's
// escaping and <bdi>, and it is always labelled as the agent's claim.

/** Longest untrusted text in the UI; push notifications use 120. */
export const UNTRUSTED_MAX = 500;

// Cc (controls, incl. C1) and Cf (format: bidi marks, overrides, isolates, zero-width,
// soft hyphen, BOM) are removed; line/paragraph separators and whitespace runs become one space.
const HIDDEN = /[\p{Cc}\p{Cf}]/gu;
const BREAKS = /[\s\u2028\u2029]+/gu;

export function cleanUntrusted(text: string | null | undefined, max = UNTRUSTED_MAX): string {
  if (!text) return '';
  const flat = text.replace(/[\r\n\t\u0085\u2028\u2029]/g, ' ').replace(HIDDEN, '').replace(BREAKS, ' ').trim();
  const chars = [...flat];
  return chars.length <= max ? flat : `${chars.slice(0, max - 1).join('')}…`;
}
