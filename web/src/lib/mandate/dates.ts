// SPDX-License-Identifier: AGPL-3.0-or-later

// Validity of a mandate as calendar dates. The document holds instants (valid_from
// inclusive, expires exclusive); the editor shows dates in the household time zone, never
// the browser's: "valid from" starts with that day, "valid until" includes the whole day.
// An instant that is not on a day boundary (set outside the UI) stays as it is until the
// field is edited.

import type { MandateStatus } from '../api/types.ts';
import { parseDateTime } from '../engine/check.ts';

const DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DAY_MS = 86_400_000;

const formatters = new Map<string, Intl.DateTimeFormat>();

function partsFormatter(timeZone: string): Intl.DateTimeFormat {
  let f = formatters.get(timeZone);
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone,
      hourCycle: 'h23',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
    formatters.set(timeZone, f);
  }
  return f;
}

/** utc is Date.UTC without its habit of reading the years 0–99 as 1900–1999 (typed on the way to "2026"). */
function utc(year: number, monthIndex: number, day: number, hour = 0, minute = 0, second = 0): number {
  const date = new Date(0);
  date.setUTCFullYear(year, monthIndex, day);
  return date.setUTCHours(hour, minute, second, 0);
}

/** wallClock returns the local date and time of an instant as if it were UTC, in ms. */
function wallClock(at: number, timeZone: string): number {
  const parts = partsFormatter(timeZone).formatToParts(at);
  const part = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((p) => p.type === type)?.value);
  return utc(part('year'), part('month') - 1, part('day'), part('hour'), part('minute'), part('second'));
}

const pad = (n: number, width = 2) => String(n).padStart(width, '0');
const isoDate = (ms: number) => {
  const d = new Date(ms);
  return `${pad(d.getUTCFullYear(), 4)}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
};
const isoInstant = (ms: number) => new Date(ms).toISOString().replace('.000Z', 'Z');

/** dateInZone returns the calendar date ("YYYY-MM-DD") of an RFC 3339 instant; "" if invalid. */
export function dateInZone(instant: string | undefined, timeZone: string): string {
  const at = parseDateTime(instant);
  return Number.isNaN(at) ? '' : isoDate(wallClock(at, timeZone));
}

/** parseDate returns midnight UTC of "YYYY-MM-DD" in ms, or NaN for anything that is no date. */
function parseDate(date: string): number {
  const match = DATE.exec(date);
  if (!match) return NaN;
  const [year, month, day] = match.slice(1).map(Number) as [number, number, number];
  const ms = utc(year, month - 1, day);
  return isoDate(ms) === date ? ms : NaN;
}

/**
 * startOfDay returns the instant a calendar date begins in the time zone, or null for an
 * invalid date. It tries the offsets in force the day before and the day after. Where
 * local midnight exists the earliest instant showing it wins (a clock set back shows it
 * twice); where a clock set forward skips midnight, the day begins with the jump.
 */
export function startOfDay(date: string, timeZone: string): string | null {
  const local = parseDate(date);
  if (Number.isNaN(local)) return null;
  const offsets = [local - DAY_MS, local + DAY_MS].map((at) => wallClock(at, timeZone) - at);
  const candidates = [...new Set(offsets)].map((offset) => local - offset);
  const exact = candidates.filter((at) => wallClock(at, timeZone) === local);
  return isoInstant(exact.length > 0 ? Math.min(...exact) : Math.max(...candidates));
}

/** validUntilDate returns the last day a mandate applies; "" without an end. */
export function validUntilDate(expires: string | null | undefined, timeZone: string): string {
  const at = parseDateTime(expires ?? undefined);
  return Number.isNaN(at) ? '' : isoDate(wallClock(at - 1, timeZone));
}

/** expiresAfter returns the instant right after the given last day, or null for an invalid date. */
export function expiresAfter(lastDay: string, timeZone: string): string | null {
  const local = parseDate(lastDay);
  return Number.isNaN(local) ? null : startOfDay(isoDate(local + DAY_MS), timeZone);
}

export type EffectiveStatus = MandateStatus | 'not_yet_valid' | 'expired';

/** effectiveStatus applies the validity dates to the stored status at the server's time. */
export function effectiveStatus(
  mandate: { status: MandateStatus; valid_from: string; expires?: string | null },
  now: number,
): EffectiveStatus {
  if (mandate.status === 'revoked') return 'revoked';
  if (now < parseDateTime(mandate.valid_from)) return 'not_yet_valid';
  const expires = parseDateTime(mandate.expires ?? undefined);
  return !Number.isNaN(expires) && now >= expires ? 'expired' : 'active';
}
