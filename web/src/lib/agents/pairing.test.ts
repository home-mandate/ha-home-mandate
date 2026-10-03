// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/client.ts';
import { codeError, displayCode, isComplete, normalizeCode } from './pairing.ts';

describe('pairing code', () => {
  it('ignores case, spaces and the dash, like the server', () => {
    expect(normalizeCode(' bcdf-ghjk ')).toBe('BCDFGHJK');
    expect(normalizeCode('bc df gh jk')).toBe('BCDFGHJK');
  });

  it('is complete only with 8 letters or digits', () => {
    expect(isComplete('bcdf-ghjk')).toBe(true);
    expect(isComplete('BCDF-GHJ')).toBe(false);
    expect(isComplete('BCDF-GHJKL')).toBe(false);
    expect(isComplete('BCDF_GHJK')).toBe(false);
    expect(isComplete('BCDF-GHJÄ')).toBe(false);
  });

  it('shows a complete code with the dash in the middle', () => {
    expect(displayCode('bcdfghjk')).toBe('BCDF-GHJK');
    expect(displayCode('bcd')).toBe('BCD');
  });

  it('maps server answers to field states, with the lock time', () => {
    expect(codeError(new ApiError('pairing_code_invalid', 400))).toEqual({ state: 'wrong', lockedFor: 0 });
    expect(codeError(new ApiError('pairing_code_expired', 410))).toEqual({ state: 'expired', lockedFor: 0 });
    expect(codeError(new ApiError('pairing_locked', 429, undefined, 540))).toEqual({ state: 'locked', lockedFor: 540 });
    expect(codeError(new ApiError('pairing_locked', 429))).toEqual({ state: 'locked', lockedFor: 600 });
    expect(codeError(new ApiError('unavailable', 0))).toEqual({ state: 'failed', lockedFor: 0 });
    expect(codeError(new Error('x'))).toEqual({ state: 'failed', lockedFor: 0 });
  });
});
