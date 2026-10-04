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
    ['entity id pattern', withRule({ resource: { entity_id: 'Light Flur' } }), '/rules/0/resource/entity_id format'],
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

describe('checkDraft: rules of the next specification version', () => {
  const draft = (rule: Record<string, unknown>, extra: Record<string, unknown> = {}): MandateDraft =>
    ({
      rules: [{ id: 'r-1', resource: { category: 'climate' }, actions: ['set_temperature'], decision: 'allow', ...rule }],
      approval: { timeout: 'PT2M', approvers: ['user-1'] },
      limits: { max_actions_per_hour: 10 },
      valid_from: '2026-01-01T00:00:00+01:00',
      ...extra,
    }) as unknown as MandateDraft;
  const fields = (d: MandateDraft) => checkDraft(d).map((p) => `${p.field} ${p.code}`);

  it('accepts opaque identifiers and rejects spaces and non-ASCII', () => {
    for (const entity_id of ['1/2/3', 'Kitchen_Light', 'urn:dev:mac:0024befffe804ff1', 'x'.repeat(255)]) {
      expect(fields(draft({ resource: { entity_id }, actions: ['read'] }))).toEqual([]);
    }
    for (const entity_id of ['Kitchen Light', 'Küche', '', 'x'.repeat(256)]) {
      expect(fields(draft({ resource: { entity_id }, actions: ['read'] }))).toEqual(['/rules/0/resource/entity_id format']);
    }
    expect(fields(draft({ resource: { area: 'Floor-1/Room.2' }, actions: ['read'] }))).toEqual([]);
    expect(fields(draft({ resource: { area: 'a'.repeat(65) }, actions: ['read'] }))).toEqual(['/rules/0/resource/area format']);
  });

  it('checks actions of a rule without category against the whole vocabulary', () => {
    expect(fields(draft({ resource: { entity_id: 'door-1' }, actions: ['unlock', 'turn_on'] }))).toEqual([]);
    expect(fields(draft({ resource: { entity_id: 'door-1' }, actions: ['unlokc'] }))).toEqual(['/rules/0/actions/0 vocabulary']);
    expect(fields(draft({ resource: { any: true }, actions: ['read', 'tag'] }))).toEqual(['/rules/0/actions/1 vocabulary']);
    expect(fields(draft({ resource: { category: 'paperless:document' }, actions: ['tag'] }))).toEqual([]);
  });

  it('rejects allow_critical together with "*"', () => {
    expect(fields(draft({ resource: { category: 'lock' }, actions: ['*'], allow_critical: true }))).toEqual(['/rules/0/allow_critical allow_only']);
    expect(fields(draft({ resource: { category: 'lock' }, actions: ['unlock'], allow_critical: true }))).toEqual([]);
  });

  it('checks constraints', () => {
    const c = (constraints: unknown, rule: Record<string, unknown> = {}) => fields(draft({ constraints, ...rule }));
    expect(c({ temperature: { min: 1600, max: 2300 } })).toEqual([]);
    expect(c({ temperature: { max: 2300 } })).toEqual([]);
    expect(c({ temperature: { min: 2400, max: 2300 } })).toEqual(['/rules/0/constraints/temperature order']);
    expect(c({ temperature: {} })).toEqual(['/rules/0/constraints/temperature required']);
    expect(c({})).toEqual(['/rules/0/constraints required']);
    expect(c({ temperature: { max: 22.5 } })).toEqual(['/rules/0/constraints/temperature format']);
    expect(c({ temperature: { max: '23' } })).toEqual(['/rules/0/constraints/temperature format']);
    expect(c({ temprature: { max: 2300 } })).toEqual(['/rules/0/constraints/temprature unknown']);
    expect(c({ brightness: { max: 50 } })).toEqual(['/rules/0/constraints/brightness unknown']);
    expect(c({ temperature: { max: 2300 } }, { decision: 'deny' })).toEqual(['/rules/0/constraints allow_only']);
    expect(c({ temperature: { max: 2300 } }, { actions: ['set_temperature', 'set_mode'] })).toEqual(['/rules/0/constraints/temperature unknown']);
    expect(c({ temperature: { max: 2300 } }, { actions: ['*'] })).toEqual(['/rules/0/constraints allow_only']);
    expect(c({ position: { min: 20 } }, { resource: { entity_id: 'blind-1' }, actions: ['set_position'] })).toEqual([]);
  });

  it('accepts timeouts with hours and limits the digits', () => {
    const t = (timeout: string) => fields(draft({}, { approval: { timeout, approvers: ['user-1'] } }));
    for (const ok of ['PT1H', 'PT1M30S', 'PT0H59M60S', 'PT10S']) expect(t(ok)).toEqual([]);
    expect(t('PT2H')).toEqual(['/approval/timeout range']);
    for (const bad of ['PT', 'PT5S1M', 'PT000010S', 'P1D', 'PT1.5M']) expect(t(bad)).toEqual(['/approval/timeout format']);
  });

  it('checks timestamps as the specification does', () => {
    const v = (valid_from: string) => fields(draft({}, { valid_from }));
    expect(v('2026-01-01T00:00:00.123456789+01:00')).toEqual([]);
    for (const bad of ['2026-01-01T00:00:00.1234567891+01:00', '0000-01-01T00:00:00Z', '2026-01-01T00:00:00-00:00', '2026-01-01T00:00:00+24:00']) {
      expect(v(bad)).toEqual(['/valid_from format']);
    }
  });

  it('checks approvers as displayed text with the list of the specification', () => {
    const a = (approver: string) => fields(draft({}, { approval: { timeout: 'PT2M', approvers: [approver] } }));
    for (const ok of ['user-1', 'Anna Müller', 'می\u200cخواهم']) expect(a(ok)).toEqual([]);
    for (const bad of [' user', 'user ', 'a\u00a0b', 'a\u202eb', '\u2800', '\u0301a', 'a\u200d', 'a\ufdd0b']) {
      expect(a(bad)).toEqual(['/approval/approvers/0 format']);
    }
  });
});
