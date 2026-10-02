// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { DEFAULT_FILTERS, activeCount, parseFilters, toAuditQuery, toQuery, type AuditFilters } from './filters.ts';

const NOW = Date.parse('2026-10-02T17:42:00Z');

describe('parseFilters', () => {
  it('reads every filter from the URL query', () => {
    const f = parseFilters({
      period: ['24h'],
      agent: ['pair:voice-assistant'],
      device: ['lock.front_door'],
      type: ['decision'],
      decision: ['allow', 'default'],
      seq: ['14'],
    });
    expect(f).toEqual({
      period: '24h',
      agent: 'pair:voice-assistant',
      device: 'lock.front_door',
      type: 'decision',
      decisions: ['allow', 'default'],
      seq: 14,
    } satisfies AuditFilters);
  });

  it('ignores what it does not know instead of guessing', () => {
    expect(
      parseFilters({ period: ['1y'], type: ['drop table'], decision: ['maybe', 'allow', 'allow'], seq: ['-1'], agent: [''], device: [''] }),
    ).toEqual({ ...DEFAULT_FILTERS, decisions: ['allow'] });
    expect(parseFilters({ seq: ['1e3'] }).seq).toBeNull();
    expect(parseFilters({ seq: ['0'] }).seq).toBeNull();
  });

  it('accepts every administrative event type', () => {
    for (const type of ['agent.registered', 'agent.revoked', 'mandate.created', 'mandate.updated', 'mandate.revoked',
      'emergency_stop.activated', 'emergency_stop.released', 'auth.rejected', 'log.truncated']) {
      expect(parseFilters({ type: [type] }).type).toBe(type);
    }
    expect(parseFilters({ type: ['decision'] }).type).toBe('decision');
  });
});

describe('toQuery', () => {
  it('writes only what differs from the defaults, and reads back the same', () => {
    expect(toQuery(DEFAULT_FILTERS)).toEqual({});
    const f: AuditFilters = { period: '7d', agent: 'pair:x', device: 'hallway', type: 'auth.rejected', decisions: ['deny'], seq: 3 };
    expect(parseFilters(toQuery(f))).toEqual(f);
  });
});

describe('toAuditQuery', () => {
  it('turns the period into a start in server time', () => {
    expect(toAuditQuery({ ...DEFAULT_FILTERS, period: '24h' }, NOW).since).toBe('2026-10-01T17:42:00.000Z');
    expect(toAuditQuery({ ...DEFAULT_FILTERS, period: '7d' }, NOW).since).toBe('2026-09-25T17:42:00.000Z');
    expect(toAuditQuery(DEFAULT_FILTERS, NOW).since).toBeUndefined();
  });

  it('maps the type to group or event, and passes agent, device and decisions on', () => {
    expect(toAuditQuery({ ...DEFAULT_FILTERS, type: 'decision' }, NOW)).toMatchObject({ group: 'decision' });
    expect(toAuditQuery({ ...DEFAULT_FILTERS, type: 'emergency_stop.activated' }, NOW)).toMatchObject({ event: 'emergency_stop.activated' });
    const q = toAuditQuery({ ...DEFAULT_FILTERS, agent: 'pair:x', device: 'lock.front_door', decisions: ['ask'] }, NOW);
    expect(q).toMatchObject({ agent: 'pair:x', device: 'lock.front_door', decisions: ['ask'] });
    expect(q.group).toBeUndefined();
    expect(toAuditQuery(DEFAULT_FILTERS, NOW)).toEqual({});
  });
});

describe('activeCount', () => {
  it('counts the filters that narrow the list; the selected entry is no filter', () => {
    expect(activeCount(DEFAULT_FILTERS)).toBe(0);
    expect(activeCount({ ...DEFAULT_FILTERS, seq: 4 })).toBe(0);
    expect(activeCount({ period: '24h', agent: 'a', device: 'd', type: 'decision', decisions: ['allow', 'deny'], seq: null })).toBe(5);
  });
});
