// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from '../api/fixtures.ts';
import type { MandateDraft, Rule } from '../api/types.ts';
import { diff } from '../engine/analysis.ts';
import { setLocale } from '../paraglide/runtime.js';
import { countChanges, effectGroups, ruleChanges, settingChanges } from './changes.ts';
import { appendRule, moveRule, removeRule, replaceRule, withDecision, withExpires, withLimit, withValidFrom, withWindow } from './edit.ts';
import { describeProblems } from './problems.ts';

beforeEach(() => setLocale('en', { reload: false }));

const base = voiceAssistantDraft;
const devices = devicesFixture.devices;
const ruleAt = (d: MandateDraft, i: number) => d.rules[i] as Rule;
const edited = (draft: MandateDraft, name = 'Kitchen') => ({ name, draft });

describe('ruleChanges', () => {
  it('finds nothing in an equal draft, whatever the order of actions', () => {
    const shuffled = replaceRule(base, 0, { ...ruleAt(base, 0), actions: ['set', 'turn_off', 'turn_on', 'read'] });
    expect(ruleChanges(base, structuredClone(base))).toEqual([]);
    expect(ruleChanges(base, shuffled)).toEqual([]);
  });

  it('reports added, changed and removed rules', () => {
    const next = appendRule(removeRule(replaceRule(base, 0, withDecision(ruleAt(base, 0), 'ask')), 4));
    expect(ruleChanges(base, next).map((c) => [c.kind, c.rule.id, c.index])).toEqual([
      ['changed', 'lights', 0],
      ['added', 'rule-1', 4],
      ['removed', 'no-cameras', 4],
    ]);
  });
});

describe('settingChanges', () => {
  it('finds nothing when only the notation of an instant differs', () => {
    const same = withValidFrom(base, '2026-10-01T02:00:00+02:00');
    expect(settingChanges(edited(base), edited(same, ' Kitchen '))).toEqual([]);
  });

  it('lists every changed setting', () => {
    const next = {
      ...withExpires(withLimit(withValidFrom(base, '2026-11-01T00:00:00Z'), 5), '2027-01-01T00:00:00Z'),
      approval: { timeout: 'PT30S', approvers: ['u-partner'] },
    };
    expect(settingChanges(edited(base), edited(next, 'Other'))).toEqual(['name', 'valid_from', 'expires', 'limit', 'timeout', 'approvers']);
  });

  it('sees a new order of the rules both versions share, not additions or removals', () => {
    expect(settingChanges(edited(base), edited(moveRule(base, 0, 2)))).toEqual(['order']);
    expect(settingChanges(edited(base), edited(removeRule(base, 1)))).toEqual([]);
    expect(settingChanges(edited(base), edited(appendRule(base)))).toEqual([]);
  });

  it('counts rule and setting changes together', () => {
    expect(countChanges(edited(base), edited(base))).toBe(0);
    expect(countChanges(edited(base), edited(appendRule(withLimit(base, 5)), 'Other'))).toBe(3);
  });
});

describe('effectGroups', () => {
  it('groups effective changes by their new decision, default as denied', () => {
    const next = removeRule(replaceRule(replaceRule(base, 0, withDecision(ruleAt(base, 0), 'ask')), 2, withDecision(ruleAt(base, 2), 'allow')), 1);
    const groups = effectGroups(diff(base, next, devices));
    expect(groups.map((g) => g.kind)).toEqual(['allow', 'ask', 'deny']);
    const of = (kind: string) => groups.find((g) => g.kind === kind)?.changes.map((c) => `${c.device.entity_id}/${c.action}`);
    // The door's "read" becomes allowed; "unlock" is critical and stays an approval request.
    expect(of('allow')).toEqual(['lock.front_door/read']);
    expect(of('ask')).toContain('light.kitchen/turn_on');
    expect(of('deny')).toEqual(['climate.hvac/read', 'climate.hvac/set_temperature']);
  });

  it('sorts a change of the "at times" outcome by that outcome', () => {
    const nightly: Rule = { id: 'night', resource: { category: 'light', area: 'kitchen' }, actions: ['turn_on'], decision: 'deny', conditions: { time_window: '22:00-06:00' } };
    const groups = effectGroups(diff(base, { ...base, rules: [...base.rules, nightly] }, devices));
    expect(groups.map((g) => g.kind)).toEqual(['deny']);
    expect(groups[0]?.changes.map((c) => [c.device.entity_id, c.action, c.to.decision, c.to.timed?.decision])).toEqual([['light.kitchen', 'turn_on', 'allow', 'deny']]);
  });

  it('leaves out changes that only change the reason, not the permission', () => {
    // Without the camera rule the camera falls back to the default: denied as before.
    expect(diff(base, removeRule(base, 4), devices).length).toBeGreaterThan(0);
    expect(effectGroups(diff(base, removeRule(base, 4), devices))).toEqual([]);
    // A time window on the deny rule: denied at times by the rule, otherwise by default.
    const windowed = replaceRule(base, 4, withWindow(ruleAt(base, 4), '22:00-06:00'));
    expect(effectGroups(diff(base, windowed, devices))).toEqual([]);
  });

  it('lists critical actions first within a group', () => {
    const everything: Rule = { id: 'all', resource: { any: true }, actions: ['*'], decision: 'ask' };
    const [group] = effectGroups(diff({ ...base, rules: [] }, { ...base, rules: [everything] }, devices));
    const critical = group?.changes.map((c) => c.to.critical) ?? [];
    expect(critical.includes(true) && critical.includes(false)).toBe(true);
    expect(critical.lastIndexOf(true)).toBeLessThan(critical.indexOf(false));
  });

  it('has no groups without changes', () => {
    expect(effectGroups([])).toEqual([]);
  });
});

describe('describeProblems', () => {
  const problems = (draft: MandateDraft, name = 'Kitchen') => describeProblems(name, draft).map((p) => [p.rule, p.part, p.text]);

  it('finds nothing in a valid mandate', () => {
    expect(problems(base)).toEqual([]);
  });

  it('checks the display name', () => {
    expect(problems(base, '  ')).toEqual([[null, 'name', 'Enter a name of up to 80 characters.']]);
    expect(problems(base, 'x'.repeat(81))).toHaveLength(1);
    expect(problems(base, 'ü'.repeat(80))).toEqual([]);
  });

  it('explains problems of the settings at their field', () => {
    const draft: MandateDraft = {
      ...base,
      limits: { max_actions_per_hour: 0 },
      approval: { timeout: 'PT5S', approvers: [] },
      valid_from: 'soon',
      expires: '2026-13-01T00:00:00Z',
    };
    expect(problems(draft)).toEqual([
      [null, 'limit', 'The rate limit must be between 1 and 1,000.'],
      [null, 'timeout', 'Timeout must be between 10 seconds and 1 hour.'],
      [null, 'approvers', 'Choose at least one person.'],
      [null, 'valid_from', 'Enter a valid date.'],
      [null, 'expires', 'Enter a valid date.'],
    ]);
    expect(problems(withExpires(base, '2026-09-01T00:00:00Z'))).toEqual([[null, 'expires', '“Valid until” must not be before “Valid from”.']]);
  });

  it('explains problems of a rule at its part, with the action that does not fit', () => {
    const broken: Rule = {
      id: 'broken',
      resource: { category: 'light', area: 'kitchen' },
      actions: ['turn_on', 'unlock'],
      decision: 'ask',
      conditions: { time_window: '07:00-07:00', weekdays: [] },
      approval: { timeout: '', approvers: [] },
    };
    const found = describeProblems('Kitchen', { ...base, rules: [...base.rules, broken] });
    expect(found.map((p) => [p.rule, p.part, p.text])).toEqual([
      [5, 'actions', 'Action “unlock” doesn’t fit “Lights”.'],
      [5, 'timeout', 'Timeout must be between 10 seconds and 1 hour.'],
      [5, 'approvers', 'Choose at least one person.'],
      [5, 'window', 'Start and end of the time window are the same.'],
      [5, 'weekdays', 'Choose at least one weekday.'],
    ]);
    expect(found[0]?.action).toBe('unlock');
  });

  it('explains missing actions, an incomplete window and everything else as an invalid rule', () => {
    const rule = (patch: Partial<Rule>): MandateDraft => ({ ...base, rules: [{ id: 'r', resource: { category: 'light' }, actions: ['read'], decision: 'allow', ...patch }] });
    expect(problems(rule({ actions: [] }))).toEqual([[0, 'actions', 'Choose at least one action.']]);
    expect(problems(rule({ actions: ['read', 'read'] }))).toEqual([[0, 'actions', 'This rule is invalid and can’t be saved like this.']]);
    expect(problems(rule({ conditions: { time_window: '-06:00' } }))).toEqual([[0, 'window', 'Enter the start and end of the time window.']]);
    expect(problems(rule({ conditions: {} }))).toEqual([[0, 'window', 'This rule is invalid and can’t be saved like this.']]);
    expect(problems(rule({ resource: { category: 'Bad Category' as never } }))).toEqual([[0, 'scope', 'This rule is invalid and can’t be saved like this.']]);
    expect(problems(rule({ approval: { timeout: 'PT1M', approvers: ['u-admin'] } }))).toEqual([[0, 'decision', 'This rule is invalid and can’t be saved like this.']]);
    expect(problems(rule({ id: 'not valid' }))).toEqual([[0, 'decision', 'This rule is invalid and can’t be saved like this.']]);
  });

  it('reports too many rules once', () => {
    const many = { ...base, rules: Array.from({ length: 201 }, (_, i): Rule => ({ id: `r${i}`, resource: { category: 'light' }, actions: ['read'], decision: 'ask' })) };
    expect(problems(many)).toEqual([[null, 'rules', 'A mandate can have at most 200 rules.']]);
  });
});
