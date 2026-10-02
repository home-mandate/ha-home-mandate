// SPDX-License-Identifier: AGPL-3.0-or-later

// Filters of the audit log (design README 6.8). They live in the URL query, so a filtered
// view can be bookmarked and the back button works; anything unknown in the URL is
// ignored, never guessed. The period is counted back from the server's clock.

import type { AuditEvent, AuditQuery, DecisionFilter } from '../api/types.ts';

export type Period = '24h' | '7d' | 'all';
export type TypeFilter = 'all' | AuditEvent;

export interface AuditFilters {
  period: Period;
  /** OAuth client ID of an agent. */
  agent: string | null;
  /** entity_id or area_id. */
  device: string | null;
  type: TypeFilter;
  decisions: DecisionFilter[];
  /** Selected entry (desktop detail column); no filter. */
  seq: number | null;
}

export const DEFAULT_FILTERS: AuditFilters = { period: 'all', agent: null, device: null, type: 'all', decisions: [], seq: null };

export const PERIODS: readonly Period[] = ['24h', '7d', 'all'];
export const DECISIONS: readonly DecisionFilter[] = ['allow', 'ask', 'deny', 'default'];
export const EVENTS: readonly AuditEvent[] = [
  'decision',
  'agent.registered',
  'agent.revoked',
  'mandate.created',
  'mandate.updated',
  'mandate.revoked',
  'emergency_stop.activated',
  'emergency_stop.released',
  'auth.rejected',
  'log.truncated',
];

const HOUR_MS = 3_600_000;
const PERIOD_MS: Record<Exclude<Period, 'all'>, number> = { '24h': 24 * HOUR_MS, '7d': 7 * 24 * HOUR_MS };
const SEQ = /^[1-9]\d{0,14}$/;
const MAX_ID = 512;

function one(query: Record<string, string[]>, key: string): string | null {
  const value = query[key]?.[0];
  return value && value.length <= MAX_ID ? value : null;
}

function includes<T extends string>(list: readonly T[], value: string | null): value is T {
  return value !== null && (list as readonly string[]).includes(value);
}

export function parseFilters(query: Record<string, string[]>): AuditFilters {
  const period = one(query, 'period');
  const type = one(query, 'type');
  const seq = one(query, 'seq');
  const decisions = DECISIONS.filter((d) => query['decision']?.includes(d));
  return {
    period: includes(PERIODS, period) ? period : DEFAULT_FILTERS.period,
    agent: one(query, 'agent'),
    device: one(query, 'device'),
    type: includes(EVENTS, type) ? type : 'all',
    decisions,
    seq: seq !== null && SEQ.test(seq) ? Number(seq) : null,
  };
}

/** toQuery is the URL query of f, without the defaults. */
export function toQuery(f: AuditFilters): Record<string, string[]> {
  const q: Record<string, string[]> = {};
  if (f.period !== DEFAULT_FILTERS.period) q['period'] = [f.period];
  if (f.agent !== null) q['agent'] = [f.agent];
  if (f.device !== null) q['device'] = [f.device];
  if (f.type !== 'all') q['type'] = [f.type];
  if (f.decisions.length > 0) q['decision'] = [...f.decisions];
  if (f.seq !== null) q['seq'] = [String(f.seq)];
  return q;
}

/** toAuditQuery is the API query of f; serverNow is the server's clock in ms. */
export function toAuditQuery(f: AuditFilters, serverNow: number): AuditQuery {
  const q: AuditQuery = {};
  if (f.period !== 'all') q.since = new Date(serverNow - PERIOD_MS[f.period]).toISOString();
  if (f.agent !== null) q.agent = f.agent;
  if (f.device !== null) q.device = f.device;
  if (f.type === 'decision') q.group = 'decision';
  else if (f.type !== 'all') q.event = f.type;
  if (f.decisions.length > 0) q.decisions = [...f.decisions];
  return q;
}

/** activeCount counts the filters that narrow the list. */
export function activeCount(f: AuditFilters): number {
  return (
    Number(f.period !== DEFAULT_FILTERS.period) +
    Number(f.agent !== null) +
    Number(f.device !== null) +
    Number(f.type !== 'all') +
    Number(f.decisions.length > 0)
  );
}
