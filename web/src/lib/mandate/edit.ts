// SPDX-License-Identifier: AGPL-3.0-or-later

// Edits of a mandate draft. Every function returns a new draft or rule and leaves its
// input untouched: the evaluation caches results per draft object (engine/analysis.ts).
//
// allow_critical is a human's confirmation for exactly one form of a rule (decision U9).
// Any edit of the rule's scope, actions or conditions therefore takes it away again; the
// confirmation has to be given anew for what the rule covers now.

import type { Approval, Category, Decision, Device, MandateDraft, Rule, Weekday } from '../api/types.ts';
import { canonical, grantsCritical, newRule } from '../engine/vocabulary.ts';
import { WEEKDAYS } from './labels.ts';
import { resourceOf, scopeOf, type ScopeCategory } from './scope.ts';

function without(rule: Rule, ...keys: (keyof Rule)[]): Rule {
  return Object.fromEntries(Object.entries(rule).filter(([key]) => !keys.includes(key as keyof Rule))) as unknown as Rule;
}

const unconfirmed = (rule: Rule): Rule => without(rule, 'allow_critical');

// ---------------------------------------------------------------------------
// Rules of a draft

export function replaceRule(draft: MandateDraft, index: number, rule: Rule): MandateDraft {
  return { ...draft, rules: draft.rules.map((r, i) => (i === index ? rule : r)) };
}

/** moveRule moves the rule at `from` to position `to`; out-of-range moves change nothing. */
export function moveRule(draft: MandateDraft, from: number, to: number): MandateDraft {
  const last = draft.rules.length - 1;
  if (from === to || from < 0 || to < 0 || from > last || to > last) return draft;
  const rules = draft.rules.filter((_, i) => i !== from);
  return { ...draft, rules: [...rules.slice(0, to), draft.rules[from] as Rule, ...rules.slice(to)] };
}

export function removeRule(draft: MandateDraft, index: number): MandateDraft {
  return { ...draft, rules: draft.rules.filter((_, i) => i !== index) };
}

export function insertRule(draft: MandateDraft, index: number, rule: Rule): MandateDraft {
  return { ...draft, rules: [...draft.rules.slice(0, index), rule, ...draft.rules.slice(index)] };
}

/** appendRule adds a rule with the safe defaults: read only, "ask". */
export function appendRule(draft: MandateDraft, category: Category = 'light'): MandateDraft {
  return { ...draft, rules: [...draft.rules, newRule(category, draft.rules)] };
}

/**
 * withoutRevokedConfirmations prepares a draft that is put on top of a newer version: a
 * rule that carried allow_critical from the version the edit started from, and that the
 * newer version no longer has in this form, loses it. Someone took that confirmation back
 * meanwhile; a stale copy must not bring it back. What the human confirmed in this edit
 * stays. Returns the draft and whether anything was dropped.
 */
export function withoutRevokedConfirmations(draft: MandateDraft, started: MandateDraft, newer: MandateDraft): { draft: MandateDraft; dropped: boolean } {
  const forms = (d: MandateDraft) => new Set(grantsCritical(d).map(canonical));
  const before = forms(started);
  const now = forms(newer);
  const revoked = (rule: Rule) => rule.allow_critical === true && before.has(canonical(rule)) && !now.has(canonical(rule));
  const dropped = draft.rules.some(revoked);
  return { draft: dropped ? { ...draft, rules: draft.rules.map((r) => (revoked(r) ? unconfirmed(r) : r)) } : draft, dropped };
}

// ---------------------------------------------------------------------------
// Scope of a rule

/** withCategory changes the category; a chosen single device no longer fits and is dropped. */
export function withCategory(rule: Rule, category: ScopeCategory, devices: readonly Device[]): Rule {
  const { area } = scopeOf(rule, devices);
  return unconfirmed({ ...rule, resource: resourceOf({ category, area, device: null }) });
}

export function withArea(rule: Rule, area: string | null, devices: readonly Device[]): Rule {
  const { category } = scopeOf(rule, devices);
  return unconfirmed({ ...rule, resource: resourceOf({ category, area, device: null }) });
}

/** withDevice names a single device (with its category) or goes back to the whole category. */
export function withDevice(rule: Rule, device: string | null, devices: readonly Device[]): Rule {
  const scope = scopeOf(rule, devices);
  const found = devices.find((d) => d.entity_id === device);
  const category = scope.category === 'all' ? (found?.category ?? 'all') : scope.category;
  return unconfirmed({ ...rule, resource: resourceOf({ category, area: device === null ? scope.area : null, device }) });
}

// ---------------------------------------------------------------------------
// Actions and decision

/** Vocabulary order first, then whatever else the rule carries. */
function ordered(actions: readonly string[], vocabulary: readonly string[]): string[] {
  return [...vocabulary.filter((a) => actions.includes(a)), ...actions.filter((a) => !vocabulary.includes(a))];
}

/** toggleAction switches one action; from "all actions" it leaves all but this one. */
export function toggleAction(rule: Rule, action: string, vocabulary: readonly string[]): Rule {
  const current = rule.actions.includes('*') ? [...vocabulary] : rule.actions;
  const next = current.includes(action) ? current.filter((a) => a !== action) : [...current, action];
  return unconfirmed({ ...rule, actions: ordered(next, vocabulary) });
}

/** toggleAllActions switches between "*" and the safe start "read". */
export function toggleAllActions(rule: Rule): Rule {
  return unconfirmed({ ...rule, actions: rule.actions.includes('*') ? ['read'] : ['*'] });
}

/** withDecision sets the decision and drops what only another decision may carry. */
export function withDecision(rule: Rule, decision: Decision): Rule {
  if (rule.decision === decision) return rule;
  const next = { ...rule, decision };
  if (decision === 'ask') return without(next, 'allow_critical');
  return without(next, 'allow_critical', 'approval');
}

/** withAllowCritical records the separate confirmation; only an "allow" rule can carry it. */
export function withAllowCritical(rule: Rule, on: boolean): Rule {
  return on && rule.decision === 'allow' ? { ...rule, allow_critical: true } : unconfirmed(rule);
}

/** withApproval sets a rule's own approval settings; null goes back to the mandate's default. */
export function withApproval(rule: Rule, approval: Approval | null): Rule {
  return approval === null ? without(rule, 'approval') : { ...rule, approval };
}

// ---------------------------------------------------------------------------
// Conditions

function withConditions(rule: Rule, conditions: NonNullable<Rule['conditions']>): Rule {
  const filled = Object.fromEntries(Object.entries(conditions).filter(([, value]) => value !== undefined));
  const next = Object.keys(filled).length === 0 ? without(rule, 'conditions') : { ...rule, conditions: filled };
  return unconfirmed(next);
}

/** withWindow sets "HH:MM-HH:MM" or removes the time window with null. */
export function withWindow(rule: Rule, window: string | null): Rule {
  return withConditions(rule, { ...rule.conditions, time_window: window ?? undefined });
}

/** toggleWeekday switches one day; all seven days mean "no weekday condition". */
export function toggleWeekday(rule: Rule, day: Weekday): Rule {
  const current = rule.conditions?.weekdays ?? WEEKDAYS;
  const next = WEEKDAYS.filter((d) => (d === day ? !current.includes(d) : current.includes(d)));
  return withConditions(rule, { ...rule.conditions, weekdays: next.length === WEEKDAYS.length ? undefined : next });
}

// ---------------------------------------------------------------------------
// Settings of the mandate

export function withDefaults(draft: MandateDraft, approval: Approval): MandateDraft {
  return { ...draft, approval };
}

export function withLimit(draft: MandateDraft, perHour: number): MandateDraft {
  return { ...draft, limits: { max_actions_per_hour: perHour } };
}

export function withValidFrom(draft: MandateDraft, validFrom: string): MandateDraft {
  return { ...draft, valid_from: validFrom };
}

/** withExpires sets the end of validity; null means no end. */
export function withExpires(draft: MandateDraft, expires: string | null): MandateDraft {
  const { expires: _dropped, ...rest } = draft;
  void _dropped;
  return expires === null ? rest : { ...rest, expires };
}
