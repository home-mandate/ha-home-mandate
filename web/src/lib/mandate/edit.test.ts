// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from '../api/fixtures.ts';
import type { MandateDraft, Rule } from '../api/types.ts';
import { checkDraft } from '../engine/check.ts';
import {
  appendRule,
  insertRule,
  moveRule,
  removeRule,
  replaceRule,
  toggleAction,
  toggleAllActions,
  toggleWeekday,
  withAllowCritical,
  withApproval,
  withArea,
  withCategory,
  withDecision,
  withDefaults,
  withDevice,
  withExpires,
  withLimit,
  withoutRevokedConfirmations,
  withValidFrom,
  withWindow,
} from './edit.ts';
import { categoryOf, deviceOptions, isEditable, resourceOf, scopeOf, vocabularyOf } from './scope.ts';

const devices = devicesFixture.devices;
const draft = voiceAssistantDraft;
const ids = (d: MandateDraft) => d.rules.map((r) => r.id);
const rule = (patch: Partial<Rule> = {}): Rule => ({ id: 'r', resource: { category: 'lock' }, actions: ['read'], decision: 'allow', ...patch });
const frozen = <T>(value: T): T => Object.freeze(structuredClone(value));

describe('scope', () => {
  it('reads the selects from a resource', () => {
    expect(scopeOf(rule({ resource: { any: true } }), devices)).toEqual({ category: 'all', area: null, device: null });
    expect(scopeOf(rule({ resource: { category: 'light', area: 'kitchen' } }), devices)).toEqual({ category: 'light', area: 'kitchen', device: null });
    expect(scopeOf(rule({ resource: { area: 'kitchen' } }), devices)).toEqual({ category: 'all', area: 'kitchen', device: null });
  });

  it('shows the catalog category of a single device, "all" for an unknown one', () => {
    expect(scopeOf(rule({ resource: { entity_id: 'lock.front_door' } }), devices).category).toBe('lock');
    expect(scopeOf(rule({ resource: { entity_id: 'lock.gone' } }), devices)).toEqual({ category: 'all', area: null, device: 'lock.gone' });
  });

  it('writes the selects back; every scope survives a round trip', () => {
    expect(resourceOf({ category: 'all', area: null, device: null })).toEqual({ any: true });
    expect(resourceOf({ category: 'all', area: 'garage', device: null })).toEqual({ area: 'garage' });
    expect(resourceOf({ category: 'gate', area: null, device: null })).toEqual({ category: 'gate' });
    expect(resourceOf({ category: 'gate', area: 'garage', device: null })).toEqual({ category: 'gate', area: 'garage' });
    expect(resourceOf({ category: 'lock', area: 'hallway', device: 'lock.front_door' })).toEqual({ entity_id: 'lock.front_door', category: 'lock' });
    expect(resourceOf({ category: 'all', area: null, device: 'lock.gone' })).toEqual({ entity_id: 'lock.gone' });
    for (const r of draft.rules) expect(resourceOf(scopeOf(r, devices))).toMatchObject(r.resource);
  });

  it('offers the vocabulary of the scope, only "read" without a known category', () => {
    expect(vocabularyOf(rule(), devices)).toEqual(['read', 'lock', 'unlock', 'open']);
    expect(vocabularyOf(rule({ resource: { entity_id: 'lock.front_door' } }), devices)).toEqual(['read', 'lock', 'unlock', 'open']);
    expect(vocabularyOf(rule({ resource: { any: true } }), devices)).toEqual(['read']);
    expect(vocabularyOf(rule({ resource: { category: 'paperless:document' } }), devices)).toEqual(['read']);
    expect(categoryOf(rule(), devices)).toBe('lock');
    expect(categoryOf(rule({ resource: { area: 'garage' } }), devices)).toBeUndefined();
  });

  it('cannot edit rules on extension categories', () => {
    expect(isEditable(rule())).toBe(true);
    expect(isEditable(rule({ resource: { any: true } }))).toBe(true);
    expect(isEditable(rule({ resource: { category: 'paperless:document' } }))).toBe(false);
  });

  it('lists the devices of a category and area', () => {
    const names = (s: Parameters<typeof deviceOptions>[0]) => deviceOptions(s, devices).map((d) => d.entity_id);
    expect(names({ category: 'light', area: null, device: null })).toEqual(['light.kitchen', 'light.living_room']);
    expect(names({ category: 'light', area: 'kitchen', device: null })).toEqual(['light.kitchen']);
    expect(names({ category: 'all', area: 'garage', device: null })).toEqual(['cover.garage_door', 'camera.demo_camera']);
  });
});

describe('rules of a draft', () => {
  it('never changes its input', () => {
    const d = frozen(draft);
    const first = d.rules[0] as Rule;
    replaceRule(d, 0, rule());
    moveRule(d, 0, 2);
    removeRule(d, 1);
    insertRule(d, 1, rule());
    appendRule(d);
    toggleAction(first, 'set', ['read', 'turn_on', 'turn_off', 'set']);
    toggleWeekday(first, 'mon');
    withWindow(first, '22:00-06:00');
    withCategory(first, 'lock', devices);
    expect(d).toEqual(draft);
  });

  it('replaces, removes and inserts', () => {
    expect(ids(replaceRule(draft, 1, rule()))).toEqual(['lights', 'r', 'door', 'media', 'no-cameras']);
    expect(ids(removeRule(draft, 0))).toEqual(['climate', 'door', 'media', 'no-cameras']);
    expect(ids(insertRule(removeRule(draft, 2), 2, draft.rules[2] as Rule))).toEqual(ids(draft));
  });

  it('moves a rule up and down and ignores moves out of range', () => {
    expect(ids(moveRule(draft, 0, 1))).toEqual(['climate', 'lights', 'door', 'media', 'no-cameras']);
    expect(ids(moveRule(draft, 4, 0))).toEqual(['no-cameras', 'lights', 'climate', 'door', 'media']);
    expect(ids(moveRule(draft, 1, 3))).toEqual(['lights', 'door', 'media', 'climate', 'no-cameras']);
    for (const [from, to] of [[0, 0], [-1, 0], [0, -1], [5, 0], [0, 5]] as const) expect(moveRule(draft, from, to)).toBe(draft);
  });

  it('adds new rules as read-only "ask" with a free id; the draft stays valid', () => {
    const next = appendRule(appendRule(draft), 'lock');
    expect(next.rules.slice(-2)).toEqual([
      { id: 'rule-1', resource: { category: 'light' }, actions: ['read'], decision: 'ask' },
      { id: 'rule-2', resource: { category: 'lock' }, actions: ['read'], decision: 'ask' },
    ]);
    expect(checkDraft(next)).toEqual([]);
  });
});

describe('scope edits', () => {
  it('changes the category and drops a single device', () => {
    expect(withCategory(rule({ resource: { category: 'light', area: 'kitchen' } }), 'switch', devices).resource).toEqual({ category: 'switch', area: 'kitchen' });
    expect(withCategory(rule({ resource: { entity_id: 'lock.front_door', category: 'lock' } }), 'gate', devices).resource).toEqual({ category: 'gate' });
    expect(withCategory(rule(), 'all', devices).resource).toEqual({ any: true });
  });

  it('changes the area', () => {
    expect(withArea(rule(), 'hallway', devices).resource).toEqual({ category: 'lock', area: 'hallway' });
    expect(withArea(rule({ resource: { category: 'lock', area: 'hallway' } }), null, devices).resource).toEqual({ category: 'lock' });
    expect(withArea(rule({ resource: { any: true } }), 'garage', devices).resource).toEqual({ area: 'garage' });
  });

  it('names a single device with its category, and goes back to the category', () => {
    expect(withDevice(rule({ resource: { category: 'lock', area: 'hallway' } }), 'lock.front_door', devices).resource).toEqual({ entity_id: 'lock.front_door', category: 'lock' });
    expect(withDevice(rule({ resource: { any: true } }), 'cover.garage_door', devices).resource).toEqual({ entity_id: 'cover.garage_door', category: 'gate' });
    expect(withDevice(rule({ resource: { any: true } }), 'lock.gone', devices).resource).toEqual({ entity_id: 'lock.gone' });
    expect(withDevice(rule({ resource: { entity_id: 'lock.front_door', category: 'lock' } }), null, devices).resource).toEqual({ category: 'lock' });
  });
});

describe('actions and decision', () => {
  const vocabulary = ['read', 'lock', 'unlock', 'open'];

  it('toggles actions in vocabulary order and keeps what the vocabulary does not know', () => {
    expect(toggleAction(rule({ actions: ['unlock'] }), 'read', vocabulary).actions).toEqual(['read', 'unlock']);
    expect(toggleAction(rule({ actions: ['read', 'unlock'] }), 'unlock', vocabulary).actions).toEqual(['read']);
    expect(toggleAction(rule({ actions: ['dim', 'read'] }), 'open', vocabulary).actions).toEqual(['read', 'open', 'dim']);
    expect(toggleAction(rule({ actions: ['read'] }), 'read', vocabulary).actions).toEqual([]);
  });

  it('leaves all but one action when toggling from "all actions"', () => {
    expect(toggleAction(rule({ actions: ['*'] }), 'unlock', vocabulary).actions).toEqual(['read', 'lock', 'open']);
    expect(toggleAllActions(rule({ actions: ['read', 'lock'] })).actions).toEqual(['*']);
    expect(toggleAllActions(rule({ actions: ['*'] })).actions).toEqual(['read']);
  });

  it('drops what another decision may not carry', () => {
    const approval = { timeout: 'PT30S', approvers: ['u-admin'] };
    const ask = rule({ decision: 'ask', approval });
    expect(withDecision(ask, 'ask')).toBe(ask);
    expect(withDecision(ask, 'allow')).toEqual(rule({ decision: 'allow' }));
    expect(withDecision(rule({ allow_critical: true }), 'ask')).toEqual(rule({ decision: 'ask' }));
    expect(withDecision(rule({ allow_critical: true }), 'deny')).toEqual(rule({ decision: 'deny' }));
    expect(withApproval(rule({ decision: 'ask' }), approval)).toEqual(ask);
    expect(withApproval(ask, null)).toEqual(rule({ decision: 'ask' }));
  });

  it('records the critical confirmation only on allow rules', () => {
    expect(withAllowCritical(rule(), true).allow_critical).toBe(true);
    expect(withAllowCritical(rule({ decision: 'ask' }), true)).toEqual(rule({ decision: 'ask' }));
    expect(withAllowCritical(rule({ allow_critical: true }), false)).toEqual(rule());
  });

  it('takes the critical confirmation away when the rule covers something else', () => {
    const confirmed = rule({ actions: ['read', 'unlock'], allow_critical: true });
    const edits: Rule[] = [
      toggleAction(confirmed, 'open', vocabulary),
      toggleAllActions(confirmed),
      withCategory(confirmed, 'gate', devices),
      withArea(confirmed, 'hallway', devices),
      withDevice(confirmed, 'lock.front_door', devices),
      withWindow(confirmed, '08:00-18:00'),
      toggleWeekday(confirmed, 'sun'),
    ];
    for (const edited of edits) expect(edited.allow_critical).toBeUndefined();
    expect(confirmed.allow_critical).toBe(true);
  });
});

describe('putting an edit on top of a newer version', () => {
  const granted = rule({ id: 'door', actions: ['read', 'unlock'], allow_critical: true });
  const started: MandateDraft = { ...draft, rules: [granted] };

  it('drops a confirmation the newer version took back', () => {
    const newer: MandateDraft = { ...draft, rules: [{ ...granted, decision: 'ask', allow_critical: undefined } as Rule] };
    const mine: MandateDraft = { ...started, limits: { max_actions_per_hour: 5 } };
    const result = withoutRevokedConfirmations(mine, started, newer);
    expect(result.dropped).toBe(true);
    expect(result.draft.rules[0]).toEqual(rule({ id: 'door', actions: ['read', 'unlock'] }));
    expect(mine.rules[0]?.allow_critical).toBe(true);
  });

  it('keeps a confirmation the newer version still has, and one given in this edit', () => {
    const own = rule({ id: 'gate', resource: { category: 'gate' }, actions: ['open'], allow_critical: true });
    const mine: MandateDraft = { ...started, rules: [granted, own] };
    const result = withoutRevokedConfirmations(mine, started, { ...started });
    expect(result).toEqual({ draft: mine, dropped: false });
    // The newer version removed the door rule; the gate rule was confirmed in this edit.
    const removed = withoutRevokedConfirmations(mine, started, { ...draft, rules: [] });
    expect(removed.dropped).toBe(true);
    expect(removed.draft.rules.map((r) => r.allow_critical)).toEqual([undefined, true]);
  });
});

describe('conditions', () => {
  it('sets and removes the time window', () => {
    expect(withWindow(rule(), '22:00-06:00').conditions).toEqual({ time_window: '22:00-06:00' });
    expect(withWindow(rule({ conditions: { time_window: '22:00-06:00' } }), null)).toEqual(rule());
    expect(withWindow(rule({ conditions: { time_window: '22:00-06:00', weekdays: ['mon'] } }), null).conditions).toEqual({ weekdays: ['mon'] });
  });

  it('toggles weekdays; all seven days are no condition', () => {
    const weekdays = toggleWeekday(toggleWeekday(rule(), 'sat'), 'sun');
    expect(weekdays.conditions).toEqual({ weekdays: ['mon', 'tue', 'wed', 'thu', 'fri'] });
    expect(toggleWeekday(rule({ conditions: { weekdays: ['mon'] } }), 'mon').conditions).toEqual({ weekdays: [] });
    expect(toggleWeekday(toggleWeekday(weekdays, 'sun'), 'sat')).toEqual(rule());
    expect(toggleWeekday(rule({ conditions: { weekdays: ['fri'] } }), 'tue').conditions).toEqual({ weekdays: ['tue', 'fri'] });
  });
});

describe('settings of the mandate', () => {
  it('sets defaults, limit and validity', () => {
    const approval = { timeout: 'PT30S', approvers: ['u-partner'] };
    expect(withDefaults(draft, approval).approval).toEqual(approval);
    expect(withLimit(draft, 5).limits).toEqual({ max_actions_per_hour: 5 });
    expect(withValidFrom(draft, '2026-11-01T00:00:00Z').valid_from).toBe('2026-11-01T00:00:00Z');
    const limited = withExpires(draft, '2027-01-01T00:00:00Z');
    expect(limited.expires).toBe('2027-01-01T00:00:00Z');
    expect(withExpires(limited, null)).toEqual(draft);
    expect('expires' in withExpires(limited, null)).toBe(false);
  });
});
