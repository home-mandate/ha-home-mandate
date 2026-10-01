// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatDate, formatDateTime, formatNumber, formatRelative, formatTime } from './format.ts';

const berlinDE = { locale: 'de-DE', timeZone: 'Europe/Berlin' };
const newYorkUS = { locale: 'en-US', timeZone: 'America/New_York' };

// Intl uses narrow no-break spaces in some English formats; compare on plain spaces.
const plain = (s: string) => s.replace(/[\u202f\u00a0]/g, ' ');

describe('format', () => {
  it('runs in a time zone that differs from the household', () => {
    // vite.config.ts sets TZ=Pacific/Kiritimati (UTC+14) for the tests.
    expect(new Date('2026-10-01T00:00:00Z').getTimezoneOffset()).toBe(-14 * 60);
  });

  it('formats dates in the household time zone, not the machine time zone', () => {
    const lateEvening = new Date('2026-10-30T22:30:00Z'); // 31 Oct in Kiritimati
    expect(formatDate(lateEvening, berlinDE)).toBe('30. Oktober 2026');
    expect(formatDate(lateEvening, newYorkUS)).toBe('October 30, 2026');
  });

  it('formats times in de-DE and en-US', () => {
    const date = new Date('2026-10-01T16:05:00Z');
    expect(formatTime(date, berlinDE)).toBe('18:05');
    expect(plain(formatTime(date, newYorkUS))).toBe('12:05 PM');
  });

  it('handles the DST change on 2026-10-25 in Europe/Berlin', () => {
    // 02:30 local time occurs twice: first in CEST (UTC+2), then in CET (UTC+1).
    const beforeChange = new Date('2026-10-25T00:30:00Z');
    const afterChange = new Date('2026-10-25T01:30:00Z');
    expect(formatTime(beforeChange, berlinDE)).toBe('02:30');
    expect(formatTime(afterChange, berlinDE)).toBe('02:30');
    expect(formatTime(new Date('2026-10-25T02:30:00Z'), berlinDE)).toBe('03:30');
  });

  it('formats date and time together', () => {
    const date = new Date('2026-10-25T01:30:00Z');
    expect(formatDateTime(date, berlinDE)).toBe('25.10.2026, 02:30');
    expect(plain(formatDateTime(date, newYorkUS))).toBe('Oct 24, 2026, 9:30 PM');
  });

  it('formats numbers per locale', () => {
    expect(formatNumber(1234.5, berlinDE)).toBe('1.234,5');
    expect(formatNumber(1234.5, newYorkUS)).toBe('1,234.5');
    expect(formatNumber(21.5, berlinDE, { style: 'unit', unit: 'celsius' })).toBe('21,5 °C');
  });

  it('formats relative times with the right unit', () => {
    const now = new Date('2026-10-01T12:00:00Z');
    const at = (ms: number) => new Date(now.getTime() + ms);
    expect(formatRelative(at(-30_000), now, newYorkUS)).toBe('30 seconds ago');
    expect(formatRelative(at(0), now, newYorkUS)).toBe('now');
    expect(formatRelative(at(-3 * 60_000), now, newYorkUS)).toBe('3 minutes ago');
    expect(formatRelative(at(-3 * 60_000), now, berlinDE)).toBe('vor 3 Minuten');
    expect(formatRelative(at(2 * 3_600_000), now, berlinDE)).toBe('in 2 Stunden');
    expect(formatRelative(at(-86_400_000), now, berlinDE)).toBe('gestern');
    expect(formatRelative(at(3 * 86_400_000), now, newYorkUS)).toBe('in 3 days');
  });

  it('chooses the unit after rounding', () => {
    const now = new Date('2026-10-01T12:00:00Z');
    const at = (ms: number) => new Date(now.getTime() + ms);
    expect(formatRelative(at(-59_600), now, newYorkUS)).toBe('1 minute ago');
    expect(formatRelative(at(-(59 * 60_000 + 40_000)), now, newYorkUS)).toBe('1 hour ago');
    expect(formatRelative(at(-(23 * 3_600_000 + 40 * 60_000)), now, berlinDE)).toBe('gestern');
  });

  it('counts calendar days in the household time zone across the DST change', () => {
    // 2026-10-25 has 25 hours in Berlin: 00:10 CEST and 23:30 CET are the same day.
    const now = new Date('2026-10-25T22:30:00Z');
    expect(formatRelative(new Date('2026-10-24T22:10:00Z'), now, berlinDE)).toBe('heute');
    expect(formatRelative(new Date('2026-10-24T21:50:00Z'), now, berlinDE)).toBe('gestern');
  });

  it('rejects an invalid time zone instead of falling back silently', () => {
    expect(() => formatDate(new Date(), { locale: 'de', timeZone: 'Mars/Olympus' })).toThrow(RangeError);
  });
});
