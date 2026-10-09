// SPDX-License-Identifier: AGPL-3.0-or-later

// Text displayed to humans (SPEC-v0 section 3.1 item 8), checked against the
// code point list of the specification instead of the Unicode tables of the browser, so
// that the editor and the server agree.

import list from './conformance/data/forbidden-codepoints-v0.json';

type Ranges = readonly (readonly number[])[];
const { forbidden, joiners, not_first: notFirst } = list as { forbidden: Ranges; joiners: readonly number[]; not_first: Ranges };
const SPACE = 0x20;

function inRanges(ranges: Ranges, cp: number): boolean {
  let low = 0;
  let high = ranges.length - 1;
  while (low <= high) {
    const mid = (low + high) >> 1;
    const [start = 0, end = 0] = ranges[mid] ?? [];
    if (cp < start) high = mid - 1;
    else if (cp > end) low = mid + 1;
    else return true;
  }
  return false;
}

/** displayable tells whether text may be shown to a human as it is. */
export function displayable(text: string): boolean {
  if (!text.isWellFormed()) return false;
  const points = Array.from(text, (c) => c.codePointAt(0) ?? 0);
  const first = points[0];
  const last = points[points.length - 1];
  if (first === undefined || last === undefined) return false;
  const joiner = (cp: number) => joiners.includes(cp);
  if (first === SPACE || last === SPACE || joiner(first) || joiner(last) || inRanges(notFirst, first)) return false;
  let afterJoiner = false;
  for (const cp of points) {
    if (inRanges(forbidden, cp) || (joiner(cp) && afterJoiner)) return false;
    afterJoiner = joiner(cp);
  }
  return true;
}
