// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { DEFAULT_FILTERS, SEARCH_MAX, activeCount, cleanSearch, parseFilters, toAuditQuery, toQuery, type AuditFilters } from './filters.ts';
import vectors from './search-vectors.json' with { type: 'json' };

const NOW = Date.parse('2026-10-02T17:42:00Z');

describe('parseFilters', () => {
  it('reads every filter from the URL query', () => {
    const f = parseFilters({
      period: ['24h'],
      agent: ['pair:voice-assistant'],
      device: ['lock.front_door'],
      q: ['Haustür'],
      type: ['decision'],
      decision: ['allow', 'default'],
      seq: ['14'],
    });
    expect(f).toEqual({
      period: '24h',
      agent: 'pair:voice-assistant',
      device: 'lock.front_door',
      q: 'Haustür',
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

  it('is safe against hostile or doubled values', () => {
    const f = parseFilters({ agent: ['a', 'b'], seq: ['0x1'], decision: ['bogus', 'deny'], period: ['24h', '7d'], device: ['x'.repeat(600)] });
    expect(f).toEqual({ ...DEFAULT_FILTERS, agent: 'a', period: '24h', decisions: ['deny'] });
  });

  it('drops agent and device values that would look different from what they filter', () => {
    expect(parseFilters({ device: ['lock.front\u200B'] }).device).toBeNull();
    expect(parseFilters({ device: ['lock.frоnt_door'] }).device).toBeNull(); // Cyrillic o
    expect(parseFilters({ device: ['Lock.Front door'] }).device).toBeNull();
    expect(parseFilters({ device: ['\u202E'] }).device).toBeNull();
    expect(parseFilters({ device: ['hallway'] }).device).toBe('hallway');
    expect(parseFilters({ agent: ['pair:x\u202E'] }).agent).toBeNull();
    expect(parseFilters({ agent: ['https://claude.ai/oauth/claude-code-client-metadata'] }).agent).toBe(
      'https://claude.ai/oauth/claude-code-client-metadata',
    );
  });

  it('cleans the search text and drops one that is too long', () => {
    expect(parseFilters({ q: ['  Haus\u202Etür \n Flur '] }).q).toBe('Haustür Flur');
    expect(parseFilters({ q: ['\u200B \u0007'] }).q).toBe('');
    expect(parseFilters({ q: ['x'.repeat(SEARCH_MAX)] }).q).toHaveLength(SEARCH_MAX);
    expect(parseFilters({ q: ['x'.repeat(SEARCH_MAX + 1)] }).q).toBe('');
  });

  it('accepts every administrative event type', () => {
    for (const type of ['agent.registered', 'agent.revoked', 'mandate.created', 'mandate.updated', 'mandate.revoked',
      'emergency_stop.activated', 'emergency_stop.released', 'auth.rejected', 'log.truncated', 'log.checkpoint', 'directory.changed',
      'template.changed', 'approver.changed']) {
      expect(parseFilters({ type: [type] }).type).toBe(type);
    }
    expect(parseFilters({ type: ['decision'] }).type).toBe('decision');
  });
});

describe('toQuery', () => {
  it('writes only what differs from the defaults, and reads back the same', () => {
    expect(toQuery(DEFAULT_FILTERS)).toEqual({});
    const f: AuditFilters = { period: '7d', agent: 'pair:x', device: 'hallway', q: 'Tür', type: 'auth.rejected', decisions: ['deny'], seq: 3 };
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
    expect(toAuditQuery({ ...DEFAULT_FILTERS, type: 'template.changed' }, NOW)).toMatchObject({ event: 'template.changed' });
    expect(toAuditQuery({ ...DEFAULT_FILTERS, type: 'approver.changed' }, NOW)).toMatchObject({ event: 'approver.changed' });
    const q = toAuditQuery({ ...DEFAULT_FILTERS, agent: 'pair:x', device: 'lock.front_door', decisions: ['ask'] }, NOW);
    expect(q).toMatchObject({ agent: 'pair:x', device: 'lock.front_door', decisions: ['ask'] });
    expect(q.group).toBeUndefined();
    expect(toAuditQuery(DEFAULT_FILTERS, NOW)).toEqual({});
  });

  it('passes the search text on, and leaves an empty one out', () => {
    expect(toAuditQuery({ ...DEFAULT_FILTERS, q: 'tür' }, NOW)).toEqual({ q: 'tür' });
    expect(toAuditQuery({ ...DEFAULT_FILTERS, q: '' }, NOW)).toEqual({});
  });
});

describe('cleanSearch', () => {
  it('removes control and format characters, joins whitespace and trims', () => {
    expect(cleanSearch(' a\u0000b\u2066c\t\r\nd  ')).toBe('abc d');
    expect(cleanSearch('Garten\u00ADtor')).toBe('Gartentor');
  });

  it('counts characters, not UTF-16 units, against the limit', () => {
    expect(cleanSearch('🚪'.repeat(SEARCH_MAX))).toBe('🚪'.repeat(SEARCH_MAX));
    expect(cleanSearch('🚪'.repeat(SEARCH_MAX + 1))).toBeNull();
    expect(cleanSearch(' '.repeat(10_000))).toBeNull(); // huge input is refused before any work
  });

  // The server cleans with internal/untrusted.CleanSearch; both run these vectors (B2).
  it('cleans like the server (shared vectors)', () => {
    for (const v of vectors.search) expect(cleanSearch(v.input), JSON.stringify(v.input)).toBe(v.clean);
  });
});

describe('activeCount', () => {
  it('counts the filters that narrow the list; the selected entry is no filter', () => {
    expect(activeCount(DEFAULT_FILTERS)).toBe(0);
    expect(activeCount({ ...DEFAULT_FILTERS, seq: 4 })).toBe(0);
    expect(activeCount({ period: '24h', agent: 'a', device: 'd', q: 'x', type: 'decision', decisions: ['allow', 'deny'], seq: null })).toBe(6);
  });
});
