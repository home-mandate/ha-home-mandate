// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from '../api/fixtures.ts';
import type { Device, MandateDraft, Rule } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { removeRule, replaceRule, withDecision } from './edit.ts';
import { cellDetail, cellLabel, cellReason, cellWhy, shortLabel } from './explain.ts';
import { buildRows, cellAt, type MatrixCell } from './matrix.ts';

beforeEach(() => setLocale('en', { reload: false }));

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
    expect(cellWhy({ rule: 2, demoted: true, timed: null, limited: false })).toBe('From Rule 3 · Downgraded');
    expect(cellWhy({ rule: 2, demoted: false, timed: null, limited: true })).toBe('From Rule 3 · Within limits');
    expect(cellWhy({ rule: null, demoted: false, timed: { decision: 'allow', rule: 3, demoted: false } })).toBe('No rule applies · Time-dependent: Allowed');
  });

  it('gives a cell its accessible name', () => {
    expect(cellLabel(device('light.kitchen'), at(base, 'light.kitchen', 'turn_on'))).toBe('\u2068Küchenlicht\u2069, turn on: Allowed. From Rule 1');
    expect(cellLabel(device('lock.front_door'), at(base, 'lock.front_door', 'lock'))).toBe('\u2068Haustür\u2069, lock: Default: denied. No rule applies');
  });

  it('adds what the corner marks show: downgraded and changed', () => {
    const draft = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow'));
    const label = cellLabel(device('lock.front_door'), at(draft, 'lock.front_door', 'unlock'));
    expect(label).toBe('\u2068Haustür\u2069, unlock: Ask first. From Rule 3. Downgraded');
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

  it('explains a downgrade and a change', () => {
    const draft = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow'));
    expect(detail(at(draft, 'lock.front_door', 'unlock'), draft)).toEqual([
      ['rule', 'Rule 3: \u2068Haustür\u2069: read, unlock → Allowed · 1 rule applies'],
      ['critical', 'Downgraded: Becomes an approval request because the action is critical.'],
    ]);
    expect(detail(at(draft, 'lock.front_door', 'read'), draft)[1]).toEqual(['changed', 'Changed since the previous version · v4: Ask first']);
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

  it('shows the former outcome when a rule is gone', () => {
    const draft = removeRule(base, 4);
    expect(detail(at(draft, 'camera.demo_camera', 'snapshot'), draft)).toEqual([
      ['rule', 'No rule applies. No rule applies, so it’s denied. This is always the case and can’t be changed.'],
      ['changed', 'Changed since the previous version · v4: Denied'],
    ]);
  });
});
