// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor works directly on schema rules. The device scope of a rule is shown as three
// selects (category, area, single device); this module maps a rule's resource to those
// selects and back, and finds the action vocabulary that fits the scope.

import type { Category, Device, ExtensionCategory, Rule, RuleResource } from '../api/types.ts';
import { actionsOf, CATEGORIES } from '../engine/vocabulary.ts';

/** "all" stands for a rule without a category. */
export type ScopeCategory = Category | ExtensionCategory | 'all';

export interface Scope {
  category: ScopeCategory;
  area: string | null;
  device: string | null;
}

const isKnown = (category: string): category is Category => (CATEGORIES as readonly string[]).includes(category);

/**
 * scopeOf reads the selects from a rule. A rule on a single device without a category
 * shows the device's category from the catalog, so its actions can be offered.
 */
export function scopeOf(rule: Rule, devices: readonly Device[]): Scope {
  const r = rule.resource;
  if ('any' in r && r.any === true) return { category: 'all', area: null, device: null };
  const device = r.entity_id ?? null;
  const category = r.category ?? devices.find((d) => d.entity_id === device)?.category ?? 'all';
  return { category, area: r.area ?? null, device };
}

/**
 * resourceOf writes the selects back. A single device is named by its entity_id (and its
 * category, so the server checks the actions); the area only narrowed the choice.
 */
export function resourceOf(scope: Scope): RuleResource {
  const category = scope.category === 'all' ? undefined : scope.category;
  if (scope.device !== null) return category ? { entity_id: scope.device, category } : { entity_id: scope.device };
  if (category && scope.area) return { category, area: scope.area };
  if (category) return { category };
  if (scope.area) return { area: scope.area };
  return { any: true };
}

/**
 * isEditable tells whether the form can edit a rule. Rules on an extension category
 * ("paperless:document") have a vocabulary the UI does not know; they stay as they are
 * and can only be moved or deleted.
 */
export function isEditable(rule: Rule): boolean {
  const category = 'category' in rule.resource ? rule.resource.category : undefined;
  return category === undefined || isKnown(category);
}

/** vocabularyOf returns the actions the form offers for a rule's scope; only "read" without a category. */
export function vocabularyOf(rule: Rule, devices: readonly Device[]): readonly string[] {
  const { category } = scopeOf(rule, devices);
  return isKnown(category) ? actionsOf(category) : ['read'];
}

/** categoryOf returns the known category of a rule's scope, for action labels and critical marks. */
export function categoryOf(rule: Rule, devices: readonly Device[]): Category | undefined {
  const { category } = scopeOf(rule, devices);
  return isKnown(category) ? category : undefined;
}

/** deviceOptions lists the devices the "single device" select offers for a scope. */
export function deviceOptions(scope: Scope, devices: readonly Device[]): Device[] {
  return devices.filter(
    (d) => (scope.category === 'all' || d.category === scope.category) && (scope.area === null || d.area === scope.area),
  );
}
