// SPDX-License-Identifier: AGPL-3.0-or-later

// Limits of an allowing rule (SPEC-v0 section 4.5): a rule with constraints matches only
// values within them; everything outside is denied. The vocabulary names the parameters
// and their units; the form shows temperatures in degrees and stores hundredths.

import type { Device, Rule } from '../api/types.ts';
import { MAX_LIMIT } from '../engine/check.ts';
import { parametersOf } from '../engine/vocabulary.ts';
import { m } from '../i18n.ts';
import { categoryOf } from './scope.ts';

interface Unit {
  /** Stored value = shown value × scale. */
  scale: number;
  /** Intl unit for display. */
  unit: 'percent' | 'celsius' | 'fahrenheit';
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
  temperature_f: { scale: 100, unit: 'fahrenheit' },
};

/**
 * The temperature parameter per unit of Home Assistant (spec #21 part B): the gateway
 * evaluates a temperature as exactly one of them, in the household's unit.
 */
const TEMPERATURE_OF: Readonly<Record<string, string>> = { '°C': 'temperature', '°F': 'temperature_f' };
const TEMPERATURES: readonly string[] = Object.values(TEMPERATURE_OF);

/**
 * Parameters limited by a list of permitted values (SPEC-v0 section 4.5): the values the
 * editor offers, those Home Assistant has an HVAC mode for (Home Assistant profile).
 */
export const CHOICES: Readonly<Record<string, readonly string[]>> = {
  mode: ['off', 'heat', 'cool', 'auto', 'dry', 'fan_only'],
};

const CHOICE_LABELS: Readonly<Record<string, () => string>> = {
  off: () => m.mode_off(),
  heat: () => m.mode_heat(),
  cool: () => m.mode_cool(),
  auto: () => m.mode_auto(),
  dry: () => m.mode_dry(),
  fan_only: () => m.mode_fan_only(),
  emergency_heat: () => m.mode_emergency_heat(),
  precooling: () => m.mode_precooling(),
  sleep: () => m.mode_sleep(),
};

/** choiceLabel names a permitted value; an unknown one is shown as it is. */
export function choiceLabel(value: number | string): string {
  return typeof value === 'string' ? (CHOICE_LABELS[value]?.() ?? value) : String(value);
}

/** isChoice tells whether the editor limits the parameter by a list of values. */
export function isChoice(name: string): boolean {
  return Object.hasOwn(CHOICES, name);
}

/** chosen returns the values a rule permits for a choice parameter; every offered one without a limit. */
export function chosen(rule: Rule, name: string): readonly (number | string)[] {
  const c = rule.constraints?.[name];
  return c !== undefined && 'one_of' in c ? c.one_of : (CHOICES[name] ?? []);
}

/**
 * withChoices limits a choice parameter to values. All offered values are no limit; none
 * is an empty list, which the check of the draft reports, never "everything" (SPEC-v0
 * section 4.5). Values the editor does not offer are kept. Like any change of the rule's
 * form it takes back allow_critical (decision U9).
 */
export function withChoices(rule: Rule, name: string, values: readonly (number | string)[]): Rule {
  const { constraints, allow_critical: _confirmed, ...rest } = rule;
  void _confirmed;
  const offered = CHOICES[name] ?? [];
  const others = Object.fromEntries(Object.entries(constraints ?? {}).filter(([key]) => key !== name));
  const all = offered.every((v) => values.includes(v)) && values.every((v) => offered.includes(v as string));
  const next = all ? others : { ...others, [name]: { one_of: [...values] } };
  return Object.keys(next).length > 0 ? { ...rest, constraints: next } : rest;
}

const NAMES: Readonly<Record<string, () => string>> = {
  mode: () => m.limit_mode(),
  brightness: () => m.limit_brightness(),
  temperature: () => m.limit_temperature(),
  temperature_f: () => m.limit_temperature(),
  position: () => m.limit_position(),
  volume: () => m.limit_volume(),
};

/** A number as people type it: digits, at most one decimal separator (dot or comma). */
const NUMBER = /^-?\d+(?:[.,]\d+)?$/;

type Constraint = NonNullable<Rule['constraints']>[string];

/** rangeOf returns the limits of a constraint on an integer; an empty range for a list of values. */
export function rangeOf(c: Constraint | undefined): { min?: number; max?: number } {
  return c === undefined || 'one_of' in c ? {} : c;
}

export function unitOf(name: string): Unit {
  return UNITS[name] ?? { scale: 1, unit: 'percent' };
}

export function parameterLabel(name: string): string {
  return NAMES[name]?.() ?? name;
}

/**
 * limitableParameters lists the values an allowing rule can be limited by: those that
 * every one of its chosen actions carries (SPEC-v0 section 3.1 item 10). A temperature
 * only in the household's unit (temperatureUnit, "°C" or "°F"; none while it is unknown):
 * a limit in the other unit would never apply (#42).
 */
export function limitableParameters(rule: Rule, devices: readonly Device[], temperatureUnit: string | null): string[] {
  if (rule.decision !== 'allow' || rule.actions.length === 0 || rule.actions.includes('*')) return [];
  const category = categoryOf(rule, devices);
  if (category === undefined) return [];
  const own = temperatureUnit === null ? undefined : TEMPERATURE_OF[temperatureUnit];
  const [first, ...rest] = rule.actions.map((a) => parametersOf(category, a));
  return (first ?? []).filter(
    (name) => rest.every((names) => names.includes(name)) && (name in UNITS || isChoice(name)) && (!TEMPERATURES.includes(name) || name === own),
  );
}

/**
 * limitsNeedOwnRule tells whether an allowing rule has an action with a value that could
 * be limited, but cannot be limited because another of its actions lacks that value
 * (SPEC-v0 section 3.1 item 10): the editor then says to use a rule of its own.
 */
export function limitsNeedOwnRule(rule: Rule, devices: readonly Device[], temperatureUnit: string | null): boolean {
  if (rule.decision !== 'allow' || rule.actions.length < 2 || rule.actions.includes('*')) return false;
  if (limitableParameters(rule, devices, temperatureUnit).length > 0) return false;
  return rule.actions.some((a) => limitableParameters({ ...rule, actions: [a] }, devices, temperatureUnit).length > 0);
}

/** otherUnitLimits names the rule's limits on a temperature in a unit the household does not use: they never apply. */
export function otherUnitLimits(rule: Rule, temperatureUnit: string | null): string[] {
  const own = temperatureUnit === null ? undefined : TEMPERATURE_OF[temperatureUnit];
  if (own === undefined) return [];
  return Object.keys(rule.constraints ?? {}).filter((name) => TEMPERATURES.includes(name) && name !== own);
}

/** toInput shows a stored limit in the unit of the form; nothing for an invalid one. */
export function toInput(name: string, value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '' : String(value / unitOf(name).scale);
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
  if (Math.abs(stored - rounded) >= 1e-9 || Math.abs(rounded) > MAX_LIMIT) return 'invalid';
  return rounded + 0; // no -0
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

/**
 * valuesText describes the parameters of an evaluated request (audit log), e.g. "Mode:
 * Cool" or "Temperature: 21.5 °C", in the units of the form; empty without parameters.
 */
export function valuesText(parameters: Readonly<Record<string, number | string>> | undefined, locale: string): string {
  return Object.entries(parameters ?? {})
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([name, value]) => {
      if (typeof value === 'string') return m.limits_one_of({ name: parameterLabel(name), values: choiceLabel(value) });
      if (!Object.hasOwn(UNITS, name)) return m.limits_one_of({ name: parameterLabel(name), values: String(value) });
      const unit = unitOf(name);
      const shown = new Intl.NumberFormat(locale, { style: 'unit', unit: unit.unit, maximumFractionDigits: 2 }).format(value / unit.scale);
      return m.limits_one_of({ name: parameterLabel(name), values: shown });
    })
    .join(', ');
}

/** limitsText describes the limits of a rule, e.g. "Brightness 10–80%"; empty without. */
export function limitsText(rule: Rule, locale: string): string {
  return Object.entries(rule.constraints ?? {})
    .map(([name, c]) => {
      if ('one_of' in c) {
        const unit = unitOf(name);
        const number = new Intl.NumberFormat(locale, { style: 'unit', unit: unit.unit, maximumFractionDigits: 2 });
        const shown = c.one_of.map((v) => (typeof v === 'number' ? number.format(v / unit.scale) : choiceLabel(v)));
        const values = new Intl.ListFormat(locale, { type: 'disjunction' }).format(shown);
        return m.limits_one_of({ name: parameterLabel(name), values });
      }
      const limit = rangeOf(c);
      // An invalid limit (typed, not yet a number) is reported by the form, not described.
      const min = Number.isFinite(limit.min) ? limit.min : undefined;
      const max = Number.isFinite(limit.max) ? limit.max : undefined;
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
