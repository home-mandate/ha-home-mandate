// SPDX-License-Identifier: AGPL-3.0-or-later

// Computed notes under a rule's sentence (design README 6.5): critical actions that still
// ask, a confirmed "without approval", a stricter rule that wins, and why a rule cannot be
// edited here. They explain the effect; the evaluation itself is engine/analysis.ts.

import type { Device, DeviceCatalog, MandateDraft, Rule } from '../api/types.ts';
import { criticalIncluded, criticalOn, demotedIn, type Override } from '../engine/analysis.ts';
import { coversAction, resourceMatches } from '../engine/evaluate.ts';
import { m } from '../i18n.ts';
import { actionLabel } from './labels.ts';
import { categoryOf, isEditable } from './scope.ts';
import { conditionsText, listText } from './text.ts';

export interface RuleNote {
  kind: 'critical' | 'danger' | 'info';
  text: string;
}

/** Names of critical actions, each once ("open" of gate and lock is one name). */
function names(pairs: readonly [string, string][], locale: string): string {
  return listText([...new Set(pairs.map(([category, action]) => actionLabel(category, action)))], locale);
}

/**
 * A rule on a single device can only hit that device's category. Naming the critical
 * actions of every category would be wrong for it, so the catalog's category is used.
 */
function scoped(rule: Rule, devices: readonly Device[]): Rule {
  const category = categoryOf(rule, devices);
  const r = rule.resource;
  if (category === undefined || r.entity_id === undefined || r.category !== undefined) return rule;
  return { ...rule, resource: { ...r, category } };
}

/** demotedNames lists the critical actions an allow rule would cover without the confirmation. */
export function demotedNames(rule: Rule, devices: readonly Device[], locale: string): string {
  const { allow_critical: _confirmed, ...unconfirmed } = scoped(rule, devices);
  void _confirmed;
  return names(demotedIn(unconfirmed), locale);
}

/** includedNames lists the critical actions "all actions" includes; "" if none. */
export function includedNames(rule: Rule, devices: readonly Device[], locale: string): string {
  return names(criticalIncluded(scoped(rule, devices)), locale);
}

/** criticalDevices returns the devices on which a rule covers at least one critical action. */
export function criticalDevices(rule: Rule, devices: readonly Device[]): Device[] {
  return devices.filter(
    (d) =>
      resourceMatches(rule, { entity_id: d.entity_id, category: d.category, area: d.area ?? undefined }) &&
      d.actions.some((a) => coversAction(rule, a) && criticalOn(d, a)),
  );
}

/** ruleNotes explains what happens to a rule in the mandate it is part of. */
export function ruleNotes(draft: MandateDraft, index: number, catalog: DeviceCatalog, override: Override | null, locale: string): RuleNote[] {
  const rule = draft.rules[index];
  if (!rule) return [];
  const notes: RuleNote[] = [];
  if (!isEditable(rule)) notes.push({ kind: 'info', text: m.rule_readonly_extension() });
  const demoted = demotedIn(scoped(rule, catalog.devices));
  if (demoted.length > 0) notes.push({ kind: 'critical', text: `${m.demoted_hint()} (${names(demoted, locale)})` });
  if (rule.allow_critical === true) notes.push({ kind: 'danger', text: m.critical_override_active() });
  if (override) {
    const category = catalog.devices.find((d) => d.entity_id === override.device)?.category;
    const by = draft.rules[override.by];
    const window = by ? conditionsText(by, locale) : '';
    const values = { action: actionLabel(category, override.action), n: override.by + 1 };
    notes.push({ kind: 'info', text: override.timed && window ? m.rule_overridden_timed({ ...values, window }) : m.rule_overridden(values) });
  }
  return notes;
}
