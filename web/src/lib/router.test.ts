// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { parseHash } from './router.ts';

describe('parseHash', () => {
  it.each([
    ['', 'home'],
    ['#', 'home'],
    ['#/', 'home'],
    ['#/agents/unknown', 'not_found'],
    ['#//evil.example', 'not_found'],
    ['#javascript:alert(1)', 'not_found'],
  ])('%s → %s', (hash, want) => {
    expect(parseHash(hash).name).toBe(want);
  });
});
