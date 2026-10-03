// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { MARK, MARK2, around, pieces } from './sentence.ts';

describe('around', () => {
  it('splits a sentence at the mark, wherever the language puts it', () => {
    expect(around(`${MARK} wants to open the door`)).toEqual(['', ' wants to open the door']);
    expect(around(`Die Tür möchte ${MARK} öffnen`)).toEqual(['Die Tür möchte ', ' öffnen']);
  });

  it('keeps the whole text before when the mark is missing or doubled, so nothing is lost', () => {
    expect(around('no mark')).toEqual(['no mark', '']);
    expect(around(`a ${MARK} b ${MARK} c`)).toEqual([`a ${MARK} b ${MARK} c`.replaceAll(MARK, ''), '']);
  });

  it('uses a control character that cleaned foreign text can never contain', () => {
    expect(MARK).toMatch(/^\p{Cc}$/u);
  });
});

describe('pieces', () => {
  it('splits at both marks in the order of the language', () => {
    expect(pieces(`${MARK} möchte ${MARK2} öffnen`)).toEqual([{ slot: 1 }, { text: ' möchte ' }, { slot: 2 }, { text: ' öffnen' }]);
    expect(pieces(`${MARK2} soll ${MARK} …`)).toEqual([{ slot: 2 }, { text: ' soll ' }, { slot: 1 }, { text: ' …' }]);
  });

  it('is plain text without marks', () => {
    expect(pieces('no marks')).toEqual([{ text: 'no marks' }]);
  });
});
