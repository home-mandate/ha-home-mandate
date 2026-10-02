// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { MandateDraft, Rule } from '../api/types.ts';
import { checkDraft, parseDateTime, type Problem } from './check.ts';

const valid: MandateDraft = {
  rules: [
    { id: 'lights', resource: { category: 'light' }, actions: ['read', 'turn_on'], decision: 'allow' },
    {
      id: 'door',
      resource: { entity_id: 'lock.front_door', area: 'hallway' },
      actions: ['unlock'],
      decision: 'ask',
      approval: { timeout: 'PT1M30S', approvers: ['u1'] },
      conditions: { time_window: '22:00-06:00', weekdays: ['mon', 'fri'] },
    },
    { id: 'all-read', resource: { any: true }, actions: ['read'], decision: 'allow' },
    { id: 'gate', resource: { category: 'gate' }, actions: ['open'], decision: 'allow', allow_critical: true },
    { id: 'ext', resource: { category: 'paperless:document' }, actions: ['tag'], decision: 'deny' },
  ],
  approval: { timeout: 'PT2M', approvers: ['u1', 'u2'] },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
  expires: '2027-10-01T00:00:00+02:00',
};

const withRule = (patch: Partial<Rule> | Record<string, unknown>): MandateDraft => ({
  ...valid,
  rules: [{ ...valid.rules[0], ...patch } as Rule],
});

const fields = (problems: Problem[]) => problems.map((p) => `${p.field} ${p.code}`);

describe('parseDateTime (as strict as Go time.Parse RFC 3339)', () => {
  it.each(['2026-10-01T00:00:00Z', '2026-10-01T23:59:59.999+14:00', '2024-02-29T12:00:00-05:30', '2026-12-31T23:59:59Z'])('accepts %s', (t) => {
    expect(parseDateTime(t)).not.toBeNaN();
  });

  it.each([
    '2025-02-30T00:00:00Z',
    '2025-02-29T00:00:00Z',
    '2026-04-31T00:00:00Z',
    '2026-13-01T00:00:00Z',
    '2026-00-10T00:00:00Z',
    '2026-10-00T00:00:00Z',
    '2026-10-01T24:00:00Z',
    '2026-10-01T23:60:00Z',
    '2026-10-01T23:59:60Z',
    '2026-10-01T00:00:00+24:00',
    '2026-10-01T00:00:00+01:60',
    '2026-10-01t00:00:00z',
    '2026-10-01T00:00:00',
  ])('rejects %s', (t) => {
    expect(parseDateTime(t)).toBeNaN();
  });
});

describe('checkDraft', () => {
  it('accepts a valid draft', () => {
    expect(checkDraft(valid)).toEqual([]);
  });

  it.each<[string, MandateDraft, string]>([
    ['rate limit 0', { ...valid, limits: { max_actions_per_hour: 0 } }, '/limits/max_actions_per_hour range'],
    ['rate limit 1001', { ...valid, limits: { max_actions_per_hour: 1001 } }, '/limits/max_actions_per_hour range'],
    ['rate limit 1.5', { ...valid, limits: { max_actions_per_hour: 1.5 } }, '/limits/max_actions_per_hour range'],
    ['timeout format', { ...valid, approval: { ...valid.approval, timeout: '2m' } }, '/approval/timeout format'],
    ['timeout 9 s', { ...valid, approval: { ...valid.approval, timeout: 'PT9S' } }, '/approval/timeout range'],
    ['timeout 61 min', { ...valid, approval: { ...valid.approval, timeout: 'PT61M' } }, '/approval/timeout range'],
    ['no approvers', { ...valid, approval: { ...valid.approval, approvers: [] } }, '/approval/approvers required'],
    ['duplicate approver', { ...valid, approval: { ...valid.approval, approvers: ['u1', 'u1'] } }, '/approval/approvers/1 duplicate'],
    ['approver longer than 64 characters', { ...valid, approval: { ...valid.approval, approvers: ['x'.repeat(65)] } }, '/approval/approvers/0 format'],
    ['approver with bidi override', { ...valid, approval: { ...valid.approval, approvers: ['u\u202E1'] } }, '/approval/approvers/0 format'],
    ['valid_from format', { ...valid, valid_from: '2026-10-01' }, '/valid_from format'],
    ['expires before valid_from', { ...valid, expires: '2026-09-30T00:00:00Z' }, '/expires order'],
    ['expires equal valid_from', { ...valid, expires: '2026-10-01T02:00:00+02:00' }, '/expires order'],
    ['rule id format', withRule({ id: 'a b' }), '/rules/0/id format'],
    ['empty resource', withRule({ resource: {} }), '/rules/0/resource required'],
    ['any with category', withRule({ resource: { any: true, category: 'light' } }), '/rules/0/resource any_alone'],
    ['entity id pattern', withRule({ resource: { entity_id: 'Light.Flur' } }), '/rules/0/resource/entity_id format'],
    ['area pattern', withRule({ resource: { area: 'Küche' } }), '/rules/0/resource/area format'],
    ['unknown category', withRule({ resource: { category: 'toaster' } }), '/rules/0/resource/category unknown'],
    ['no actions', withRule({ actions: [] }), '/rules/0/actions required'],
    ['duplicate action', withRule({ actions: ['read', 'read'] }), '/rules/0/actions/1 duplicate'],
    ['action pattern', withRule({ actions: ['Turn-On'] }), '/rules/0/actions/0 format'],
    ['action outside vocabulary', withRule({ actions: ['unlock'] }), '/rules/0/actions/0 vocabulary'],
    ['unknown decision', withRule({ decision: 'maybe' }), '/rules/0/decision unknown'],
    ['allow_critical on ask', withRule({ decision: 'ask', allow_critical: true }), '/rules/0/allow_critical allow_only'],
    ['approval on allow', withRule({ approval: valid.approval }), '/rules/0/approval ask_only'],
    ['rule approval timeout', withRule({ decision: 'ask', approval: { timeout: 'PT0S', approvers: ['u1'] } }), '/rules/0/approval/timeout range'],
    ['empty conditions', withRule({ conditions: {} }), '/rules/0/conditions required'],
    ['window format', withRule({ conditions: { time_window: '8:00-9:00' } }), '/rules/0/conditions/time_window format'],
    ['window 24:00', withRule({ conditions: { time_window: '22:00-24:00' } }), '/rules/0/conditions/time_window format'],
    ['window start = end', withRule({ conditions: { time_window: '08:00-08:00' } }), '/rules/0/conditions/time_window empty'],
    ['no weekdays', withRule({ conditions: { weekdays: [] } }), '/rules/0/conditions/weekdays required'],
    ['unknown weekday', withRule({ conditions: { weekdays: ['monday'] } }), '/rules/0/conditions/weekdays/0 unknown'],
    ['duplicate weekday', withRule({ conditions: { weekdays: ['mon', 'mon'] } }), '/rules/0/conditions/weekdays/1 duplicate'],
  ])('reports %s', (_name, draft, problem) => {
    expect(fields(checkDraft(draft))).toContain(problem);
  });

  it('counts approver length in code points, like the schema', () => {
    const astral = '\u{1F512}'.repeat(64); // 64 code points, 128 UTF-16 units
    expect(checkDraft({ ...valid, approval: { ...valid.approval, approvers: [astral] } })).toEqual([]);
  });

  it('reports duplicate rule ids at the second rule', () => {
    const draft = { ...valid, rules: [valid.rules[0]!, { ...valid.rules[2]!, id: 'lights' }] };
    expect(fields(checkDraft(draft))).toEqual(['/rules/1/id duplicate']);
  });

  it('reports more than 200 rules', () => {
    const rules = Array.from({ length: 201 }, (_, i) => ({ ...valid.rules[0]!, id: `r${i}` }));
    expect(fields(checkDraft({ ...valid, rules }))).toEqual(['/rules too_many']);
  });

  it('reports every problem, not only the first', () => {
    const draft = withRule({ id: '', actions: [] });
    expect(fields(checkDraft(draft))).toEqual(['/rules/0/id format', '/rules/0/actions required']);
  });
});
