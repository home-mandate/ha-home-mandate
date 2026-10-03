// SPDX-License-Identifier: AGPL-3.0-or-later

// Pairing by code in the UI (decision D5): what the code field accepts and how the
// server's answers map to the field's states. The server is the authority; this only
// keeps obviously incomplete codes from costing an attempt.

import { ApiError } from '../api/client.ts';

/** Characters of a code; the server ignores case, spaces and the dash. */
export const CODE_LENGTH = 8;
/** The server's alphabet (internal/oauth/device.go): no vowels, no 0/O or 1/I look-alikes. */
const ALPHABET = 'BCDFGHJKLMNPQRSTVWXZ';
const CODE = new RegExp(`^[${ALPHABET}]{${CODE_LENGTH}}$`);
/** The documented lock: 10 minutes. Used when the server gives no time, and as the cap. */
const LOCK_S = 600;

/** incomplete: the typed text is no code yet (not sent, costs no attempt). */
export type CodeState = 'idle' | 'incomplete' | 'wrong' | 'expired' | 'locked' | 'failed';

/** normalizeCode is the code without spaces and dashes, in capitals. */
export function normalizeCode(text: string): string {
  return text.toUpperCase().replace(/[\s-]/g, '');
}

/** isComplete tells whether the text holds a code of the right length and alphabet. */
export function isComplete(text: string): boolean {
  return CODE.test(normalizeCode(text));
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
    case 'conflict': // the code now belongs to another request than the one checked
      return { state: 'expired', lockedFor: 0 };
    case 'pairing_locked':
      // A proxy's Retry-After must not stretch the lock shown beyond the server's.
      return { state: 'locked', lockedFor: Math.min(err.retryAfter ?? LOCK_S, LOCK_S) };
    default:
      return { state: 'failed', lockedFor: 0 };
  }
}

/**
 * isHomeAddress tells whether a request came from the home network: loopback, private IPv4
 * (RFC 1918), link-local, or IPv6 unique-local / link-local. Anything else gets a warning.
 */
export function isHomeAddress(address: string): boolean {
  const ip = address.trim().toLowerCase().replace(/^\[|\]$/g, '').replace(/^::ffff:/, '').split('%')[0] ?? '';
  const v4 = /^(\d{1,3})\.(\d{1,3})\.\d{1,3}\.\d{1,3}$/.exec(ip);
  if (v4) {
    const a = Number(v4[1]);
    const b = Number(v4[2]);
    return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254);
  }
  return ip === '::1' || /^f[cd][0-9a-f]{2}:/.test(ip) || /^fe[89ab][0-9a-f]:/.test(ip);
}
