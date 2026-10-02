// SPDX-License-Identifier: AGPL-3.0-or-later

// What the editor shows about a draft, built on the evaluation of ./evaluate.ts:
// matrix cells (with "only at times" splits), effective changes between two versions
// (save summary, version compare) and per-rule hints. Validity dates are not part of the
// matrix; validityChange reports them for the save summary.
//
// Drafts are treated as immutable: results are cached per draft object, so an edit must
// produce a new draft (which the editor does anyway).
//
// Limit of the time split: a cell shows the outcome of the rules without conditions and,
// if different, the strictest outcome while conditioned rules apply ("at times"). With
// several conditioned rules of different decisions on one cell, the weaker ones are not
// shown separately; the UI labels the split "at times" and the rule list stays the
// reference.

import type { Category, Decision, Device, MandateDraft, Rule } from '../api/types.ts';
import { checkDraft, parseDateTime } from './check.ts';
import { coversAction, decide, resourceMatches } from './evaluate.ts';
import { CATEGORIES, criticalActionsOf, lookupAction } from './vocabulary.ts';

/** "default" is "no rule allows it", kept apart from "a rule forbids it" (deny). */
export type CellDecision = Decision | 'default';

export interface CellOutcome {
  decision: CellDecision;
  /** Index of the deciding rule; null for default. */
  rule: number | null;
  /** allow became ask because the action is critical (SPEC-v0 section 4 step 5). */
  demoted: boolean;
}

export interface Cell extends CellOutcome {
  critical: boolean;
  /** Rules that match at some time. */
  matching: number;
  /** The draft is invalid; nothing would apply. */
  invalid: boolean;
  /** Outcome while time-conditioned rules apply; null if the cell does not depend on time. */
  timed: CellOutcome | null;
}

export interface Change {
  device: Device;
  action: string;
  from: Cell;
  to: Cell;
  /** The change allows more: shown first and highlighted. */
  widening: boolean;
}

export interface Override {
  /** Index of the stricter rule. */
  by: number;
  device: string;
  action: string;
  /** The stricter rule applies only at certain times. */
  timed: boolean;
}

const STRICTNESS: Record<CellDecision, number> = { allow: 0, ask: 1, deny: 2, default: 2 };
const validity = new WeakMap<MandateDraft, boolean>();
const cells = new WeakMap<MandateDraft, Map<string, Cell>>();

function isValid(draft: MandateDraft): boolean {
  let valid = validity.get(draft);
  if (valid === undefined) {
    valid = checkDraft(draft).length === 0;
    validity.set(draft, valid);
  }
  return valid;
}

const conditional = (r: Rule) => r.conditions !== undefined;

function outcome(draft: MandateDraft, matched: Rule[], critical: boolean): CellOutcome {
  if (matched.length === 0) return { decision: 'default', rule: null, demoted: false };
  const result = decide(draft, matched, critical);
  return {
    decision: result.decision,
    rule: draft.rules.findIndex((r) => r.id === result.rule_id),
    demoted: result.reason === 'critical_demotion',
  };
}

/** cell evaluates one device and action, independent of the current time (cached per draft). */
export function cell(draft: MandateDraft, device: Device, action: string): Cell {
  let cache = cells.get(draft);
  if (!cache) {
    cache = new Map();
    cells.set(draft, cache);
  }
  const key = `${device.entity_id}\u0000${device.category}\u0000${device.area ?? ''}\u0000${action}`;
  const cached = cache.get(key);
  if (cached) return cached;
  const result = computeCell(draft, device, action);
  cache.set(key, result);
  return result;
}

function computeCell(draft: MandateDraft, device: Device, action: string): Cell {
  const critical = lookupAction(device.category, action).critical;
  const base = { critical, matching: 0, invalid: false, timed: null };
  if (!isValid(draft)) return { ...base, decision: 'default', rule: null, demoted: false, invalid: true };
  const resource = { entity_id: device.entity_id, category: device.category, area: device.area ?? undefined };
  const matched = draft.rules.filter((r) => resourceMatches(r, resource) && coversAction(r, action));
  const always = outcome(draft, matched.filter((r) => !conditional(r)), critical);
  const atTimes = outcome(draft, matched, critical);
  const timed = atTimes.decision === always.decision ? null : atTimes;
  return { ...base, ...always, matching: matched.length, timed };
}

export interface ValidityChange {
  valid_from: boolean;
  expires: boolean;
  /** The mandate applies for longer: earlier start, later or removed end. */
  widening: boolean;
}

/** validityChange reports changed validity dates for the save summary; null if none. */
export function validityChange(prev: MandateDraft, next: MandateDraft): ValidityChange | null {
  const from = (d: MandateDraft) => parseDateTime(d.valid_from);
  const until = (d: MandateDraft) => (d.expires === undefined ? Infinity : parseDateTime(d.expires));
  const fromChanged = from(prev) !== from(next);
  const untilChanged = until(prev) !== until(next);
  if (!fromChanged && !untilChanged) return null;
  return { valid_from: fromChanged, expires: untilChanged, widening: from(next) < from(prev) || until(next) > until(prev) };
}

/** isWidening tells whether a change from → to allows more. */
export function isWidening(from: CellDecision, to: CellDecision): boolean {
  return STRICTNESS[to] < STRICTNESS[from];
}

const key = (c: Cell) => `${c.decision}/${c.timed?.decision ?? ''}`;

/** diff lists the effective changes from prev to next, widening ones first. */
export function diff(prev: MandateDraft, next: MandateDraft, devices: readonly Device[]): Change[] {
  const changes: Change[] = [];
  for (const device of devices) {
    for (const action of device.actions) {
      const from = cell(prev, device, action);
      const to = cell(next, device, action);
      if (key(from) === key(to)) continue;
      const widening =
        isWidening(from.decision, to.decision) ||
        isWidening(from.timed?.decision ?? from.decision, to.timed?.decision ?? to.decision);
      changes.push({ device, action, from, to, widening });
    }
  }
  return [...changes.filter((c) => c.widening), ...changes.filter((c) => !c.widening)];
}

/** ruleMatches counts, per rule, the devices it matches for at least one action. */
export function ruleMatches(draft: MandateDraft, devices: readonly Device[]): number[] {
  return draft.rules.map(
    (rule) =>
      devices.filter((d) => resourceMatches(rule, { entity_id: d.entity_id, category: d.category, area: d.area ?? undefined }) && d.actions.some((a) => coversAction(rule, a))).length,
  );
}

/** overrides finds, per rule, the first place where a stricter rule wins over it. */
export function overrides(draft: MandateDraft, devices: readonly Device[]): (Override | null)[] {
  return draft.rules.map((rule, i) => {
    for (const device of devices) {
      const resource = { entity_id: device.entity_id, category: device.category, area: device.area ?? undefined };
      if (!resourceMatches(rule, resource)) continue;
      for (const action of device.actions) {
        if (!coversAction(rule, action)) continue;
        const critical = lookupAction(device.category, action).critical;
        const mine: Decision = rule.decision === 'allow' && critical && rule.allow_critical !== true ? 'ask' : rule.decision;
        const c = cell(draft, device, action);
        const winner = c.timed ?? c;
        if (winner.rule !== null && winner.rule !== i && STRICTNESS[winner.decision] > STRICTNESS[mine]) {
          const by = draft.rules[winner.rule];
          return { by: winner.rule, device: device.entity_id, action, timed: by !== undefined && conditional(by) };
        }
      }
    }
    return null;
  });
}

/** categoriesOf returns the categories a rule can hit. */
function categoriesOf(rule: Rule): readonly Category[] {
  const category = 'category' in rule.resource ? rule.resource.category : undefined;
  if (category === undefined) return CATEGORIES;
  return CATEGORIES.filter((c) => c === category);
}

/** demotedIn lists the critical actions an allow rule without allow_critical still asks for. */
export function demotedIn(rule: Rule): [Category, string][] {
  if (rule.decision !== 'allow' || rule.allow_critical === true) return [];
  return categoriesOf(rule).flatMap((c) =>
    criticalActionsOf(c)
      .filter((a) => coversAction(rule, a))
      .map((a): [Category, string] => [c, a]),
  );
}

/** criticalIncluded lists the critical actions a rule includes through "*". */
export function criticalIncluded(rule: Rule): [Category, string][] {
  if (!rule.actions.includes('*')) return [];
  return categoriesOf(rule).flatMap((c) => criticalActionsOf(c).map((a): [Category, string] => [c, a]));
}
