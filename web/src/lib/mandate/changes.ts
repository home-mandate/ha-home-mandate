// SPDX-License-Identifier: AGPL-3.0-or-later

// What an edit changed compared to the stored version: rules added, changed and removed,
// changed settings, and the effective changes grouped by their new decision. Feeds the
// "unsaved changes" counter, the save summary and the version compare.

import type { Decision, MandateDraft, Rule } from '../api/types.ts';
import type { Cell, CellDecision, Change } from '../engine/analysis.ts';
import { parseDateTime } from '../engine/check.ts';
import { canonical } from '../engine/vocabulary.ts';

export type RuleChangeKind = 'added' | 'changed' | 'removed';

export interface RuleChange {
  kind: RuleChangeKind;
  rule: Rule;
  /** Position in the version that has the rule: the old one for "removed". */
  index: number;
}

/** ruleChanges compares rules by id: added and changed in the new order, then removed. */
export function ruleChanges(prev: MandateDraft, next: MandateDraft): RuleChange[] {
  const before = new Map(prev.rules.map((r) => [r.id, r]));
  const after = new Set(next.rules.map((r) => r.id));
  const changes: RuleChange[] = [];
  next.rules.forEach((rule, index) => {
    const old = before.get(rule.id);
    if (!old) changes.push({ kind: 'added', rule, index });
    else if (canonical(old) !== canonical(rule)) changes.push({ kind: 'changed', rule, index });
  });
  prev.rules.forEach((rule, index) => {
    if (!after.has(rule.id)) changes.push({ kind: 'removed', rule, index });
  });
  return changes;
}

/**
 * ruleUnsaved says whether the rule with this id differs from the stored version (changed or
 * added); false when the draft no longer has it. Decides the hint after "Done" (issue #20).
 */
export function ruleUnsaved(prev: MandateDraft, next: MandateDraft, id: string): boolean {
  const rule = next.rules.find((r) => r.id === id);
  if (!rule) return false;
  const old = prev.rules.find((r) => r.id === id);
  return !old || canonical(old) !== canonical(rule);
}

/** A mandate as the editor holds it: the draft and the display name next to it. */
export interface Edited {
  name: string;
  draft: MandateDraft;
}

export type Setting = 'name' | 'valid_from' | 'expires' | 'limit' | 'timeout' | 'approvers' | 'order';

const sameInstant = (a: string | undefined, b: string | undefined) => a === b || parseDateTime(a) === parseDateTime(b);

/** Order of the rules both versions have. */
function sharedOrder(of: MandateDraft, other: MandateDraft): string {
  const ids = new Set(other.rules.map((r) => r.id));
  return of.rules.filter((r) => ids.has(r.id)).map((r) => r.id).join(',');
}

/** settingChanges lists what changed besides the rules themselves. */
export function settingChanges(prev: Edited, next: Edited): Setting[] {
  const a = prev.draft;
  const b = next.draft;
  const changed: [Setting, boolean][] = [
    ['name', prev.name.trim() !== next.name.trim()],
    ['valid_from', !sameInstant(a.valid_from, b.valid_from)],
    ['expires', !sameInstant(a.expires, b.expires)],
    ['limit', a.limits.max_actions_per_hour !== b.limits.max_actions_per_hour],
    ['timeout', a.approval.timeout !== b.approval.timeout],
    ['approvers', canonical(a.approval.approvers) !== canonical(b.approval.approvers)],
    ['order', sharedOrder(a, b) !== sharedOrder(b, a)],
  ];
  return changed.filter(([, is]) => is).map(([setting]) => setting);
}

export function countChanges(prev: Edited, next: Edited): number {
  return ruleChanges(prev.draft, next.draft).length + settingChanges(prev, next).length;
}

export interface EffectGroup {
  /** The new decision; "default" counts as denied. */
  kind: Decision;
  changes: Change[];
}

const ORDER: readonly Decision[] = ['allow', 'ask', 'deny'];

/** For the agent "no rule allows it" and "a rule forbids it" are the same: denied. */
const permission = (decision: CellDecision): Decision => (decision === 'default' ? 'deny' : decision);
const atTimes = (cell: Cell): CellDecision => cell.timed?.decision ?? cell.decision;

/** The decision that is new: the one that always applies or, if that stayed, the one at times. */
function newPermission(change: Change): Decision | null {
  const { from, to } = change;
  if (permission(from.decision) !== permission(to.decision)) return permission(to.decision);
  if (permission(atTimes(from)) !== permission(atTimes(to))) return permission(atTimes(to));
  // Only the reason changed (a deny rule instead of the default, or the other way round).
  return null;
}

/**
 * effectGroups sorts effective changes into "newly allowed / needs approval / denied".
 * A change that leaves the agent's permission as it was is no effect and is left out.
 */
export function effectGroups(changes: readonly Change[]): EffectGroup[] {
  return ORDER.map((kind) => {
    const of = changes.filter((c) => newPermission(c) === kind);
    // Critical actions first, so a long list never hides them.
    return { kind, changes: [...of.filter((c) => c.to.critical), ...of.filter((c) => !c.to.critical)] };
  }).filter((g) => g.changes.length > 0);
}
