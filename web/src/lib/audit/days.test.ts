// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import type { AuditEntry } from '../api/types.ts';
import { auditFixture } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import { groupByDay } from './days.ts';

beforeEach(() => setLocale('en', { reload: false }));

const at = (seq: number, recorded_at: string): AuditEntry => ({ ...(auditFixture[0] as AuditEntry), seq, recorded_at });
const ctx = { locale: 'en', timeZone: 'Europe/Berlin' };

describe('groupByDay', () => {
  it('groups by calendar day of the household, newest first as given, with today and yesterday named', () => {
    const now = Date.parse('2026-10-02T10:00:00Z');
    const days = groupByDay([at(4, '2026-10-02T09:00:00Z'), at(3, '2026-10-01T21:30:00Z'), at(2, '2026-10-01T12:00:00Z'), at(1, '2026-09-29T08:00:00Z')], ctx, now);
    // 2026-10-01T21:30Z is 23:30 in Berlin: still October 1.
    expect(days.map((d) => [d.label, d.entries.map((e) => e.seq)])).toEqual([
      ['Today', [4]],
      ['Yesterday', [3, 2]],
      ['September 29, 2026', [1]],
    ]);
  });

  it('puts an entry after midnight in Berlin on the next day, whatever the browser zone', () => {
    const now = Date.parse('2026-10-02T10:00:00Z');
    const days = groupByDay([at(2, '2026-10-01T22:30:00Z')], ctx, now);
    expect(days[0]?.label).toBe('Today');
  });

  it('keeps the days unique and stable as keys', () => {
    const now = Date.parse('2026-10-02T10:00:00Z');
    const days = groupByDay([at(2, '2026-10-01T10:00:00Z'), at(1, '2026-10-01T09:00:00Z')], ctx, now);
    expect(days).toHaveLength(1);
    expect(days[0]?.key).toBe('2026-10-01');
    expect(groupByDay([], ctx, now)).toEqual([]);
  });
});
