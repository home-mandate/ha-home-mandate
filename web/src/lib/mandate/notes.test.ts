// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from '../api/fixtures.ts';
import type { MandateDraft, Rule } from '../api/types.ts';
import { overrides } from '../engine/analysis.ts';
import { setLocale } from '../paraglide/runtime.js';
import { criticalDevices, demotedNames, includedNames, ruleNotes } from './notes.ts';

beforeEach(() => setLocale('en', { reload: false }));

const catalog = devicesFixture;
const rule = (patch: Partial<Rule>): Rule => ({ id: 'r', resource: { category: 'lock' }, actions: ['*'], decision: 'allow', ...patch });
const draftOf = (...rules: Rule[]): MandateDraft => ({ ...voiceAssistantDraft, rules });
const notes = (draft: MandateDraft, index: number) => {
  const override = overrides(draft, catalog.devices)[index] ?? null;
  return ruleNotes(draft, index, catalog, override, 'en').map((n) => [n.kind, n.text]);
};

describe('ruleNotes', () => {
  it('has nothing to say about a plain rule or a missing one', () => {
    expect(notes(voiceAssistantDraft, 0)).toEqual([]);
    expect(notes(voiceAssistantDraft, 99)).toEqual([]);
  });

  it('says which critical actions an allow rule still asks for', () => {
    expect(notes(draftOf(rule({})), 0)).toEqual([['critical', 'Becomes an approval request because the action is critical. (unlock, open)']]);
  });

  it('warns permanently once critical actions are allowed without approval', () => {
    expect(notes(draftOf(rule({ allow_critical: true })), 0)).toEqual([['danger', 'Critical actions allowed without approval']]);
  });

  it('names the stricter rule that wins, with its time window if it has one', () => {
    const allow = rule({ id: 'a', resource: { category: 'light' }, actions: ['turn_on'] });
    const deny = rule({ id: 'd', resource: { category: 'light', area: 'kitchen' }, actions: ['turn_on'], decision: 'deny' });
    expect(notes(draftOf(allow, deny), 0)).toEqual([['info', 'For “turn on”, rule 2 overrides this – the stricter decision wins.']]);
    const nightly = { ...deny, conditions: { time_window: '22:00-06:00' } };
    const [note] = notes(draftOf(allow, nightly), 0);
    expect(note?.[1]).toMatch(/^For “turn on”, rule 2 overrides this at times \(10:00\sPM to 06:00\sAM the next day\)\.$/);
    // The deny rule on an area only gets the reminder that it follows the area.
    expect(notes(draftOf(allow, deny), 1).map(([kind]) => kind)).toEqual(['info']);
  });

  it('explains why a rule on an extension category cannot be edited', () => {
    const extension = rule({ resource: { category: 'paperless:document' }, actions: ['read'] });
    expect(notes(draftOf(extension), 0)).toEqual([['info', 'This rule uses an extension category. It can’t be edited here, only moved or deleted.']]);
  });
});

describe('critical actions of a rule', () => {
  it('names them once, whatever the confirmation says', () => {
    const devices = catalog.devices;
    expect(demotedNames(rule({ resource: { any: true } }), devices, 'en')).toBe('open, unlock, disarm, get snapshot, activate, run, set');
    expect(demotedNames(rule({ allow_critical: true }), devices, 'en')).toBe('unlock, open');
    expect(demotedNames(rule({ actions: ['read'] }), devices, 'en')).toBe('');
    expect(demotedNames(rule({ decision: 'ask' }), devices, 'en')).toBe('');
  });

  it('names what "all actions" includes', () => {
    const devices = catalog.devices;
    expect(includedNames(rule({ decision: 'deny' }), devices, 'en')).toBe('unlock, open');
    expect(includedNames(rule({ actions: ['read', 'unlock'] }), devices, 'en')).toBe('');
    expect(includedNames(rule({ resource: { category: 'light' } }), devices, 'en')).toBe('');
  });

  it('names only the critical actions of a single device\'s category', () => {
    const devices = catalog.devices;
    const door = rule({ resource: { entity_id: 'lock.front_door' } });
    expect(includedNames(door, devices, 'en')).toBe('unlock, open');
    expect(demotedNames(door, devices, 'en')).toBe('unlock, open');
    expect(notes(draftOf(door), 0)).toEqual([['critical', 'Becomes an approval request because the action is critical. (unlock, open)']]);
    // A device the catalog does not know could be anything.
    expect(includedNames(rule({ resource: { entity_id: 'lock.gone' } }), devices, 'en')).toBe('open, unlock, disarm, get snapshot, activate, run, set');
  });

  it('counts the devices on which the rule covers a critical action', () => {
    const ids = (r: Rule) => criticalDevices(r, catalog.devices).map((d) => d.entity_id);
    expect(ids(rule({}))).toEqual(['lock.front_door']);
    expect(ids(rule({ actions: ['read'] }))).toEqual([]);
    expect(ids(rule({ resource: { area: 'garage' } }))).toEqual(['cover.garage_door', 'camera.demo_camera']);
    expect(ids(rule({ resource: { any: true } }))).toHaveLength(6);
    // A device the household marked counts for every action but read.
    expect(ids(rule({ resource: { entity_id: 'switch.cellar_door' }, actions: ['turn_on'] }))).toEqual(['switch.cellar_door']);
    expect(ids(rule({ resource: { entity_id: 'switch.cellar_door' }, actions: ['read'] }))).toEqual([]);
  });
});

describe('rules on devices and areas Home Assistant does not have', () => {
  const unknown = (draft: MandateDraft, index: number, known = true) =>
    ruleNotes(draft, index, catalog, null, 'en', known).map((n) => [n.kind, n.text]);

  it('warns that a renamed device is no longer protected by a deny or ask rule', () => {
    const deny = rule({ resource: { entity_id: 'lock.cellar' }, actions: ['unlock'], decision: 'deny' });
    expect(unknown(draftOf(deny), 0)).toEqual([
      ['danger', '“lock.cellar” no longer exists in Home Assistant. If it was renamed, this rule no longer protects it. Choose the device again.'],
    ]);
    expect(unknown(draftOf({ ...deny, decision: 'ask' }), 0)[0]?.[0]).toBe('danger');
  });

  it('says that an allow rule on a missing device applies to nothing', () => {
    const allow = rule({ resource: { entity_id: 'light.gone' }, actions: ['turn_on'] });
    expect(unknown(draftOf(allow), 0)).toEqual([['info', '“light.gone” no longer exists in Home Assistant. This rule applies to nothing until you choose the device again.']]);
  });

  it('reports a removed area the same way', () => {
    const deny = rule({ resource: { area: 'cellar' }, actions: ['*'], decision: 'deny' });
    expect(unknown(draftOf(deny), 0)).toEqual([
      ['danger', 'The area “cellar” no longer exists in Home Assistant. This rule no longer protects anything. Choose the area again.'],
    ]);
    const allow = rule({ resource: { area: 'cellar', category: 'light' }, actions: ['turn_on'] });
    expect(unknown(draftOf(allow), 0)).toEqual([['info', 'The area “cellar” no longer exists in Home Assistant. This rule applies to nothing until you choose the area again.']]);
  });

  it('says nothing about missing devices while the catalog is not loaded', () => {
    const deny = rule({ resource: { entity_id: 'lock.cellar' }, actions: ['unlock'], decision: 'deny' });
    expect(unknown(draftOf(deny), 0, false)).toEqual([]);
  });

  it('reminds that a deny or ask rule on an area follows the area, not the device', () => {
    const hint = 'This rule covers the devices that are in this area now. A device moved to another area is no longer covered.';
    expect(unknown(draftOf(rule({ resource: { area: 'garage' }, actions: ['open'], decision: 'deny' })), 0)).toEqual([['info', hint]]);
    expect(unknown(draftOf(rule({ resource: { area: 'hallway', category: 'lock' }, actions: ['unlock'], decision: 'ask' })), 0)).toEqual([['info', hint]]);
    // An allow rule on an area does not protect anything, a rule on a device does not follow the area.
    expect(unknown(draftOf(rule({ resource: { area: 'kitchen', category: 'light' }, actions: ['turn_on'] })), 0)).toEqual([]);
    expect(unknown(draftOf(rule({ resource: { entity_id: 'lock.front_door' }, actions: ['unlock'], decision: 'deny' })), 0)).toEqual([]);
  });
});
