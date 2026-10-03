// SPDX-License-Identifier: AGPL-3.0-or-later

// Message access for all components: import { m } from '$lib/i18n.ts', never from the
// generated Paraglide files directly. In the test build (vite build --mode pseudo) every
// message is pseudo-localized: accented and about 40 % longer, so Playwright can find
// truncated or overflowing text (docs/TESTING.md section 5). The release build never
// contains the pseudo variant's effect.

import { m as messages } from './paraglide/messages.js';

export const PSEUDO = import.meta.env.MODE === 'pseudo';

const ACCENTS: Readonly<Record<string, string>> = {
  a: 'á', e: 'é', i: 'ï', o: 'ö', u: 'ü', c: 'ç', n: 'ñ', y: 'ý',
  A: 'Å', E: 'É', I: 'Î', O: 'Ö', U: 'Ü', C: 'Ç', N: 'Ñ',
};
const LENGTHEN = 0.4;
/** Longest padding piece: real languages wrap between words, so one huge "word" would only test the padding. */
const PIECE = 8;

/** pseudo returns text accented and padded the way the design's pseudoStr does, padding in word-sized pieces. */
export function pseudo(text: string): string {
  const accented = text.replace(/[A-Za-z]/g, (ch) => ACCENTS[ch] ?? ch);
  const pad = Math.max(1, Math.round(text.length * LENGTHEN) - 2);
  const pieces = Array.from({ length: Math.ceil(pad / PIECE) }, (_, i) => '~'.repeat(Math.min(PIECE, pad - i * PIECE)));
  return `[${accented} ${pieces.join(' ')}]`;
}

/** pseudoMessages wraps every message function of a catalog with pseudo. */
export function pseudoMessages<T extends object>(source: T): T {
  return new Proxy(source, {
    get(target, key, receiver) {
      const value: unknown = Reflect.get(target, key, receiver);
      if (typeof value !== 'function') return value;
      return (...args: unknown[]) => pseudo(String((value as (...a: unknown[]) => unknown)(...args)));
    },
  });
}

export const m = PSEUDO ? pseudoMessages(messages) : messages;
