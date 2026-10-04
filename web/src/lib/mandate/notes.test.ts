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
    expect(notes(draftOf(allow, deny), 1)).toEqual([]);
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
