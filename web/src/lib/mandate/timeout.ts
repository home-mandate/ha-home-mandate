// SPDX-License-Identifier: AGPL-3.0-or-later

// The approval timeout as the form shows it: a number and a unit. The document holds an
// ISO 8601 duration ("PT2M", "PT90S").

import { timeoutSeconds } from '../engine/check.ts';

export type TimeoutUnit = 's' | 'm';

export interface TimeoutInput {
  /** As typed; anything but digits makes the timeout invalid. */
  value: string;
  unit: TimeoutUnit;
}

const SECONDS_PER_MINUTE = 60;
const DIGITS = /^\d{1,6}$/;

/** timeoutInput splits a duration for the form: whole minutes as minutes, otherwise seconds. */
export function timeoutInput(iso: string): TimeoutInput {
  const seconds = timeoutSeconds(iso);
  if (Number.isNaN(seconds)) return { value: '', unit: 's' };
  return seconds > 0 && seconds % SECONDS_PER_MINUTE === 0
    ? { value: String(seconds / SECONDS_PER_MINUTE), unit: 'm' }
    : { value: String(seconds), unit: 's' };
}

/** timeoutIso builds the duration; an input that is no whole number gives "" (invalid). */
export function timeoutIso(input: TimeoutInput): string {
  const value = input.value.trim();
  if (!DIGITS.test(value)) return '';
  return `PT${Number(value)}${input.unit === 'm' ? 'M' : 'S'}`;
}

/**
 * inputMax is the largest number the form offers in a unit for the installation's upper
 * limit (approval_timeout_seconds); undefined while the limit is unknown.
 */
export function inputMax(maxSeconds: number | null, unit: TimeoutUnit): number | undefined {
  if (maxSeconds === null) return undefined;
  return unit === 'm' ? Math.max(1, Math.floor(maxSeconds / SECONDS_PER_MINUTE)) : maxSeconds;
}

/**
 * cappedBy returns the installation's upper limit when a timeout exceeds it: the server
 * then waits only that long (a mandate may only shorten it). Null otherwise.
 */
export function cappedBy(iso: string, maxSeconds: number | null): number | null {
  const seconds = timeoutSeconds(iso);
  return maxSeconds !== null && !Number.isNaN(seconds) && seconds > maxSeconds ? maxSeconds : null;
}

/** timeoutText writes a duration out, e.g. "2 minutes" or "90 seconds"; "" if invalid. */
export function timeoutText(iso: string, locale: string): string {
  const { value, unit } = timeoutInput(iso);
  if (value === '') return '';
  return new Intl.NumberFormat(locale, { style: 'unit', unit: unit === 'm' ? 'minute' : 'second', unitDisplay: 'long' }).format(Number(value));
}
