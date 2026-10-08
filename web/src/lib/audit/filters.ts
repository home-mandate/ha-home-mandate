// SPDX-License-Identifier: AGPL-3.0-or-later

// Filters of the audit log (design README 6.8). They live in the URL query, so a filtered
// view can be bookmarked and shared; they replace the current history entry (no step per
// filter change). Anything unknown in the URL is ignored, never guessed. The period is
// counted back from the server's clock. The search text (q) is matched by the server, case-
// insensitively, against device, area and agent names and IDs.

import type { AuditEvent, AuditQuery, DecisionFilter } from '../api/types.ts';
import { cleanUntrusted } from '../untrusted.ts';

export type Period = '24h' | '7d' | 'all';
export type TypeFilter = 'all' | AuditEvent;

export interface AuditFilters {
  period: Period;
  /** OAuth client ID of an agent. */
  agent: string | null;
  /** Exact entity_id or area_id (links, bookmarks). */
  device: string | null;
  /** Search text, cleaned; empty for none. */
  q: string;
  type: TypeFilter;
  decisions: DecisionFilter[];
  /** Selected entry (desktop detail column); no filter. */
  seq: number | null;
}

export const DEFAULT_FILTERS: AuditFilters = { period: 'all', agent: null, device: null, q: '', type: 'all', decisions: [], seq: null };

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
  'log.checkpoint',
  'directory.changed',
  'template.changed',
  'approver.changed',
];

const HOUR_MS = 3_600_000;
const PERIOD_MS: Record<Exclude<Period, 'all'>, number> = { '24h': 24 * HOUR_MS, '7d': 7 * 24 * HOUR_MS };
const SEQ = /^[1-9]\d{0,14}$/;
const MAX_ID = 512;
/** Longest search text in characters; the server rejects longer ones. */
export const SEARCH_MAX = 100;

// Same classes as for untrusted text: controls and format characters (bidi, zero-width,
// soft hyphen) go, any whitespace run becomes one space.
const HIDDEN = /[\p{Cc}\p{Cf}]/gu;
const SPACES = /[\s\u2028\u2029]+/gu;

/** cleanSearch is the search text as sent to the server, or null when it is too long. */
export function cleanSearch(text: string): string | null {
  if (text.length > MAX_ID) return null; // no work on huge input; far above the limit anyway
  const clean = text.replace(/[\t\r\n\u0085]/g, ' ').replace(HIDDEN, '').replace(SPACES, ' ').trim();
  return [...clean].length <= SEARCH_MAX ? clean : null;
}

// entity_id or area_id as Home Assistant forms them.
const DEVICE_ID = /^[a-z0-9_]+(\.[a-z0-9_]+)?$/;

function one(query: Record<string, string[]>, key: string): string | null {
  const value = query[key]?.[0];
  return value && value.length <= MAX_ID ? value : null;
}

/**
 * shown is a filter value that looks exactly like what it filters: one with hidden
 * characters (which the display removes) is dropped, so a crafted link cannot show a
 * harmless-looking filter that matches something else. Dropping widens the list.
 */
function shown(value: string | null, valid: (v: string) => boolean = () => true): string | null {
  return value !== null && cleanUntrusted(value) === value && valid(value) ? value : null;
}

function includes<T extends string>(list: readonly T[], value: string | null): value is T {
  return value !== null && (list as readonly string[]).includes(value);
}

export function parseFilters(query: Record<string, string[]>): AuditFilters {
  const period = one(query, 'period');
  const type = one(query, 'type');
  const seq = one(query, 'seq');
  // Not through one(): cleanSearch has its own, smaller limit.
  const q = query['q']?.[0];
  const decisions = DECISIONS.filter((d) => query['decision']?.includes(d));
  return {
    period: includes(PERIODS, period) ? period : DEFAULT_FILTERS.period,
    agent: shown(one(query, 'agent')),
    device: shown(one(query, 'device'), (v) => DEVICE_ID.test(v)),
    q: q === undefined ? '' : (cleanSearch(q) ?? ''),
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
  if (f.q !== '') q['q'] = [f.q];
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
  if (f.q !== '') q.q = f.q;
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
    Number(f.q !== '') +
    Number(f.type !== 'all') +
    Number(f.decisions.length > 0)
  );
}
