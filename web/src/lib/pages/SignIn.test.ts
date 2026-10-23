// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { setLocale } from '../paraglide/runtime.js';
import SignIn from './SignIn.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(cleanup);

describe('SignIn', () => {
  it('signs in to the start page', () => {
    render(SignIn, { error: null });
    expect(screen.getByRole('link').getAttribute('href')).toBe('signin');
  });

  it('signs in through the request’s link when its page was opened (issue #53)', () => {
    render(SignIn, { error: null, request: '0123456789abcdef0123456789abcdef' });
    expect(screen.getByRole('link').getAttribute('href')).toBe('requests/0123456789abcdef0123456789abcdef');
  });
});
