// SPDX-License-Identifier: AGPL-3.0-or-later

// Vocabulary v0 (SPEC-v0 section 5): which actions a category has, which are
// critical, and the safe defaults for new rules. Used by the UI's own evaluation
// (./evaluate.ts) and the editor; the server stays the authority for every decision.

import type { Category, ExtensionCategory, MandateDraft, Rule } from '../api/types.ts';
// Only the categories: the file's $schema and id are URLs that must not end up in the bundle.
import { categories } from './conformance/vocabulary/v0.json';

/** A parameter of an action (SPEC-v0 sections 4.5 and 5): its type, range or permitted values. */
export interface ParameterSpec {
  type: 'integer' | 'string';
  unit?: string;
  minimum?: number;
  maximum?: number;
  /** The permitted values of a string parameter; absent: any string. */
  values?: readonly string[];
}

/** The value of a parameter: an integer in the unit of the vocabulary, or a string. */
export type ParameterValue = number | string;

interface CategorySpec {
  actions: readonly string[];
  critical: readonly string[];
  /** action → its parameters (SPEC-v0 section 4.5). */
  parameters: Readonly<Record<string, Readonly<Record<string, ParameterSpec>>>>;
}

interface VocabularyFile {
  categories: Record<string, { actions: Record<string, { critical?: boolean; parameters?: Record<string, ParameterSpec> }> }>;
}

// The normative vocabulary of the pinned version of the specification, copied by
// tools/webconformance; nothing about categories and actions is kept in code.
const VOCABULARY = Object.fromEntries(
  Object.entries(categories as VocabularyFile['categories']).map(([category, { actions }]) => [
    category,
    {
      actions: Object.keys(actions),
      critical: Object.keys(actions).filter((a) => actions[a]?.critical === true),
      parameters: Object.fromEntries(Object.entries(actions).map(([a, spec]) => [a, spec.parameters ?? {}])),
    },
  ]),
) as unknown as Readonly<Record<Category, CategorySpec>>;

export const CATEGORIES = Object.keys(VOCABULARY) as readonly Category[];

/** actionsOf returns the actions of a category, "read" first, in specification order. */
export function actionsOf(category: Category): readonly string[] {
  return VOCABULARY[category].actions;
}

/** parametersOf returns the names of an action's parameters (SPEC-v0 section 4.5). */
export function parametersOf(category: Category, action: string): readonly string[] {
  return Object.keys(VOCABULARY[category]?.parameters[action] ?? {});
}

/** parameterSpec returns a parameter of an action in a category of the vocabulary. */
export function parameterSpec(category: string | undefined, action: string, parameter: string): ParameterSpec | undefined {
  if (category === undefined || !isKnown(category)) return undefined;
  const parameters = VOCABULARY[category].parameters[action];
  return parameters && Object.hasOwn(parameters, parameter) ? parameters[parameter] : undefined;
}

const MAX_TEXT = 64;

/** An identifier of 1 to limit printable ASCII characters without space (SPEC-v0 section 3.4). */
export function opaque(s: unknown, limit: number): s is string {
  return typeof s === 'string' && s.length >= 1 && s.length <= limit && /^[!-~]+$/.test(s);
}

/** validValue: an integer of magnitude at most 2^53 − 1, or a string like an area (SPEC-v0 section 4.5). */
export function validValue(v: unknown): v is ParameterValue {
  return (typeof v === 'number' && Number.isInteger(v) && Math.abs(v) <= Number.MAX_SAFE_INTEGER) || opaque(v, MAX_TEXT);
}

/**
 * declares: the value has the type of the parameter and, for a string with permitted
 * values, is one of them. The range of an integer is not part of it: a mandate may name a
 * value outside it, which no valid request carries.
 */
export function declares(p: ParameterSpec, v: ParameterValue): boolean {
  if (p.type === 'integer') return typeof v === 'number';
  return typeof v === 'string' && (p.values === undefined || p.values.includes(v));
}

/** admits: a request may carry the value for the parameter (SPEC-v0 section 4, step 0). */
function admits(p: ParameterSpec, v: ParameterValue): boolean {
  if (!declares(p, v)) return false;
  if (typeof v === 'string') return true;
  return (p.minimum === undefined || v >= p.minimum) && (p.maximum === undefined || v <= p.maximum);
}

/**
 * parametersAdmitted: every parameter of a request that the action declares has its type
 * and lies within its range or is one of its permitted values. Others are not checked here.
 */
export function parametersAdmitted(category: string | undefined, action: string, parameters: Readonly<Record<string, ParameterValue>>): boolean {
  return Object.entries(parameters).every(([name, value]) => {
    const p = parameterSpec(category, action, name);
    return p === undefined || admits(p, value);
  });
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

/** A constraint as the editor and the evaluation see it: limits or a list of values. */
export type ConstraintSpec = { min?: number; max?: number } | { one_of: readonly ParameterValue[] };

/**
 * parameterFits tells whether the constraint fits a parameter of the action (SPEC-v0
 * section 3.1 item 10): limits only an integer parameter, a list only values the
 * parameter declares; in the given category, or without one in any category of the
 * vocabulary.
 */
export function parameterFits(category: string | undefined, action: string, parameter: string, c: ConstraintSpec): boolean {
  return CATEGORIES.some((name) => {
    if (category !== undefined && name !== category) return false;
    const p = parameterSpec(name, action, parameter);
    if (!p) return false;
    return 'one_of' in c ? c.one_of.every((v) => declares(p, v)) : p.type === 'integer';
  });
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
