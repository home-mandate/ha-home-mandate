// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { announcement, clockOffset, formatClock, remainingSeconds, spokenDuration } from './countdown.ts';

describe('clockOffset', () => {
  it('is how far the browser clock is ahead of the server', () => {
    expect(clockOffset('2026-10-02T17:42:00Z', Date.parse('2026-10-02T17:42:05Z'))).toBe(5000);
    expect(clockOffset('not a time', 1000)).toBe(0);
  });
});

describe('remainingSeconds', () => {
  const expires = '2026-10-02T17:43:30Z';
  it('counts from the server time, not the browser clock', () => {
    // Browser runs 5 s ahead: 17:42:35 browser time is 17:42:30 server time → 60 s left.
    expect(remainingSeconds(expires, 5000, Date.parse('2026-10-02T17:42:35Z'))).toBe(60);
  });

  it('rounds up, so 0 means really expired, and never goes below 0', () => {
    expect(remainingSeconds(expires, 0, Date.parse('2026-10-02T17:43:29.200Z'))).toBe(1);
    expect(remainingSeconds(expires, 0, Date.parse('2026-10-02T17:43:31Z'))).toBe(0);
  });

  it('treats an unreadable expiry as expired', () => {
    expect(remainingSeconds('soon', 0, 0)).toBe(0);
  });
});

describe('announcement', () => {
  it.each([
    [61, 60, 60],
    [62, 61, null],
    [31, 30, 30],
    [11, 9, 10],
    [12, 10, 10],
    [1, 0, 0],
    [0, 0, null],
    [60, 60, null],
    [null, 45, null],
    [null, 0, 0],
    // A throttled background tab jumps over several thresholds: say the current one.
    [45, 0, 0],
    [70, 20, 30],
  ] as const)('from %s to %s announces %s', (prev, next, want) => {
    expect(announcement(prev, next)).toBe(want);
  });
});

describe('spokenDuration', () => {
  it('says minutes and seconds in words, leaving out a zero part', () => {
    expect(spokenDuration(90, 'en')).toBe('1 minute 30 seconds');
    expect(spokenDuration(40, 'de')).toBe('40 Sekunden');
    expect(spokenDuration(120, 'de')).toBe('2 Minuten');
    expect(spokenDuration(1, 'en')).toBe('1 second');
    expect(spokenDuration(0, 'en')).toBe('0 seconds');
  });
});

describe('formatClock', () => {
  it('shows m:ss with locale digits', () => {
    expect(formatClock(102, 'de')).toBe('1:42');
    expect(formatClock(9, 'en')).toBe('0:09');
    expect(formatClock(3600, 'en')).toBe('60:00');
    expect(formatClock(-5, 'en')).toBe('0:00');
  });
});
