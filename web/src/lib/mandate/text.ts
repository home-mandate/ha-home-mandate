// SPDX-License-Identifier: AGPL-3.0-or-later

// A rule as a sentence ("Lights · Kitchen: turn on, turn off → Allowed"), as the editor,
// the save summary and the version compare show it. Device and area names come from Home
// Assistant and are untrusted: they are cleaned here and rendered escaped.

import type { DeviceCatalog, Rule, Weekday } from '../api/types.ts';
import { windowMinutes } from '../engine/check.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted, isolate } from '../untrusted.ts';
import { actionLabel, categoryLabel, WEEKDAYS, weekdayNames } from './labels.ts';
import { limitsText } from './limits.ts';
import { categoryOf } from './scope.ts';

export interface RuleText {
  /** The device, the category or "all devices". */
  subject: string;
  /** The area of a rule on a category or on all devices; empty otherwise. */
  area: string;
  actions: string;
  /** Weekdays and time window; empty without conditions. */
  conditions: string;
}

const MINUTE_MS = 60_000;
const DAY_MS = 86_400_000;
/** 2024-01-01 was a Monday. */
const A_MONDAY = Date.UTC(2024, 0, 1);
/** From this many days in a row a run is written as a range ("Mon – Fri"). */
const MIN_RANGE = 3;

export function listText(items: readonly string[], locale: string): string {
  return new Intl.ListFormat(locale, { style: 'short', type: 'unit' }).format(items);
}

/** clockText formats minutes since midnight as a time of day in the locale's habit. */
export function clockText(minutes: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit', timeZone: 'UTC' }).format(minutes * MINUTE_MS);
}

/** windowText describes "HH:MM-HH:MM"; a window that ends before it starts runs overnight. */
export function windowText(window: string | undefined, locale: string): string {
  const w = windowMinutes(window);
  if (!w) return '';
  const range = { start: clockText(w.start, locale), end: clockText(w.end, locale) };
  return w.end < w.start ? m.time_overnight(range) : m.time_same_day(range);
}

/** daysText lists weekdays, runs of three or more as a range; empty for "every day". */
export function daysText(weekdays: readonly Weekday[] | undefined, locale: string): string {
  if (!weekdays || weekdays.length === 0 || WEEKDAYS.every((d) => weekdays.includes(d))) return '';
  const names = weekdayNames(locale);
  const format = new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' });
  const parts: string[] = [];
  let i = 0;
  while (i < WEEKDAYS.length) {
    if (!weekdays.includes(WEEKDAYS[i] as Weekday)) {
      i++;
      continue;
    }
    let end = i;
    while (end + 1 < WEEKDAYS.length && weekdays.includes(WEEKDAYS[end + 1] as Weekday)) end++;
    if (end - i + 1 >= MIN_RANGE) parts.push(format.formatRange(A_MONDAY + i * DAY_MS, A_MONDAY + end * DAY_MS));
    else for (let d = i; d <= end; d++) parts.push(names[WEEKDAYS[d] as Weekday]);
    i = end + 1;
  }
  return listText(parts, locale);
}

export function conditionsText(rule: Rule, locale: string): string {
  return [daysText(rule.conditions?.weekdays, locale), windowText(rule.conditions?.time_window, locale)].filter(Boolean).join(' · ');
}

/** actionsText lists a rule's actions by name, with their limits; "*" is "all actions". */
export function actionsText(rule: Rule, catalog: DeviceCatalog, locale: string): string {
  if (rule.actions.includes('*')) return m.rule_all_actions();
  const category = categoryOf(rule, catalog.devices);
  const actions = listText(rule.actions.map((a) => actionLabel(category, a)), locale);
  const limits = limitsText(rule, locale);
  return limits ? `${actions} (${limits})` : actions;
}

export function ruleText(rule: Rule, catalog: DeviceCatalog, locale: string): RuleText {
  const r = rule.resource;
  const entity = r.entity_id;
  const device = catalog.devices.find((d) => d.entity_id === entity);
  const subject = entity !== undefined ? cleanUntrusted(device?.name) || cleanUntrusted(entity) : categoryLabel(r.category ?? 'all');
  const areaName = catalog.areas.find((a) => a.id === r.area)?.name;
  const area = entity === undefined && r.area !== undefined ? cleanUntrusted(areaName) || cleanUntrusted(r.area) : '';
  return { subject, area, actions: actionsText(rule, catalog, locale), conditions: conditionsText(rule, locale) };
}

/**
 * ruleLine is the sentence without the decision, e.g. "Lights · Kitchen: turn on, turn off".
 * Inside a longer plain-text sentence the names are isolated, so a right-to-left name
 * cannot reorder what stands around it; markup uses <bdi> instead.
 */
export function ruleLine(text: RuleText, isolated = false): string {
  const name = (value: string) => (isolated ? isolate(value) : value);
  return `${name(text.subject)}${text.area ? ` · ${name(text.area)}` : ''}: ${text.actions}`;
}
