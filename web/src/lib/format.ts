// SPDX-License-Identifier: AGPL-3.0-or-later

// All user-visible dates, times and numbers are formatted here, through Intl, in the
// household's time zone from Home Assistant, never the browser's.

export interface FormatContext {
  /** BCP 47 locale, e.g. "de" or "en-US". */
  locale: string;
  /** IANA time zone of the household, e.g. "Europe/Berlin". */
  timeZone: string;
}

export function formatDate(date: Date, ctx: FormatContext): string {
  return new Intl.DateTimeFormat(ctx.locale, { dateStyle: 'long', timeZone: ctx.timeZone }).format(date);
}

export function formatTime(date: Date, ctx: FormatContext): string {
  return new Intl.DateTimeFormat(ctx.locale, { timeStyle: 'short', timeZone: ctx.timeZone }).format(date);
}

export function formatDateTime(date: Date, ctx: FormatContext): string {
  return new Intl.DateTimeFormat(ctx.locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: ctx.timeZone,
  }).format(date);
}

export function formatNumber(value: number, ctx: FormatContext, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(ctx.locale, options).format(value);
}

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/**
 * formatRelative describes date relative to now, e.g. "3 minutes ago" or "yesterday".
 * The unit is chosen after rounding (59.6 s is "1 minute ago", not "60 seconds ago");
 * days are calendar days in the household time zone, so a 25-hour DST day counts once.
 */
export function formatRelative(date: Date, now: Date, ctx: FormatContext): string {
  const diff = date.getTime() - now.getTime();
  const rtf = new Intl.RelativeTimeFormat(ctx.locale, { numeric: 'auto' });
  const seconds = Math.round(diff / SECOND);
  if (Math.abs(seconds) < 60) return rtf.format(seconds, 'second');
  const minutes = Math.round(diff / MINUTE);
  if (Math.abs(minutes) < 60) return rtf.format(minutes, 'minute');
  const hours = Math.round(diff / HOUR);
  if (Math.abs(hours) < 24) return rtf.format(hours, 'hour');
  return rtf.format(dayNumber(date, ctx.timeZone) - dayNumber(now, ctx.timeZone), 'day');
}

/** dayNumber counts days since the epoch for the calendar date of d in timeZone. */
export function dayNumber(d: Date, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone, year: 'numeric', month: 'numeric', day: 'numeric' }).formatToParts(d);
  const part = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((p) => p.type === type)?.value);
  return Date.UTC(part('year'), part('month') - 1, part('day')) / DAY;
}

/** formatList joins names, e.g. "Anna und Jonas". */
export function formatList(values: readonly string[], ctx: FormatContext): string {
  return new Intl.ListFormat(ctx.locale, { style: 'long', type: 'conjunction' }).format(values);
}
