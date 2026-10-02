// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from './fixtures.ts';
import { evaluateDraft } from './mock-preview.ts';
import type { MandateDraft, PreviewEntry, Rule } from './types.ts';

const TZ = 'Europe/Berlin';
// Friday 2026-10-02, 19:00 in Berlin (CEST, UTC+2).
const EVENING = '2026-10-02T17:00:00Z';

const draftWith = (rules: Rule[], extra: Partial<MandateDraft> = {}): MandateDraft => ({
  ...voiceAssistantDraft,
  rules,
  ...extra,
});

function entry(entries: PreviewEntry[], entityId: string, action: string): PreviewEntry | undefined {
  return entries.find((e) => e.entity_id === entityId && e.action === action);
}

describe('evaluateDraft (mock of SPEC-v0 section 4)', () => {
  it('lists every device action, in catalog order', () => {
    const { entries } = evaluateDraft(voiceAssistantDraft, devicesFixture.devices, EVENING, TZ);
    expect(entries).toHaveLength(devicesFixture.devices.reduce((n, d) => n + d.actions.length, 0));
  });

  it('denies what no rule matches', () => {
    const { entries } = evaluateDraft(draftWith([]), devicesFixture.devices, EVENING, TZ);
    expect(entries.every((e) => e.decision === 'deny' && e.reason === 'no_match' && e.rule_id === null)).toBe(true);
  });

  it('applies the most restrictive matching rule', () => {
    const rules: Rule[] = [
      { id: 'all-lights', resource: { category: 'light' }, actions: ['*'], decision: 'allow' },
      { id: 'kitchen-ask', resource: { area: 'kitchen' }, actions: ['turn_on'], decision: 'ask' },
      { id: 'kitchen-off', resource: { entity_id: 'light.kitchen' }, actions: ['turn_off'], decision: 'deny' },
    ];
    const { entries } = evaluateDraft(draftWith(rules), devicesFixture.devices, EVENING, TZ);
    expect(entry(entries, 'light.kitchen', 'turn_on')).toMatchObject({ decision: 'ask', rule_id: 'kitchen-ask' });
    expect(entry(entries, 'light.kitchen', 'turn_off')).toMatchObject({ decision: 'deny', rule_id: 'kitchen-off' });
    expect(entry(entries, 'light.living_room', 'turn_on')).toMatchObject({ decision: 'allow', rule_id: 'all-lights' });
  });

  it('requires all resource fields to match', () => {
    const rules: Rule[] = [{ id: 'r', resource: { category: 'light', area: 'kitchen' }, actions: ['read'], decision: 'allow' }];
    const { entries } = evaluateDraft(draftWith(rules), devicesFixture.devices, EVENING, TZ);
    expect(entry(entries, 'light.kitchen', 'read')?.decision).toBe('allow');
    expect(entry(entries, 'light.living_room', 'read')?.decision).toBe('deny');
  });

  it('demotes critical allow to ask without allow_critical and marks critical actions', () => {
    const rules: Rule[] = [{ id: 'door', resource: { category: 'lock' }, actions: ['*'], decision: 'allow' }];
    const { entries } = evaluateDraft(draftWith(rules), devicesFixture.devices, EVENING, TZ);
    expect(entry(entries, 'lock.front_door', 'unlock')).toMatchObject({
      decision: 'ask',
      reason: 'critical_demotion',
      rule_id: 'door',
      critical: true,
    });
    expect(entry(entries, 'lock.front_door', 'lock')).toMatchObject({ decision: 'allow', critical: false });
  });

  it('keeps allow on a critical action when every matching allow rule has allow_critical', () => {
    const rules: Rule[] = [
      { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['unlock'], decision: 'allow', allow_critical: true },
      { id: 'locks', resource: { category: 'lock' }, actions: ['*'], decision: 'allow' },
    ];
    const { entries } = evaluateDraft(draftWith(rules), devicesFixture.devices, EVENING, TZ);
    expect(entry(entries, 'lock.front_door', 'unlock')).toMatchObject({ decision: 'ask', rule_id: 'locks' });
    const only = draftWith([rules[0] as Rule]);
    expect(entry(evaluateDraft(only, devicesFixture.devices, EVENING, TZ).entries, 'lock.front_door', 'unlock')).toMatchObject({
      decision: 'allow',
      reason: 'rule',
    });
  });

  it('evaluates time windows in the household time zone, also across midnight', () => {
    const rules: Rule[] = [
      { id: 'night', resource: { category: 'light' }, actions: ['turn_on'], decision: 'allow', conditions: { time_window: '22:00-06:00' } },
    ];
    const at = (iso: string) => entry(evaluateDraft(draftWith(rules), devicesFixture.devices, iso, TZ).entries, 'light.kitchen', 'turn_on');
    expect(at('2026-10-02T19:59:00Z')?.decision).toBe('deny'); // 21:59 Berlin
    expect(at('2026-10-02T20:00:00Z')?.decision).toBe('allow'); // 22:00
    expect(at('2026-10-03T03:59:00Z')?.decision).toBe('allow'); // 05:59
    expect(at('2026-10-03T04:00:00Z')?.decision).toBe('deny'); // 06:00
  });

  it('evaluates weekdays in the household time zone', () => {
    const rules: Rule[] = [{ id: 'fri', resource: { category: 'light' }, actions: ['read'], decision: 'allow', conditions: { weekdays: ['fri'] } }];
    // 2026-10-02T22:30Z is already Saturday 00:30 in Berlin.
    const at = (iso: string) => entry(evaluateDraft(draftWith(rules), devicesFixture.devices, iso, TZ).entries, 'light.kitchen', 'read');
    expect(at('2026-10-02T21:30:00Z')?.decision).toBe('allow');
    expect(at('2026-10-02T22:30:00Z')?.decision).toBe('deny');
  });

  it('denies before valid_from and from expires on', () => {
    const rules: Rule[] = [{ id: 'r', resource: { any: true }, actions: ['read'], decision: 'allow' }];
    const future = draftWith(rules, { valid_from: '2026-10-03T00:00:00Z' });
    const expired = draftWith(rules, { expires: EVENING });
    expect(evaluateDraft(future, devicesFixture.devices, EVENING, TZ).entries[0]).toMatchObject({ decision: 'deny', reason: 'not_yet_valid' });
    expect(evaluateDraft(expired, devicesFixture.devices, EVENING, TZ).entries[0]).toMatchObject({ decision: 'deny', reason: 'expired' });
  });

  it('adds the previous decision when a base draft is given', () => {
    const before = draftWith([]);
    const after = draftWith([{ id: 'r', resource: { entity_id: 'light.kitchen' }, actions: ['read'], decision: 'allow' }]);
    const { entries } = evaluateDraft(after, devicesFixture.devices, EVENING, TZ, before);
    expect(entry(entries, 'light.kitchen', 'read')).toMatchObject({ decision: 'allow', previous: 'deny' });
    expect(entry(entries, 'light.kitchen', 'turn_on')).toMatchObject({ decision: 'deny', previous: 'deny' });
  });

  it('names who is asked for ask, from the rule or else the mandate', () => {
    const own = { timeout: 'PT5M', approvers: ['u-partner'] };
    const rules: Rule[] = [
      { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['unlock'], decision: 'ask', approval: own },
      { id: 'gate', resource: { category: 'gate' }, actions: ['open'], decision: 'allow' },
      { id: 'lights', resource: { category: 'light' }, actions: ['read'], decision: 'allow' },
    ];
    const { entries } = evaluateDraft(draftWith(rules), devicesFixture.devices, EVENING, TZ);
    expect(entry(entries, 'lock.front_door', 'unlock')?.approval).toEqual(own);
    expect(entry(entries, 'cover.garage_door', 'open')).toMatchObject({
      decision: 'ask',
      reason: 'critical_demotion',
      rule_id: 'gate',
      approval: voiceAssistantDraft.approval,
    });
    expect(entry(entries, 'light.kitchen', 'read')).not.toHaveProperty('approval');
  });

  it('reports the reference time and time zone', () => {
    expect(evaluateDraft(draftWith([]), [], EVENING, TZ)).toEqual({ at: EVENING, time_zone: TZ, entries: [] });
  });
});
