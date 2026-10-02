// SPDX-License-Identifier: AGPL-3.0-or-later

// Vocabulary v0 (mandate-spec SPEC-v0 section 5) for the mandate editor: which actions a
// category has, which are critical, and the safe defaults for new rules. The server is
// the authority (GET api/devices lists each device's actions, and it enforces U9 itself);
// this copy only drives the editor.

import type { Category, Decision, ExtensionCategory, MandateDraft, Rule } from './types.ts';

interface CategorySpec {
  actions: readonly string[];
  critical: readonly string[];
}

const VOCABULARY: Readonly<Record<Category, CategorySpec>> = {
  light: { actions: ['read', 'turn_on', 'turn_off', 'set'], critical: [] },
  switch: { actions: ['read', 'turn_on', 'turn_off'], critical: [] },
  climate: { actions: ['read', 'set_temperature', 'set_mode'], critical: [] },
  cover: { actions: ['read', 'open', 'close', 'stop', 'set_position'], critical: [] },
  gate: { actions: ['read', 'open', 'close'], critical: ['open'] },
  lock: { actions: ['read', 'lock', 'unlock', 'open'], critical: ['unlock', 'open'] },
  alarm: { actions: ['read', 'arm', 'disarm'], critical: ['disarm'] },
  camera: { actions: ['read', 'snapshot'], critical: ['snapshot'] },
  media: { actions: ['read', 'turn_on', 'turn_off', 'play', 'pause', 'set_volume'], critical: [] },
  sensor: { actions: ['read'], critical: [] },
  scene: { actions: ['read', 'activate'], critical: [] },
  script: { actions: ['read', 'run'], critical: ['run'] },
  other: { actions: ['read', 'set'], critical: ['set'] },
};

export const CATEGORIES = Object.keys(VOCABULARY) as readonly Category[];

/** actionsOf returns the actions of a category, "read" first, in specification order. */
export function actionsOf(category: Category): readonly string[] {
  return VOCABULARY[category].actions;
}

export function criticalActionsOf(category: Category): readonly string[] {
  return VOCABULARY[category].critical;
}

function isKnown(category: string): category is Category {
  return Object.hasOwn(VOCABULARY, category);
}

/**
 * isCritical tells whether a rule action can hit a critical action. "read" never is.
 * Without a category (rule on an entity, an area or "any") or with an extension category
 * whose vocabulary the UI does not know, it cannot tell and assumes it can.
 */
export function isCritical(category: Category | ExtensionCategory | undefined, action: string): boolean {
  if (action === 'read') return false;
  if (category === undefined || !isKnown(category)) return true;
  const { critical } = VOCABULARY[category];
  return action === '*' ? critical.length > 0 : critical.includes(action);
}

/**
 * newRule returns a rule with safe defaults: only "read", "allow" for harmless
 * categories and "ask" where critical actions exist; never allow_critical.
 */
export function newRule(category: Category, existing: readonly Rule[]): Rule {
  const decision: Decision = criticalActionsOf(category).length > 0 ? 'ask' : 'allow';
  return { id: freeRuleId(existing), resource: { category }, actions: ['read'], decision };
}

function freeRuleId(existing: readonly Rule[]): string {
  const used = new Set(existing.map((r) => r.id));
  let n = 1;
  while (used.has(`rule-${n}`)) n++;
  return `rule-${n}`;
}

/** grantsCritical returns the rules of a draft that carry allow_critical. */
export function grantsCritical(draft: MandateDraft): Rule[] {
  return draft.rules.filter((r) => r.allow_critical === true);
}

/**
 * needsCriticalConfirmation implements decision U9: saving needs the separate
 * confirmation if the draft has an allow_critical rule that the base version (null for a
 * new mandate or template) did not have in exactly this form; even a renamed rule counts
 * as new. It looks at rule form, not effect: removing a deny rule in front of an
 * unchanged allow_critical rule needs no confirmation, but the preview (U10) shows the
 * newly allowed critical action. The server applies the same rule and answers
 * "critical_confirmation_required" otherwise.
 */
export function needsCriticalConfirmation(base: MandateDraft | null, draft: MandateDraft): boolean {
  const before = new Set(base ? grantsCritical(base).map(canonical) : []);
  return grantsCritical(draft).some((r) => !before.has(canonical(r)));
}

/** canonical serializes a value with sorted keys and sorted string arrays. */
function canonical(value: unknown): string {
  return JSON.stringify(normalize(value));
}

function normalize(value: unknown): unknown {
  if (Array.isArray(value)) {
    const items = value.map(normalize);
    return items.every((i) => typeof i === 'string') ? [...(items as string[])].sort() : items;
  }
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value)
        .filter(([, v]) => v !== undefined)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([k, v]) => [k, normalize(v)]),
    );
  }
  return value;
}
