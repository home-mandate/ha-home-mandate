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
// Letters that render blank (Hangul fillers, braille blank) and variation selectors: they
// make a name look empty or different without being Cc/Cf.
// (An alternation, not a class: Hangul fillers and selectors would combine inside a class.)
const BLANK = /\u115F|\u1160|\u3164|\uFFA0|\u2800|[\u180B-\u180F]|[\uFE00-\uFE0F]|[\u{E0100}-\u{E01EF}]/gu;
// More than two combining marks on one character ("Zalgo") only serve to overflow the layout.
const STACKED = /(\p{M}{2})\p{M}+/gu;
const VISIBLE = /[\p{L}\p{N}]/u;

export function cleanUntrusted(text: string | null | undefined, max = UNTRUSTED_MAX): string {
  if (!text) return '';
  const flat = text
    .replace(/[\r\n\t\u0085\u2028\u2029]/g, ' ')
    .replace(HIDDEN, '')
    .replace(BLANK, '')
    .replace(STACKED, '$1')
    .replace(BREAKS, ' ')
    .trim();
  const chars = [...flat];
  return chars.length <= max ? flat : `${chars.slice(0, max - 1).join('')}…`;
}

/**
 * isolate cleans untrusted text for use inside a sentence (a message placeholder, an
 * aria-label), where <bdi> is not possible: FIRST STRONG ISOLATE … POP DIRECTIONAL ISOLATE
 * keep a right-to-left name from reordering the words around it. cleanUntrusted removes
 * these characters from the text itself, so it cannot close the isolate early.
 */
export function isolate(text: string | null | undefined, max = UNTRUSTED_MAX): string {
  return `\u2068${cleanUntrusted(text, max)}\u2069`;
}

/** hasVisibleText tells whether cleaned text still holds a letter or digit (a name, not only marks or punctuation). */
export function hasVisibleText(text: string | null | undefined): boolean {
  return VISIBLE.test(cleanUntrusted(text));
}
