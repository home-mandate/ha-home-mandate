// SPDX-License-Identifier: AGPL-3.0-or-later

// Sentences from the catalog with a component in them (e.g. the agent's name with its
// "unverified" mark): the message is formatted with MARK in place of that part and split
// around it, so every language keeps its own word order. MARK is a control character;
// cleanUntrusted removes those from every foreign text, so a name cannot fake it.

export const MARK = '\u0001';
/** Second part of a sentence, e.g. the device next to the agent. */
export const MARK2 = '\u0002';

/** around splits text at its single MARK; otherwise all text is "before", without marks. */
export function around(text: string): [string, string] {
  const parts = text.split(MARK);
  if (parts.length !== 2) return [parts.join(''), ''];
  return [parts[0] ?? '', parts[1] ?? ''];
}

export type Piece = { text: string } | { slot: 1 | 2 };

/** pieces splits text at MARK (slot 1) and MARK2 (slot 2), in whatever order the language puts them. */
export function pieces(text: string): Piece[] {
  const out: Piece[] = [];
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    const slot = text[i] === MARK ? 1 : text[i] === MARK2 ? 2 : 0;
    if (slot === 0) continue;
    if (i > start) out.push({ text: text.slice(start, i) });
    out.push({ slot });
    start = i + 1;
  }
  if (start < text.length) out.push({ text: text.slice(start) });
  return out;
}
