// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { MandateDraft, Rule } from '../api/types.ts';
import {
  CATEGORIES,
  actionsOf,
  criticalActionsOf,
  grantsCritical,
  isCritical,
  lookupAction,
  needsCriticalConfirmation,
  newRule,
} from './vocabulary.ts';

const draft = (rules: Rule[]): MandateDraft => ({
  rules,
  approval: { timeout: 'PT2M', approvers: ['u1'] },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
});

const unlockAllowed: Rule = {
  id: 'door',
  resource: { entity_id: 'lock.front_door' },
  actions: ['unlock'],
  decision: 'allow',
  allow_critical: true,
};

describe('vocabulary', () => {
  it('lists the 13 categories of SPEC-v0 section 5', () => {
    expect(CATEGORIES).toHaveLength(13);
  });

  it('marks exactly the critical actions of SPEC-v0 section 5', () => {
    const critical = CATEGORIES.flatMap((c) => criticalActionsOf(c).map((a) => `${c}.${a}`));
    expect(critical.sort()).toEqual(
      ['alarm.disarm', 'camera.snapshot', 'gate.open', 'lock.open', 'lock.unlock', 'other.set', 'script.run'].sort(),
    );
  });

  it('returns the actions of a category with read first', () => {
    expect(actionsOf('lock')).toEqual(['read', 'lock', 'unlock', 'open']);
    expect(actionsOf('sensor')).toEqual(['read']);
  });

  it('treats "*" as critical when the category has critical actions', () => {
    expect(isCritical('lock', '*')).toBe(true);
    expect(isCritical('light', '*')).toBe(false);
    expect(isCritical('lock', 'lock')).toBe(false);
  });

  it('treats any rule without a category as possibly critical', () => {
    // { any: true } or an area/entity rule can match a lock.
    expect(isCritical(undefined, 'turn_on')).toBe(true);
    expect(isCritical(undefined, '*')).toBe(true);
  });

  it('never treats read as critical', () => {
    expect(isCritical(undefined, 'read')).toBe(false);
    expect(isCritical('lock', 'read')).toBe(false);
    expect(isCritical('paperless:document', 'read')).toBe(false);
  });

  it('treats unknown extension categories as critical instead of failing', () => {
    expect(isCritical('paperless:document', 'delete')).toBe(true);
    expect(isCritical('paperless:document', '*')).toBe(true);
  });
});

describe('lookupAction (SPEC-v0 section 4 step 0)', () => {
  it('knows categories, actions and critical actions', () => {
    expect(lookupAction('lock', 'unlock')).toEqual({ categoryKnown: true, actionKnown: true, critical: true });
    expect(lookupAction('lock', 'lock')).toEqual({ categoryKnown: true, actionKnown: true, critical: false });
    expect(lookupAction('light', 'unlock')).toEqual({ categoryKnown: true, actionKnown: false, critical: false });
  });

  it('does not know extensions or inherited object keys', () => {
    expect(lookupAction('paperless:document', 'read').categoryKnown).toBe(false);
    expect(lookupAction('toString', 'read').categoryKnown).toBe(false);
    expect(lookupAction('light', 'toString').actionKnown).toBe(false);
    expect(lookupAction('light', '*').actionKnown).toBe(false);
  });
});

describe('newRule (safe defaults)', () => {
  it('starts with read only and ask, the safe middle (design README 6.5)', () => {
    expect(newRule('light', [])).toEqual({
      id: 'rule-1',
      resource: { category: 'light' },
      actions: ['read'],
      decision: 'ask',
    });
    expect(newRule('lock', []).decision).toBe('ask');
  });

  it('never sets allow_critical', () => {
    for (const c of CATEGORIES) expect(newRule(c, [])).not.toHaveProperty('allow_critical');
  });

  it('picks an id not used by the existing rules', () => {
    const existing = [{ ...newRule('light', []), id: 'rule-1' }, { ...newRule('light', []), id: 'rule-3' }];
    expect(newRule('light', existing).id).toBe('rule-2');
  });
});

describe('needsCriticalConfirmation (decision U9)', () => {
  it('is false without allow_critical', () => {
    expect(grantsCritical(draft([{ ...unlockAllowed, allow_critical: undefined }]))).toEqual([]);
    expect(needsCriticalConfirmation(null, draft([]))).toBe(false);
  });

  it('is true for a new draft that grants allow_critical', () => {
    expect(needsCriticalConfirmation(null, draft([unlockAllowed]))).toBe(true);
  });

  it('is false when the base already granted exactly that rule', () => {
    expect(needsCriticalConfirmation(draft([unlockAllowed]), draft([unlockAllowed]))).toBe(false);
  });

  it('is true when an allow_critical rule was widened', () => {
    const widened = { ...unlockAllowed, actions: ['unlock', 'open'] };
    expect(needsCriticalConfirmation(draft([unlockAllowed]), draft([widened]))).toBe(true);
  });

  it('is true when an allow_critical rule moved to another resource', () => {
    const moved = { ...unlockAllowed, resource: { entity_id: 'lock.back_door' } };
    expect(needsCriticalConfirmation(draft([unlockAllowed]), draft([moved]))).toBe(true);
  });

  it('is true when conditions of an allow_critical rule were removed', () => {
    const limited = { ...unlockAllowed, conditions: { time_window: '08:00-18:00' } };
    expect(needsCriticalConfirmation(draft([limited]), draft([unlockAllowed]))).toBe(true);
  });

  it('ignores key order when comparing rules', () => {
    const reordered: Rule = {
      allow_critical: true,
      decision: 'allow',
      actions: ['unlock'],
      resource: { entity_id: 'lock.front_door' },
      id: 'door',
    };
    expect(needsCriticalConfirmation(draft([unlockAllowed]), draft([reordered]))).toBe(false);
  });

  it('lists the granting rules for the confirmation dialog', () => {
    expect(grantsCritical(draft([newRule('light', []), unlockAllowed]))).toEqual([unlockAllowed]);
  });
});
