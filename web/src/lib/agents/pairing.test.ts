// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/client.ts';
import { codeError, displayCode, isComplete, isHomeAddress, normalizeCode } from './pairing.ts';

describe('pairing code', () => {
  it('ignores case, spaces and the dash, like the server', () => {
    expect(normalizeCode(' bcdf-ghjk ')).toBe('BCDFGHJK');
    expect(normalizeCode('bc df gh jk')).toBe('BCDFGHJK');
  });

  it('is complete only with 8 characters of the server alphabet', () => {
    expect(isComplete('bcdf-ghjk')).toBe(true);
    expect(isComplete('BCDF-GHJ')).toBe(false);
    expect(isComplete('BCDF-GHJKL')).toBe(false);
    expect(isComplete('BCDF_GHJK')).toBe(false);
    expect(isComplete('BCDF-GHJ\u00C4')).toBe(false);
    // Outside the server's alphabet: a typo here must not cost one of the five attempts.
    expect(isComplete('BCDF-GHJ0')).toBe(false);
    expect(isComplete('ABCD-GHJK')).toBe(false);
  });

  it('shows a complete code with the dash in the middle', () => {
    expect(displayCode('bcdfghjk')).toBe('BCDF-GHJK');
    expect(displayCode('bcd')).toBe('BCD');
  });

  it('knows addresses of the home network', () => {
    for (const ip of ['192.168.1.42', '10.0.0.5', '172.16.0.1', '172.31.255.1', '127.0.0.1', '169.254.1.1', '::1', 'fd12:3456::1', 'fe80::1%eth0', '::ffff:192.168.0.2', '[fd00::1]']) {
      expect(isHomeAddress(ip), ip).toBe(true);
    }
    for (const ip of ['8.8.8.8', '172.32.0.1', '100.64.0.1', '2001:db8::1', '', 'not an ip']) {
      expect(isHomeAddress(ip), ip).toBe(false);
    }
  });

  it('maps server answers to field states, with the lock time', () => {
    expect(codeError(new ApiError('pairing_code_invalid', 400))).toEqual({ state: 'wrong', lockedFor: 0 });
    expect(codeError(new ApiError('pairing_code_expired', 410))).toEqual({ state: 'expired', lockedFor: 0 });
    expect(codeError(new ApiError('pairing_locked', 429, undefined, 540))).toEqual({ state: 'locked', lockedFor: 540 });
    expect(codeError(new ApiError('pairing_locked', 429))).toEqual({ state: 'locked', lockedFor: 600 });
    expect(codeError(new ApiError('pairing_locked', 429, undefined, 86_400))).toEqual({ state: 'locked', lockedFor: 600 });
    // Another request behind the code than the one checked: start over, like an expired code.
    expect(codeError(new ApiError('conflict', 409))).toEqual({ state: 'expired', lockedFor: 0 });
    expect(codeError(new ApiError('unavailable', 0))).toEqual({ state: 'failed', lockedFor: 0 });
    expect(codeError(new Error('x'))).toEqual({ state: 'failed', lockedFor: 0 });
  });
});
