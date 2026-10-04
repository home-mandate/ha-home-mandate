// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { dateInZone, effectiveStatus, expiresAfter, startOfDay, validUntilDate } from './dates.ts';
import { timeoutInput, timeoutIso, timeoutText } from './timeout.ts';

const BERLIN = 'Europe/Berlin';
const NEW_YORK = 'America/New_York';

describe('dates in the household time zone', () => {
  it('shows the calendar date of an instant in the household zone, not the machine zone', () => {
    expect(dateInZone('2026-10-01T00:00:00Z', BERLIN)).toBe('2026-10-01');
    expect(dateInZone('2026-09-30T22:00:00Z', BERLIN)).toBe('2026-10-01');
    expect(dateInZone('2026-10-01T03:59:59Z', NEW_YORK)).toBe('2026-09-30');
    expect(dateInZone('2026-10-01T02:00:00+02:00', BERLIN)).toBe('2026-10-01');
  });

  it('has no date for a missing or malformed instant', () => {
    expect(dateInZone(undefined, BERLIN)).toBe('');
    expect(dateInZone('2026-02-30T00:00:00Z', BERLIN)).toBe('');
  });

  it('starts a day at local midnight, in summer and winter time', () => {
    expect(startOfDay('2026-10-01', BERLIN)).toBe('2026-09-30T22:00:00Z');
    expect(startOfDay('2026-12-01', BERLIN)).toBe('2026-11-30T23:00:00Z');
    expect(startOfDay('2026-10-01', NEW_YORK)).toBe('2026-10-01T04:00:00Z');
    expect(startOfDay('2026-10-01', 'UTC')).toBe('2026-10-01T00:00:00Z');
  });

  it('gets the days around a DST change right (2026-10-25 has 25 hours in Berlin)', () => {
    expect(startOfDay('2026-10-25', BERLIN)).toBe('2026-10-24T22:00:00Z');
    expect(startOfDay('2026-10-26', BERLIN)).toBe('2026-10-25T23:00:00Z');
    expect(startOfDay('2026-03-29', BERLIN)).toBe('2026-03-28T23:00:00Z');
    expect(startOfDay('2026-03-30', BERLIN)).toBe('2026-03-29T22:00:00Z');
  });

  it('starts the day with the jump where the clock skips midnight', () => {
    // Santiago 2024-09-08: 00:00 does not exist, the clock goes from 23:59:59 to 01:00.
    expect(startOfDay('2024-09-08', 'America/Santiago')).toBe('2024-09-08T04:00:00Z');
    expect(dateInZone('2024-09-08T04:00:00Z', 'America/Santiago')).toBe('2024-09-08');
    expect(dateInZone('2024-09-08T03:59:59Z', 'America/Santiago')).toBe('2024-09-07');
    expect(startOfDay('2025-03-09', 'America/Havana')).toBe('2025-03-09T05:00:00Z');
    expect(expiresAfter('2024-09-07', 'America/Santiago')).toBe('2024-09-08T04:00:00Z');
    expect(validUntilDate('2024-09-08T04:00:00Z', 'America/Santiago')).toBe('2024-09-07');
  });

  it('takes the first midnight where the clock is set back over it', () => {
    // Havana 2024-11-03: 01:00 becomes 00:00, so midnight happens twice.
    expect(startOfDay('2024-11-03', 'America/Havana')).toBe('2024-11-03T04:00:00Z');
  });

  it('accepts the years typed on the way to a four-digit year', () => {
    expect(startOfDay('0002-05-06', 'UTC')).toBe('0002-05-06T00:00:00Z');
    expect(dateInZone('0020-05-06T00:00:00Z', 'UTC')).toBe('0020-05-06');
  });

  it('refuses what is not a calendar date', () => {
    for (const bad of ['', '2026-02-30', '2026-13-01', '26-01-01', '2026-1-1', 'tomorrow']) {
      expect(startOfDay(bad, BERLIN)).toBeNull();
      expect(expiresAfter(bad, BERLIN)).toBeNull();
    }
  });

  it('includes the whole last day: expires is the start of the next day', () => {
    expect(expiresAfter('2026-12-31', BERLIN)).toBe('2026-12-31T23:00:00Z');
    expect(validUntilDate('2026-12-31T23:00:00Z', BERLIN)).toBe('2026-12-31');
    expect(expiresAfter('2026-10-24', BERLIN)).toBe('2026-10-24T22:00:00Z');
    expect(expiresAfter('2026-10-25', BERLIN)).toBe('2026-10-25T23:00:00Z');
  });

  it('round-trips every day of a year with DST changes', () => {
    for (let day = Date.UTC(2026, 0, 1); day < Date.UTC(2027, 0, 1); day += 86_400_000) {
      const date = new Date(day).toISOString().slice(0, 10);
      expect(dateInZone(startOfDay(date, BERLIN) ?? '', BERLIN)).toBe(date);
      expect(validUntilDate(expiresAfter(date, NEW_YORK), NEW_YORK)).toBe(date);
    }
  });

  it('shows the last valid day of an end that is not on a day boundary', () => {
    expect(validUntilDate('2026-12-31T11:00:00Z', BERLIN)).toBe('2026-12-31');
    expect(validUntilDate(null, BERLIN)).toBe('');
    expect(validUntilDate(undefined, BERLIN)).toBe('');
  });
});

describe('effectiveStatus', () => {
  const at = (iso: string) => Date.parse(iso);
  const mandate = { status: 'active' as const, valid_from: '2026-10-01T00:00:00Z', expires: '2026-11-01T00:00:00Z' };

  it('applies the validity dates at the given time', () => {
    expect(effectiveStatus(mandate, at('2026-09-30T23:59:59Z'))).toBe('not_yet_valid');
    expect(effectiveStatus(mandate, at('2026-10-01T00:00:00Z'))).toBe('active');
    expect(effectiveStatus(mandate, at('2026-10-31T23:59:59Z'))).toBe('active');
    expect(effectiveStatus(mandate, at('2026-11-01T00:00:00Z'))).toBe('expired');
    expect(effectiveStatus({ ...mandate, expires: null }, at('2030-01-01T00:00:00Z'))).toBe('active');
    expect(effectiveStatus({ status: 'active', valid_from: mandate.valid_from }, at('2030-01-01T00:00:00Z'))).toBe('active');
  });

  it('keeps revoked whatever the dates say', () => {
    expect(effectiveStatus({ ...mandate, status: 'revoked' }, at('2026-10-15T00:00:00Z'))).toBe('revoked');
  });
});

describe('timeout', () => {
  it('splits a duration into number and unit', () => {
    expect(timeoutInput('PT2M')).toEqual({ value: '2', unit: 'm' });
    expect(timeoutInput('PT120S')).toEqual({ value: '2', unit: 'm' });
    expect(timeoutInput('PT90S')).toEqual({ value: '90', unit: 's' });
    expect(timeoutInput('PT1M30S')).toEqual({ value: '90', unit: 's' });
    expect(timeoutInput('PT0S')).toEqual({ value: '0', unit: 's' });
    expect(timeoutInput('2 minutes')).toEqual({ value: '', unit: 's' });
  });

  it('builds the duration, and nothing valid from anything but a whole number', () => {
    expect(timeoutIso({ value: '2', unit: 'm' })).toBe('PT2M');
    expect(timeoutIso({ value: ' 045 ', unit: 's' })).toBe('PT45S');
    for (const value of ['', '1.5', '-3', '2m', '1e3', '9999999']) expect(timeoutIso({ value, unit: 's' })).toBe('');
  });

  it('writes the duration out in the locale', () => {
    expect(timeoutText('PT2M', 'de')).toBe('2 Minuten');
    expect(timeoutText('PT90S', 'en')).toBe('90 seconds');
    expect(timeoutText('', 'en')).toBe('');
  });
});
