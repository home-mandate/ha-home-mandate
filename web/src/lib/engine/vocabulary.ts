// SPDX-License-Identifier: AGPL-3.0-or-later

// Vocabulary v0 (SPEC-v0 section 5): which actions a category has, which are
// critical, and the safe defaults for new rules. Used by the UI's own evaluation
// (./evaluate.ts) and the editor; the server stays the authority for every decision.

import type { Category, ExtensionCategory, MandateDraft, Rule } from '../api/types.ts';
// Only the categories: the file's $schema and id are URLs that must not end up in the bundle.
import { categories } from './conformance/vocabulary/v0.json';

interface CategorySpec {
  actions: readonly string[];
  critical: readonly string[];
  /** action → names of its integer parameters (SPEC-v0 section 4.5). */
  parameters: Readonly<Record<string, readonly string[]>>;
}

interface VocabularyFile {
  categories: Record<string, { actions: Record<string, { critical?: boolean; parameters?: Record<string, unknown> }> }>;
}

// The normative vocabulary of the pinned version of the specification, copied by
// tools/webconformance; nothing about categories and actions is kept in code.
const VOCABULARY = Object.fromEntries(
  Object.entries(categories as VocabularyFile['categories']).map(([category, { actions }]) => [
    category,
    {
      actions: Object.keys(actions),
      critical: Object.keys(actions).filter((a) => actions[a]?.critical === true),
      parameters: Object.fromEntries(Object.entries(actions).map(([a, spec]) => [a, Object.keys(spec.parameters ?? {})])),
    },
  ]),
) as unknown as Readonly<Record<Category, CategorySpec>>;

export const CATEGORIES = Object.keys(VOCABULARY) as readonly Category[];

/** actionsOf returns the actions of a category, "read" first, in specification order. */
export function actionsOf(category: Category): readonly string[] {
  return VOCABULARY[category].actions;
}

/** parametersOf returns the names of an action's integer parameters (SPEC-v0 section 4.5). */
export function parametersOf(category: Category, action: string): readonly string[] {
  return VOCABULARY[category]?.parameters[action] ?? [];
}

export function criticalActionsOf(category: Category): readonly string[] {
  return VOCABULARY[category].critical;
}

function isKnown(category: string): category is Category {
  return Object.hasOwn(VOCABULARY, category);
}

export interface ActionLookup {
  categoryKnown: boolean;
  actionKnown: boolean;
  critical: boolean;
}

/** lookupAction resolves a requested action (never "*") in the vocabulary v0. */
export function lookupAction(category: string, action: string): ActionLookup {
  if (!isKnown(category)) return { categoryKnown: false, actionKnown: false, critical: false };
  const { actions, critical } = VOCABULARY[category];
  return { categoryKnown: true, actionKnown: actions.includes(action), critical: critical.includes(action) };
}

/** knownAction tells whether any category of the vocabulary has the action. */
export function knownAction(action: string): boolean {
  return CATEGORIES.some((c) => VOCABULARY[c].actions.includes(action));
}

/**
 * hasParameter tells whether the action has the parameter: in the given category, or
 * without one in any category of the vocabulary.
 */
export function hasParameter(category: string | undefined, action: string, parameter: string): boolean {
  return CATEGORIES.some((c) => (category === undefined || c === category) && (VOCABULARY[c].parameters[action] ?? []).includes(parameter));
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
 * newRule returns a rule with safe defaults: only "read" and "ask", the safe middle
 * between allowing and forbidding (design README 6.5); never allow_critical.
 */
export function newRule(category: Category, existing: readonly Rule[]): Rule {
  return { id: freeRuleId(existing), resource: { category }, actions: ['read'], decision: 'ask' };
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
export function canonical(value: unknown): string {
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
