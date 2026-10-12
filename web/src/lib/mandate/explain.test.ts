// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from '../api/fixtures.ts';
import type { Device, MandateDraft, Rule } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { removeRule, replaceRule, withDecision } from './edit.ts';
import { cellDetail, cellLabel, cellReason, cellWhy, shortLabel } from './explain.ts';
import { buildRows, cellAt, type MatrixCell } from './matrix.ts';
import { setHouseholdCountry } from './week.svelte.ts';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => setHouseholdCountry(null));

const base = voiceAssistantDraft;
const catalog = devicesFixture;
const device = (id: string) => catalog.devices.find((d) => d.entity_id === id) as Device;

function at(draft: MandateDraft, entityId: string, action: string, previous = base): MatrixCell {
  const row = buildRows(draft, previous, catalog.devices).find((r) => r.device.entity_id === entityId);
  const found = row && cellAt(row, action);
  if (!found) throw new Error(`no cell ${entityId}/${action}`);
  return found;
}

const detail = (c: MatrixCell, draft = base) => cellDetail(c, draft, catalog, 4, 'en').map((l) => [l.kind, l.text]);

describe('words in and about a cell', () => {
  it('abbreviates the default inside a cell', () => {
    expect(shortLabel('default')).toBe('Default');
    expect(shortLabel('ask')).toBe('Ask first');
  });

  it('names the deciding rule or that none applies', () => {
    expect(cellReason({ rule: 2 })).toBe('From Rule 3');
    expect(cellReason({ rule: null })).toBe('No rule applies');
  });

  it('gives the reason and the marks in one line for mobile', () => {
    expect(cellWhy({ rule: 2, demoted: false, timed: null, limited: false })).toBe('From Rule 3');
    expect(cellWhy({ rule: 2, demoted: true, timed: null, limited: false })).toBe('From Rule 3 · Asks because critical');
    expect(cellWhy({ rule: 2, demoted: false, timed: null, limited: true })).toBe('From Rule 3 · Within limits');
    expect(cellWhy({ rule: null, demoted: false, limited: false, timed: { decision: 'allow', rule: 3, demoted: false } })).toBe('No rule applies · Time-dependent: Allowed');
  });

  it('gives a cell its accessible name', () => {
    expect(cellLabel(device('light.kitchen'), at(base, 'light.kitchen', 'turn_on'))).toBe('\u2068Küchenlicht\u2069, turn on: Allowed. From Rule 1');
    expect(cellLabel(device('lock.front_door'), at(base, 'lock.front_door', 'lock'))).toBe('\u2068Haustür\u2069, lock: Default: denied. No rule applies');
  });

  it('adds what the corner marks show: asks because critical and changed', () => {
    const draft = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow'));
    const label = cellLabel(device('lock.front_door'), at(draft, 'lock.front_door', 'unlock'));
    expect(label).toBe('\u2068Haustür\u2069, unlock: Ask first. From Rule 3. Asks because critical');
    expect(cellLabel(device('lock.front_door'), at(draft, 'lock.front_door', 'read'))).toMatch(/: Allowed\. From Rule 3\. Changed since the previous version$/);
  });

  it('says both outcomes of a cell that depends on time', () => {
    expect(cellLabel(device('media_player.living_room'), at(base, 'media_player.living_room', 'play'))).toMatch(/: Default: denied, at times Allowed\. No rule applies$/);
  });
});

describe('cellDetail', () => {
  it('explains the deciding rule and how many rules matched', () => {
    expect(detail(at(base, 'light.kitchen', 'turn_on'))).toEqual([['rule', 'Rule 1: \u2068Lights\u2069: read, turn on, turn off, adjust → Allowed · 1 rule applies']]);
  });

  it('explains the default', () => {
    expect(detail(at(base, 'lock.front_door', 'lock'))).toEqual([
      ['rule', 'No rule applies. No rule applies, so it’s denied. This is always the case and can’t be changed.'],
    ]);
  });

  it('explains an action that asks because it is critical, and a change', () => {
    const draft = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow'));
    expect(detail(at(draft, 'lock.front_door', 'unlock'), draft)).toEqual([
      ['rule', 'Rule 3: \u2068Haustür\u2069: read, unlock → Allowed · 1 rule applies'],
      ['critical', 'Asks because critical: Critical action – asks first, although a rule allows it.'],
    ]);
    expect(detail(at(draft, 'lock.front_door', 'read'), draft)[1]).toEqual(['changed', 'Changed since the previous version · v4: Ask first']);
  });

  it('explains limits: values outside them are left to another rule or the default, not denied (#41)', () => {
    const limited: MandateDraft = {
      ...base,
      rules: [{ id: 'heat', resource: { category: 'climate' }, actions: ['set_temperature'], decision: 'allow', constraints: { temperature: { min: 1600, max: 2300 } } }],
    };
    const [, limits] = detail(at(limited, 'climate.hvac', 'set_temperature'), limited);
    expect(limits?.[0]).toBe('limits');
    expect(limits?.[1]).toMatch(/^Allowed only for values within the limits \(.+\); other values are decided by another rule or the default\.$/);
    setLocale('de', { reload: false });
    const [, grenzen] = cellDetail(at(limited, 'climate.hvac', 'set_temperature'), limited, catalog, 4, 'de').map((l) => [l.kind, l.text]);
    expect(grenzen?.[1]).toMatch(/^Nur für Werte innerhalb der Grenzen erlaubt \(.+\); andere Werte entscheidet eine andere Regel oder die Voreinstellung\.$/);
  });

  it('says in German that a critical action asks first', () => {
    setLocale('de', { reload: false });
    const draft = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow'));
    const c = at(draft, 'lock.front_door', 'unlock');
    expect(cellWhy(c.cell)).toBe('Aus Regel 3 · Rückfrage, weil kritisch');
    expect(cellDetail(c, draft, catalog, 4, 'de')[1]).toEqual({ kind: 'critical', text: 'Rückfrage, weil kritisch: Kritische Aktion – fragt nach, obwohl eine Regel sie erlaubt.' });
  });

  it('warns when a critical action is allowed without approval', () => {
    const confirmed: Rule = { ...(base.rules[2] as Rule), decision: 'allow', allow_critical: true };
    const draft = replaceRule(base, 2, confirmed);
    expect(detail(at(draft, 'lock.front_door', 'unlock'), draft).slice(1)).toEqual([
      ['danger', 'Critical actions allowed without approval'],
      ['changed', 'Changed since the previous version · v4: Ask first'],
    ]);
    // "read" is not critical: no warning there.
    expect(detail(at(draft, 'lock.front_door', 'read'), draft).map((l) => l[0])).toEqual(['rule', 'changed']);
  });

  it('explains what applies at times and otherwise', () => {
    const [rule, timed] = detail(at(base, 'media_player.living_room', 'play'));
    // Nothing applies all day, so the first line is the default.
    expect(rule?.[0]).toBe('rule');
    expect(rule?.[1]).toMatch(/^No rule applies\./);
    expect(timed?.[0]).toBe('timed');
    expect(timed?.[1]).toMatch(/^In time window 07:00\sAM to 10:00\sPM: Allowed \(Rule 4\)\. Otherwise: Default: denied\.$/);
  });

  it("names the days in the order of the household's week (#43)", () => {
    const media = base.rules[3] as Rule;
    const draft = replaceRule(base, 3, { ...media, conditions: { ...media.conditions, weekdays: ['mon', 'tue', 'sun'] } });
    const timed = () => detail(at(draft, 'media_player.living_room', 'play'), draft)[1]?.[1];
    expect(timed()).toMatch(/^In time window Mon, Tue, Sun · 07:00\sAM/);
    setHouseholdCountry('US');
    expect(timed()).toMatch(/^In time window Sun\s–\sTue · 07:00\sAM/);
  });

  it('shows the former outcome when a rule is gone', () => {
    const draft = removeRule(base, 4);
    expect(detail(at(draft, 'camera.demo_camera', 'snapshot'), draft)).toEqual([
      ['rule', 'No rule applies. No rule applies, so it’s denied. This is always the case and can’t be changed.'],
      ['changed', 'Changed since the previous version · v4: Denied'],
    ]);
  });
});
