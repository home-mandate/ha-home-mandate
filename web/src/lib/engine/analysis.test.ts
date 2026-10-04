// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { Device, MandateDraft, Rule } from '../api/types.ts';
import { cell, criticalIncluded, demotedIn, diff, isWidening, overrides, ruleMatches, validityChange } from './analysis.ts';

const devices: Device[] = [
  { entity_id: 'light.kitchen', name: 'Küchenlicht', category: 'light', area: 'kitchen', actions: ['read', 'set', 'turn_off', 'turn_on'] },
  { entity_id: 'light.bedroom', name: 'Schlafzimmer', category: 'light', area: 'bedroom', actions: ['read', 'set', 'turn_off', 'turn_on'] },
  { entity_id: 'lock.front_door', name: 'Haustür', category: 'lock', area: 'hallway', actions: ['lock', 'open', 'read', 'unlock'] },
  { entity_id: 'cover.garage', name: 'Garagentor', category: 'gate', area: 'garage', actions: ['close', 'open', 'read'] },
];
const [kitchen, bedroom, door, garage] = devices as [Device, Device, Device, Device];

const draft = (rules: Rule[]): MandateDraft => ({
  rules,
  approval: { timeout: 'PT2M', approvers: ['u1'] },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
});

const lights: Rule = { id: 'lights', resource: { category: 'light' }, actions: ['read', 'turn_on', 'turn_off'], decision: 'allow' };
const nightDeny: Rule = {
  id: 'night',
  resource: { category: 'light', area: 'bedroom' },
  actions: ['turn_on'],
  decision: 'deny',
  conditions: { time_window: '22:00-06:00' },
};
const locksAsk: Rule = { id: 'locks', resource: { category: 'lock' }, actions: ['*'], decision: 'ask' };

describe('cell', () => {
  it('separates "no rule" (default) from "a rule forbids" (deny)', () => {
    const m = draft([{ ...lights, actions: ['read'] }, { id: 'no', resource: { entity_id: 'light.kitchen' }, actions: ['set'], decision: 'deny' }]);
    expect(cell(m, kitchen, 'turn_on')).toMatchObject({ decision: 'default', rule: null, timed: null });
    expect(cell(m, kitchen, 'set')).toMatchObject({ decision: 'deny', rule: 1 });
    expect(cell(m, kitchen, 'read')).toMatchObject({ decision: 'allow', rule: 0 });
  });

  it('splits a cell that depends on a time condition', () => {
    const c = cell(draft([lights, nightDeny]), bedroom, 'turn_on');
    expect(c).toMatchObject({ decision: 'allow', rule: 0, timed: { decision: 'deny', rule: 1 } });
    expect(cell(draft([lights, nightDeny]), kitchen, 'turn_on').timed).toBeNull();
  });

  it('marks critical demotion and critical actions', () => {
    const m = draft([{ id: 'all-locks', resource: { category: 'lock' }, actions: ['*'], decision: 'allow' }]);
    expect(cell(m, door, 'unlock')).toMatchObject({ decision: 'ask', demoted: true, critical: true, rule: 0 });
    expect(cell(m, door, 'lock')).toMatchObject({ decision: 'allow', demoted: false, critical: false });
  });

  it('counts the matching rules', () => {
    expect(cell(draft([lights, { ...lights, id: 'again' }]), kitchen, 'read').matching).toBe(2);
  });

  it('shows everything as default for an invalid draft', () => {
    const broken = draft([{ ...lights, actions: [] }]);
    expect(cell(broken, kitchen, 'read')).toMatchObject({ decision: 'default', invalid: true });
  });
});

describe('diff', () => {
  it('lists effective changes per device and action, widening first', () => {
    const before = draft([lights, locksAsk]);
    const after = draft([
      { ...lights, actions: ['read'] },
      { ...locksAsk, actions: ['read', 'lock', 'unlock', 'open'], decision: 'allow', allow_critical: true },
    ]);
    const changes = diff(before, after, devices);
    expect(changes.map((c) => `${c.device.entity_id} ${c.action} ${c.from.decision}→${c.to.decision}`)).toEqual([
      'lock.front_door lock ask→allow',
      'lock.front_door open ask→allow',
      'lock.front_door read ask→allow',
      'lock.front_door unlock ask→allow',
      'light.kitchen turn_off allow→default',
      'light.kitchen turn_on allow→default',
      'light.bedroom turn_off allow→default',
      'light.bedroom turn_on allow→default',
    ]);
    expect(changes.slice(0, 4).every((c) => c.widening)).toBe(true);
    expect(changes.slice(4).some((c) => c.widening)).toBe(false);
  });

  it('counts a change of the time split as a change', () => {
    const changes = diff(draft([lights]), draft([lights, nightDeny]), devices);
    expect(changes).toHaveLength(1);
    expect(changes[0]).toMatchObject({ action: 'turn_on', widening: false });
  });

  it('is empty for equal drafts', () => {
    expect(diff(draft([lights]), draft([lights]), devices)).toEqual([]);
  });
});

describe('validityChange', () => {
  const base = draft([lights]);
  it('is null when the validity is unchanged', () => {
    expect(validityChange(base, { ...base, rules: [] })).toBeNull();
  });

  it('flags an earlier start, a later or removed end as widening', () => {
    expect(validityChange(base, { ...base, valid_from: '2026-09-01T00:00:00Z' })).toEqual({ valid_from: true, expires: false, widening: true });
    const ends = { ...base, expires: '2026-12-01T00:00:00Z' };
    expect(validityChange(ends, { ...ends, expires: '2027-01-01T00:00:00Z' })).toMatchObject({ expires: true, widening: true });
    expect(validityChange(ends, base)).toMatchObject({ expires: true, widening: true });
  });

  it('flags a later start or an added end as narrowing', () => {
    expect(validityChange(base, { ...base, valid_from: '2026-11-01T00:00:00Z' })).toEqual({ valid_from: true, expires: false, widening: false });
    expect(validityChange(base, { ...base, expires: '2026-12-01T00:00:00Z' })).toEqual({ valid_from: false, expires: true, widening: false });
  });
});

describe('cell cache', () => {
  it('returns the same cell for the same draft, and a fresh one for a new draft', () => {
    const m = draft([lights]);
    expect(cell(m, kitchen, 'read')).toBe(cell(m, kitchen, 'read'));
    expect(cell({ ...m }, kitchen, 'read')).not.toBe(cell(m, kitchen, 'read'));
  });
});

describe('isWidening', () => {
  it.each([
    ['default', 'ask', true],
    ['deny', 'allow', true],
    ['ask', 'allow', true],
    ['allow', 'ask', false],
    ['default', 'deny', false],
    ['deny', 'default', false],
  ] as const)('%s → %s is %s', (from, to, want) => {
    expect(isWidening(from, to)).toBe(want);
  });
});

describe('rule hints', () => {
  it('counts the devices a rule matches', () => {
    expect(ruleMatches(draft([lights, nightDeny, locksAsk, { id: 'any', resource: { any: true }, actions: ['read'], decision: 'allow' }]), devices)).toEqual([2, 1, 1, 4]);
  });

  it('finds where a stricter rule overrides a rule, and whether only at times', () => {
    expect(overrides(draft([lights, nightDeny]), devices)).toEqual([
      { by: 1, device: 'light.bedroom', action: 'turn_on', timed: true },
      null,
    ]);
    const always = overrides(draft([lights, { id: 'k', resource: { entity_id: 'light.kitchen' }, actions: ['turn_off'], decision: 'ask' }]), devices);
    expect(always[0]).toEqual({ by: 1, device: 'light.kitchen', action: 'turn_off', timed: false });
  });

  it('lists critical actions an allow rule will still ask for', () => {
    expect(demotedIn({ ...locksAsk, decision: 'allow' })).toEqual([['lock', 'unlock'], ['lock', 'open']]);
    expect(demotedIn({ ...locksAsk, decision: 'allow', allow_critical: true })).toEqual([]);
    expect(demotedIn(locksAsk)).toEqual([]);
    // Without a category the rule can hit any category with a critical "open".
    expect(demotedIn({ id: 'g', resource: { entity_id: 'cover.garage' }, actions: ['open'], decision: 'allow' })).toEqual([
      ['gate', 'open'],
      ['lock', 'open'],
    ]);
  });

  it('lists critical actions included by "*"', () => {
    expect(criticalIncluded(locksAsk)).toEqual([['lock', 'unlock'], ['lock', 'open']]);
    expect(criticalIncluded(lights)).toEqual([]);
    expect(criticalIncluded({ id: 'a', resource: { any: true }, actions: ['*'], decision: 'deny' })).toHaveLength(8);
  });

  it('ignores the garage in light rules', () => {
    expect(cell(draft([lights]), garage, 'open').decision).toBe('default');
  });
});
