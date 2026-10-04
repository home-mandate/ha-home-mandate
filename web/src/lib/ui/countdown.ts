// SPDX-License-Identifier: AGPL-3.0-or-later

// Countdowns of approval requests run on the server's clock (design README section 8): the
// browser clock may be off, so the remaining time is expires_at minus the server time,
// estimated as the browser time minus the measured offset. Screen readers hear it only at
// 60, 30 and 10 seconds and at the end, not every second.

const ANNOUNCE_AT: readonly number[] = [60, 30, 10, 0];

/** clockOffset is how many ms the browser clock is ahead of the server (0 if unknown). */
export function clockOffset(serverTime: string, browserNow: number): number {
  const server = Date.parse(serverTime);
  return Number.isNaN(server) ? 0 : browserNow - server;
}

/** remainingSeconds rounds up, so 0 means expired; an unreadable expiry counts as expired. */
export function remainingSeconds(expiresAt: string, offsetMs: number, browserNow: number): number {
  const expires = Date.parse(expiresAt);
  if (Number.isNaN(expires)) return 0;
  return Math.max(0, Math.ceil((expires - (browserNow - offsetMs)) / 1000));
}

/**
 * announcement returns the lowest threshold crossed between two readings (prev null:
 * first reading, announce only an expired request), or null. Nothing is said when a
 * countdown appears: the timer's name tells the time on demand.
 */
export function announcement(prev: number | null, next: number): number | null {
  if (prev === null) return next === 0 ? 0 : null;
  // The lowest threshold crossed: after a jump (throttled background tab) the current one.
  let crossed: number | null = null;
  for (const at of ANNOUNCE_AT) {
    if (prev > at && next <= at) crossed = at;
  }
  return crossed;
}

/** spokenDuration says seconds in words for the timer's name, e.g. "1 minute 30 seconds"; a zero part is left out. */
export function spokenDuration(seconds: number, locale: string): string {
  const s = Math.max(0, Math.floor(seconds));
  const unit = (value: number, name: 'minute' | 'second') =>
    new Intl.NumberFormat(locale, { style: 'unit', unit: name, unitDisplay: 'long' }).format(value);
  const minutes = Math.floor(s / 60);
  if (minutes === 0) return unit(s, 'second');
  return s % 60 === 0 ? unit(minutes, 'minute') : `${unit(minutes, 'minute')} ${unit(s % 60, 'second')}`;
}

/** formatClock shows seconds as m:ss in the digits of the locale. */
export function formatClock(seconds: number, locale: string): string {
  const s = Math.max(0, Math.floor(seconds));
  const minutes = new Intl.NumberFormat(locale).format(Math.floor(s / 60));
  const rest = new Intl.NumberFormat(locale, { minimumIntegerDigits: 2 }).format(s % 60);
  return `${minutes}:${rest}`;
}
