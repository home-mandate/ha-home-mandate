// SPDX-License-Identifier: AGPL-3.0-or-later

// The audit log is grouped by calendar day of the household (design README 6.8), never
// of the browser: an entry at 23:30 in Berlin belongs to that day everywhere.

import type { AuditEntry } from '../api/types.ts';
import { dayNumber, formatDate, type FormatContext } from '../format.ts';
import { m } from '../i18n.ts';

export interface Day {
  /** YYYY-MM-DD in the household time zone. */
  key: string;
  label: string;
  entries: AuditEntry[];
}

const DAY_MS = 86_400_000;

/** groupByDay keeps the order of entries; now is the server's clock in ms. */
export function groupByDay(entries: readonly AuditEntry[], ctx: FormatContext, now: number): Day[] {
  const today = dayNumber(new Date(now), ctx.timeZone);
  const days: Day[] = [];
  for (const entry of entries) {
    const date = new Date(entry.recorded_at);
    const day = dayNumber(date, ctx.timeZone);
    const key = new Date(day * DAY_MS).toISOString().slice(0, 10);
    const last = days.at(-1);
    if (last?.key === key) {
      last.entries.push(entry);
      continue;
    }
    const label = day === today ? m.day_today() : day === today - 1 ? m.day_yesterday() : formatDate(date, ctx);
    days.push({ key, label, entries: [entry] });
  }
  return days;
}
