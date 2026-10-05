// SPDX-License-Identifier: AGPL-3.0-or-later

// Direct mode: a failed sign-in sends the browser to #/signin?error=<reason>. Only the
// known reasons are shown; anything else in the URL is ignored.

export type SignInError = 'denied' | 'failed' | 'busy';

const ERRORS: ReadonlySet<string> = new Set<SignInError>(['denied', 'failed', 'busy']);

const isSignInError = (value: string | undefined): value is SignInError => value !== undefined && ERRORS.has(value);

/** signInError reads the reason of a failed sign-in from the location hash. */
export function signInError(hash: string): SignInError | null {
  const [path = '', query = ''] = hash.replace(/^#/, '').split('?', 2);
  if (path !== '/signin') return null;
  const values = new URLSearchParams(query).getAll('error');
  const [value] = values;
  return values.length === 1 && isSignInError(value) ? value : null;
}
