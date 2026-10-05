// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { signInError } from './signin.ts';

describe('signInError', () => {
  it('reads the known reasons of a failed sign-in', () => {
    expect(signInError('#/signin?error=denied')).toBe('denied');
    expect(signInError('#/signin?error=failed')).toBe('failed');
    expect(signInError('#/signin?error=busy')).toBe('busy');
  });

  it('ignores everything else', () => {
    for (const hash of ['', '#/', '#/signin', '#/signin?error=', '#/signin?error=<script>', '#/agents?error=denied',
      '#/signin?error=denied&error=failed', '#/signin?error=DENIED']) {
      expect(signInError(hash)).toBeNull();
    }
  });
});
