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

/** formatRelative describes date relative to now, e.g. "3 minutes ago" or "in 2 days". */
export function formatRelative(date: Date, now: Date, ctx: FormatContext): string {
  const diff = date.getTime() - now.getTime();
  const abs = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat(ctx.locale, { numeric: 'auto' });
  if (abs < MINUTE) return rtf.format(Math.round(diff / SECOND), 'second');
  if (abs < HOUR) return rtf.format(Math.round(diff / MINUTE), 'minute');
  if (abs < DAY) return rtf.format(Math.round(diff / HOUR), 'hour');
  return rtf.format(Math.round(diff / DAY), 'day');
}
