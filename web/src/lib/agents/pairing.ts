// SPDX-License-Identifier: AGPL-3.0-or-later

// Pairing by code in the UI (decision D5): what the code field accepts and how the
// server's answers map to the field's states. The server is the authority; this only
// keeps obviously incomplete codes from costing an attempt.

import { ApiError } from '../api/client.ts';

/** Characters of a code; the server ignores case, spaces and the dash. */
export const CODE_LENGTH = 8;
/** Lock time when the server gives none: the documented 10 minutes. */
const LOCK_FALLBACK_S = 600;

export type CodeState = 'idle' | 'wrong' | 'expired' | 'locked' | 'failed';

/** normalizeCode is the code without spaces and dashes, in capitals. */
export function normalizeCode(text: string): string {
  return text.toUpperCase().replace(/[\s-]/g, '');
}

/** isComplete tells whether the text holds a code of the right length and alphabet. */
export function isComplete(text: string): boolean {
  return /^[A-Z0-9]{8}$/.test(normalizeCode(text));
}

/** displayCode writes a complete code as the agent shows it: "BCDF-GHJK". */
export function displayCode(text: string): string {
  const code = normalizeCode(text);
  return code.length === CODE_LENGTH ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
}

/** codeError maps a failed check or approval to the field's state; lockedFor is in seconds. */
export function codeError(err: unknown): { state: CodeState; lockedFor: number } {
  if (!(err instanceof ApiError)) return { state: 'failed', lockedFor: 0 };
  switch (err.code) {
    case 'pairing_code_invalid':
      return { state: 'wrong', lockedFor: 0 };
    case 'pairing_code_expired':
      return { state: 'expired', lockedFor: 0 };
    case 'pairing_locked':
      return { state: 'locked', lockedFor: err.retryAfter ?? LOCK_FALLBACK_S };
    default:
      return { state: 'failed', lockedFor: 0 };
  }
}
