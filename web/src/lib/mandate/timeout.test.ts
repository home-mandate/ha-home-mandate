// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { cappedBy, inputMax, timeoutInput, timeoutIso, timeoutText } from './timeout.ts';

describe('timeout', () => {
  it('splits a duration into whole minutes or seconds and back', () => {
    expect(timeoutInput('PT2M')).toEqual({ value: '2', unit: 'm' });
    expect(timeoutInput('PT90S')).toEqual({ value: '90', unit: 's' });
    expect(timeoutIso({ value: '45', unit: 's' })).toBe('PT45S');
    expect(timeoutText('PT2M', 'en')).toBe('2 minutes');
  });

  it('offers at most the installation limit in the unit chosen', () => {
    expect(inputMax(120, 'm')).toBe(2);
    expect(inputMax(120, 's')).toBe(120);
    // 90 seconds: whole minutes stop at 1.
    expect(inputMax(90, 'm')).toBe(1);
    expect(inputMax(null, 's')).toBeUndefined();
  });

  it('reports the limit a longer timeout is capped to', () => {
    expect(cappedBy('PT10M', 120)).toBe(120);
    expect(cappedBy('PT2M', 120)).toBeNull();
    expect(cappedBy('PT90S', 120)).toBeNull();
    expect(cappedBy('PT10M', null)).toBeNull();
    expect(cappedBy('', 120)).toBeNull();
  });
});
