// SPDX-License-Identifier: AGPL-3.0-or-later

// Sentences from the catalog with a component in them (e.g. the agent's name with its
// "unverified" mark): the message is formatted with MARK in place of that part and split
// around it, so every language keeps its own word order. MARK is a control character;
// cleanUntrusted removes those from every foreign text, so a name cannot fake it.

export const MARK = '\u0001';

/** around splits text at its single MARK; otherwise all text is "before", without marks. */
export function around(text: string): [string, string] {
  const parts = text.split(MARK);
  if (parts.length !== 2) return [parts.join(''), ''];
  return [parts[0] ?? '', parts[1] ?? ''];
}
