// SPDX-License-Identifier: AGPL-3.0-or-later

// Limits of an allowing rule (SPEC-v0 section 4.5): a rule with constraints matches only
// values within them; everything outside is denied. The vocabulary names the parameters
// and their units; the form shows temperatures in degrees and stores hundredths.

import type { Device, Rule } from '../api/types.ts';
import { parametersOf } from '../engine/vocabulary.ts';
import { m } from '../i18n.ts';
import { categoryOf } from './scope.ts';

interface Unit {
  /** Stored value = shown value × scale. */
  scale: number;
  /** Intl unit for display. */
  unit: 'percent' | 'celsius';
  /** Range of the shown value; none for temperatures. */
  min?: number;
  max?: number;
}

const PERCENT: Unit = { scale: 1, unit: 'percent', min: 0, max: 100 };
const UNITS: Readonly<Record<string, Unit>> = {
  brightness: PERCENT,
  position: PERCENT,
  volume: PERCENT,
  temperature: { scale: 100, unit: 'celsius' },
};

const NAMES: Readonly<Record<string, () => string>> = {
  brightness: () => m.limit_brightness(),
  temperature: () => m.limit_temperature(),
  position: () => m.limit_position(),
  volume: () => m.limit_volume(),
};

/** A number as people type it: digits, at most one decimal separator (dot or comma). */
const NUMBER = /^-?\d+(?:[.,]\d+)?$/;

export function unitOf(name: string): Unit {
  return UNITS[name] ?? { scale: 1, unit: 'percent' };
}

export function parameterLabel(name: string): string {
  return NAMES[name]?.() ?? name;
}

/**
 * limitableParameters lists the values an allowing rule can be limited by: those that
 * every one of its chosen actions carries (SPEC-v0 section 3.1 item 10).
 */
export function limitableParameters(rule: Rule, devices: readonly Device[]): string[] {
  if (rule.decision !== 'allow' || rule.actions.length === 0 || rule.actions.includes('*')) return [];
  const category = categoryOf(rule, devices);
  if (category === undefined) return [];
  const [first, ...rest] = rule.actions.map((a) => parametersOf(category, a));
  return (first ?? []).filter((name) => rest.every((names) => names.includes(name)) && name in UNITS);
}

/** toInput shows a stored limit in the unit of the form. */
export function toInput(name: string, value: number | undefined): string {
  return value === undefined ? '' : String(value / unitOf(name).scale);
}

/** fromInput turns what was typed into a stored limit: null for empty, 'invalid' if it is none. */
export function fromInput(name: string, text: string): number | null | 'invalid' {
  const trimmed = text.trim();
  if (trimmed === '') return null;
  if (!NUMBER.test(trimmed)) return 'invalid';
  const unit = unitOf(name);
  const shown = Number(trimmed.replace(',', '.'));
  if ((unit.min !== undefined && shown < unit.min) || (unit.max !== undefined && shown > unit.max)) return 'invalid';
  const stored = shown * unit.scale;
  // Exactly what was typed is stored, never a rounded value (SPEC-v0 section 4.5).
  const rounded = Math.round(stored);
  return Math.abs(stored - rounded) < 1e-9 ? rounded : 'invalid';
}

/**
 * withConstraint sets the limits of one value; without either limit the value is free
 * again. Like any change of the rule's form it takes back allow_critical (decision U9).
 */
export function withConstraint(rule: Rule, name: string, min: number | undefined, max: number | undefined): Rule {
  const { constraints, allow_critical: _confirmed, ...rest } = rule;
  void _confirmed;
  const others = Object.fromEntries(Object.entries(constraints ?? {}).filter(([key]) => key !== name));
  const limit = { ...(min === undefined ? {} : { min }), ...(max === undefined ? {} : { max }) };
  const next = Object.keys(limit).length > 0 ? { ...others, [name]: limit } : others;
  return Object.keys(next).length > 0 ? { ...rest, constraints: next } : rest;
}

/** withoutConstraints removes every limit of the rule. */
export function withoutConstraints(rule: Rule): Rule {
  const { constraints: _limits, allow_critical: _confirmed, ...rest } = rule;
  void _limits;
  void _confirmed;
  return rest;
}

/** limitsText describes the limits of a rule, e.g. "Brightness 10–80%"; empty without. */
export function limitsText(rule: Rule, locale: string): string {
  return Object.entries(rule.constraints ?? {})
    .map(([name, { min, max }]) => {
      const unit = unitOf(name);
      const format = new Intl.NumberFormat(locale, { style: 'unit', unit: unit.unit, maximumFractionDigits: 2 });
      const value = (v: number) => v / unit.scale;
      const label = parameterLabel(name);
      if (min !== undefined && max !== undefined) return m.limits_range({ name: label, range: format.formatRange(value(min), value(max)) });
      if (min !== undefined) return m.limits_from({ name: label, value: format.format(value(min)) });
      return max === undefined ? '' : m.limits_to({ name: label, value: format.format(value(max)) });
    })
    .filter(Boolean)
    .join(', ');
}
