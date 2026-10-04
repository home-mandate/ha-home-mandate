// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { MandateDraft } from '../api/types.ts';
import { evaluate, type EvalRequest } from './evaluate.ts';

// The cases of the pinned mandate-spec version, copied by tools/webconformance (a Go test
// fails if the copy differs).
const files = import.meta.glob<unknown>('./conformance/**/*.json', { eager: true, import: 'default' });

interface ConformanceCase {
  id: string;
  mandate?: string;
  mandate_inline?: MandateDraft;
  resource: EvalRequest['resource'];
  action: string;
  parameters?: Record<string, number>;
  time: string;
  timezone?: string;
  revoked?: boolean;
  expected: string;
  reason: string;
  rule_id?: string | null;
  approval_timeout?: string;
}

const casesFile = files['./conformance/conformance/cases-v0.json'] as { cases: ConformanceCase[] };

function mandateOf(c: ConformanceCase): MandateDraft {
  if (c.mandate_inline) return c.mandate_inline;
  const m = files[`./conformance/${c.mandate}`];
  if (!m) throw new Error(`case ${c.id}: mandate ${c.mandate} not copied`);
  return m as MandateDraft;
}

describe('evaluate: mandate-spec conformance cases', () => {
  it('has all cases of the pinned version', () => {
    expect(casesFile.cases.length).toBeGreaterThanOrEqual(132);
  });

  it.each(casesFile.cases.map((c) => [c.id, c] as const))('%s', (_id, c) => {
    const result = evaluate(mandateOf(c), {
      resource: c.resource,
      action: c.action,
      time: c.time,
      ...(c.parameters === undefined ? {} : { parameters: c.parameters }),
      ...(c.timezone === undefined ? {} : { timezone: c.timezone }),
      ...(c.revoked === undefined ? {} : { revoked: c.revoked }),
    });
    expect(result.decision).toBe(c.expected);
    expect(result.reason).toBe(c.reason);
    if (c.rule_id !== undefined) expect(result.rule_id).toBe(c.rule_id);
    if (c.approval_timeout !== undefined) expect(result.approval?.timeout).toBe(c.approval_timeout);
  });
});

const base: MandateDraft = {
  rules: [
    { id: 'lights', resource: { category: 'light' }, actions: ['*'], decision: 'allow' },
    {
      id: 'night',
      resource: { category: 'light' },
      actions: ['turn_on'],
      decision: 'ask',
      conditions: { time_window: '22:00-06:00' },
      approval: { timeout: 'PT5M', approvers: ['u2'] },
    },
  ],
  approval: { timeout: 'PT2M', approvers: ['u1'] },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
};

const light = (time: string, timezone?: string): EvalRequest => ({
  resource: { entity_id: 'light.flur', category: 'light', area: 'flur' },
  action: 'turn_on',
  time,
  ...(timezone === undefined ? {} : { timezone }),
});

describe('evaluate: own edge cases', () => {
  it('uses the offset of the time when no time zone is given', () => {
    // 23:30 at +02:00 is inside the night window, although it is 21:30 in UTC.
    expect(evaluate(base, light('2026-10-12T23:30:00+02:00')).decision).toBe('ask');
    expect(evaluate(base, light('2026-10-12T21:30:00Z')).decision).toBe('allow');
  });

  it('uses the household time zone, including the DST change on 2026-10-25', () => {
    // 20:30Z is 22:30 CEST the day before the change (inside the window), but only
    // 21:30 CET the evening after it (outside).
    expect(evaluate(base, light('2026-10-24T20:30:00Z', 'Europe/Berlin')).decision).toBe('ask');
    expect(evaluate(base, light('2026-10-25T20:30:00Z', 'Europe/Berlin')).decision).toBe('allow');
    expect(evaluate(base, light('2026-10-25T21:00:00Z', 'Europe/Berlin')).decision).toBe('ask');
  });

  it('takes the approval of the first matching ask rule with its own', () => {
    expect(evaluate(base, light('2026-10-12T23:30:00+02:00')).approval).toEqual({ timeout: 'PT5M', approvers: ['u2'] });
  });

  it('returns a copy of the approval settings, never the object of the mandate', () => {
    const demoting: MandateDraft = { ...base, rules: [{ id: 'l', resource: { category: 'lock' }, actions: ['*'], decision: 'allow' }] };
    const result = evaluate(demoting, { resource: { entity_id: 'lock.door', category: 'lock' }, action: 'unlock', time: '2026-10-12T12:00:00Z' });
    expect(result.approval).toEqual(base.approval);
    expect(result.approval).not.toBe(base.approval);
    expect(result.approval?.approvers).not.toBe(base.approval.approvers);
  });

  it('returns no approval for allow and deny', () => {
    expect(evaluate(base, light('2026-10-12T12:00:00Z'))).not.toHaveProperty('approval');
  });

  it.each(['UTC', 'Europe/Berlin', 'America/Argentina/Buenos_Aires', 'Etc/GMT+5'])('accepts the zone %s', (zone) => {
    expect(evaluate(base, light('2026-10-12T12:00:00Z', zone)).reason).not.toBe('invalid_request');
  });

  it.each(['CET', 'Local', 'localtime', 'europe/berlin', 'Europe/', '/Berlin', 'Europe/../Berlin', 'Mars/Olympus_Mons', 'Europe/Berlin\u0000', ''])(
    'refuses the zone %j',
    (zone) => {
      const result = evaluate(base, { ...light('2026-10-12T12:00:00Z'), timezone: zone });
      // An empty zone means "use the offset" (SPEC-v0 section 8), everything else is invalid.
      expect(result.reason).toBe(zone === '' ? 'rule' : 'invalid_request');
    },
  );

  it.each(['2026-10-12 12:00:00Z', '2026-10-12T12:00:00', '2026-13-12T12:00:00Z', 'yesterday', ''])('refuses the time %j', (time) => {
    expect(evaluate(base, light(time))).toEqual({ decision: 'deny', reason: 'invalid_request', rule_id: null });
  });

  it('refuses an invalid draft as a whole', () => {
    const broken: MandateDraft = { ...base, rules: [{ ...base.rules[0]!, conditions: { time_window: '08:00-08:00' } }] };
    expect(evaluate(broken, light('2026-10-12T12:00:00Z'))).toEqual({ decision: 'deny', reason: 'invalid_mandate', rule_id: null });
  });

  it('never matches inherited object keys as categories', () => {
    const req: EvalRequest = { resource: { entity_id: 'light.flur', category: 'constructor' }, action: 'read', time: '2026-10-12T12:00:00Z' };
    expect(evaluate(base, req).reason).toBe('unknown_category');
  });
});
