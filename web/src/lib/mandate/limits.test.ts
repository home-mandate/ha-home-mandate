// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture } from '../api/fixtures.ts';
import type { Rule } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { chosen, limitsNeedOwnRule, valuesText, fromInput, limitableParameters, limitsText, otherUnitLimits, toInput, withChoices, withConstraint } from './limits.ts';
import { checkDraft } from '../engine/check.ts';

beforeEach(() => setLocale('en', { reload: false }));

const devices = devicesFixture.devices;
const rule = (patch: Partial<Rule> = {}): Rule => ({ id: 'r', resource: { category: 'light' }, actions: ['set'], decision: 'allow', ...patch });

describe('which values a rule can limit', () => {
  it('offers the parameter every chosen action carries', () => {
    expect(limitableParameters(rule(), devices, '°C')).toEqual(['brightness']);
    expect(limitableParameters(rule({ resource: { category: 'climate' }, actions: ['set_temperature'] }), devices, '°C')).toEqual(['temperature']);
    expect(limitableParameters(rule({ resource: { entity_id: 'climate.hvac' }, actions: ['set_temperature'] }), devices, '°C')).toEqual(['temperature']);
    expect(limitableParameters(rule({ resource: { category: 'cover' }, actions: ['set_position'] }), devices, '°C')).toEqual(['position']);
    expect(limitableParameters(rule({ resource: { category: 'media' }, actions: ['set_volume'] }), devices, '°C')).toEqual(['volume']);
    expect(limitableParameters(rule({ resource: { category: 'climate' }, actions: ['set_mode'] }), devices, '°C')).toEqual(['mode']);
  });

  // #42, spec #21 part B: a temperature only in the household's unit; none while it is unknown.
  it('offers the temperature in the unit of the household', () => {
    const heating = rule({ resource: { category: 'climate' }, actions: ['set_temperature'] });
    expect(limitableParameters(heating, devices, '°F')).toEqual(['temperature_f']);
    expect(limitableParameters(heating, devices, '°C')).toEqual(['temperature']);
    expect(limitableParameters(heating, devices, null)).toEqual([]);
    expect(limitableParameters(rule(), devices, null)).toEqual(['brightness']);
  });

  it('names limits in the other unit, which never apply in the household', () => {
    const limited = rule({ resource: { category: 'climate' }, actions: ['set_temperature'], constraints: { temperature: { max: 2300 } } });
    expect(otherUnitLimits(limited, '°F')).toEqual(['temperature']);
    expect(otherUnitLimits(limited, '°C')).toEqual([]);
    expect(otherUnitLimits(limited, null)).toEqual([]);
    expect(otherUnitLimits({ ...limited, constraints: { temperature_f: { max: 7500 } } }, '°C')).toEqual(['temperature_f']);
  });

  it('offers nothing when an action has no such value, for "all actions", without a category or for deny and ask', () => {
    expect(limitableParameters(rule({ actions: ['set', 'turn_on'] }), devices, '°C')).toEqual([]);
    expect(limitableParameters(rule({ actions: ['*'] }), devices, '°C')).toEqual([]);
    expect(limitableParameters(rule({ actions: [] }), devices, '°C')).toEqual([]);
    expect(limitableParameters(rule({ resource: { any: true } }), devices, '°C')).toEqual([]);
    expect(limitableParameters(rule({ decision: 'deny' }), devices, '°C')).toEqual([]);
    expect(limitableParameters(rule({ decision: 'ask' }), devices, '°C')).toEqual([]);
  });
});

describe('units', () => {
  it('shows temperatures in degrees and stores hundredths', () => {
    expect(toInput('temperature', 2150)).toBe('21.5');
    expect(fromInput('temperature', '21.5')).toBe(2150);
    expect(fromInput('temperature', '21,5')).toBe(2150);
    expect(fromInput('temperature', '-3')).toBe(-300);
    expect(fromInput('temperature', '21.555')).toBe('invalid');
    expect(toInput('temperature_f', 7050)).toBe('70.5');
    expect(fromInput('temperature_f', '101')).toBe(10100);
    expect(fromInput('temperature_f', '70.005')).toBe('invalid');
  });

  it('keeps percent as it is, within 0 to 100', () => {
    expect(toInput('brightness', 80)).toBe('80');
    expect(fromInput('brightness', ' 80 ')).toBe(80);
    expect(fromInput('brightness', '')).toBeNull();
    for (const bad of ['101', '-1', '50.5', 'abc', '1e2', 'Infinity', ',5', '5,']) expect(fromInput('brightness', bad), bad).toBe('invalid');
    expect(Object.is(fromInput('temperature', '-0'), 0)).toBe(true);
    expect(fromInput('temperature', '99999999999999999999')).toBe('invalid');
    expect(toInput('brightness', Number.NaN)).toBe('');
  });
});

describe('editing limits', () => {
  it('sets, narrows and removes a limit; an empty set of limits disappears', () => {
    const limited = withConstraint(rule(), 'brightness', 10, 80);
    expect(limited.constraints).toEqual({ brightness: { min: 10, max: 80 } });
    expect(withConstraint(limited, 'brightness', undefined, 80).constraints).toEqual({ brightness: { max: 80 } });
    expect('constraints' in withConstraint(limited, 'brightness', undefined, undefined)).toBe(false);
  });

  it('takes back a confirmation for critical actions: the rule has a new form', () => {
    const confirmed = rule({ allow_critical: true });
    expect('allow_critical' in withConstraint(confirmed, 'brightness', 10, 80)).toBe(false);
  });

  it('leaves the input untouched', () => {
    const original = rule({ constraints: { brightness: { min: 1 } } });
    withConstraint(original, 'brightness', 5, 6);
    expect(original.constraints).toEqual({ brightness: { min: 1 } });
  });
});

describe('limits as text', () => {
  it('names the value and its range in the unit of the household', () => {
    expect(limitsText(rule({ constraints: { brightness: { min: 10, max: 80 } } }), 'en')).toMatch(/^Brightness 10\s?%?\s?–\s?80\s?%$/);
    expect(limitsText(rule({ constraints: { temperature: { min: 1600 } } }), 'en')).toBe('Temperature from 16°C');
    expect(limitsText(rule(), 'en')).toBe('');
    expect(limitsText(rule({ constraints: { brightness: { min: Number.NaN, max: 80 } } }), 'en')).toMatch(/^Brightness up to 80\s?%$/);
    expect(limitsText(rule({ constraints: { temperature: { min: -500 } } }), 'en')).toMatch(/^Temperature from -5\s?°C$/);
    expect(limitsText(rule({ constraints: { temperature_f: { min: 6000, max: 7500 } } }), 'en')).toMatch(/^Temperature 60(\s?°F)?\s?–\s?75\s?°F$/);
    setLocale('de', { reload: false });
    expect(limitsText(rule({ constraints: { temperature: { max: 2350 } } }), 'de')).toBe('Temperatur bis 23,5 °C');
  });
});

describe('permitted modes', () => {
  const modes = (patch: Partial<Rule> = {}) => rule({ resource: { category: 'climate' }, actions: ['set_mode'], ...patch });

  it('offers every mode Home Assistant has without a limit, and limits to the checked ones', () => {
    expect(chosen(modes(), 'mode')).toEqual(['off', 'heat', 'cool', 'auto', 'dry', 'fan_only']);
    const limited = withChoices(modes(), 'mode', ['heat', 'auto']);
    expect(limited.constraints).toEqual({ mode: { one_of: ['heat', 'auto'] } });
    expect(chosen(limited, 'mode')).toEqual(['heat', 'auto']);
    expect(checkDraft(draftWith(limited))).toEqual([]);
  });

  it('removes the limit when every mode is checked again', () => {
    expect(withChoices(modes(), 'mode', ['off', 'heat', 'cool', 'auto', 'dry', 'fan_only']).constraints).toBeUndefined();
  });

  it('never reads "no mode checked" as "every mode": the draft reports it', () => {
    const none = withChoices(modes(), 'mode', []);
    expect(none.constraints).toEqual({ mode: { one_of: [] } });
    expect(checkDraft(draftWith(none))).toContainEqual({ field: '/rules/0/constraints/mode/one_of', code: 'required' });
  });

  it('keeps a value the editor does not offer, and takes back allow_critical', () => {
    const sleeping = modes({ constraints: { mode: { one_of: ['sleep', 'heat'] } }, allow_critical: true });
    const next = withChoices(sleeping, 'mode', [...chosen(sleeping, 'mode'), 'cool']);
    expect(next.constraints).toEqual({ mode: { one_of: ['sleep', 'heat', 'cool'] } });
    expect(next.allow_critical).toBeUndefined();
  });

  it('names the permitted modes', () => {
    expect(limitsText(modes({ constraints: { mode: { one_of: ['heat', 'auto'] } } }), 'en')).toBe('Mode: Heat or Automatic');
    expect(limitsText(rule({ constraints: { brightness: { one_of: [10, 50] } } }), 'en')).toMatch(/^Brightness: 10\s?% or 50\s?%$/);
    setLocale('de', { reload: false });
    expect(limitsText(modes({ constraints: { mode: { one_of: ['heat', 'sleep'] } } }), 'de')).toBe('Modus: Heizen oder Schlafen');
  });
});

function draftWith(r: Rule) {
  return { rules: [r], approval: { timeout: 'PT2M', approvers: ['u1'] }, limits: { max_actions_per_hour: 60 }, valid_from: '2026-10-01T00:00:00Z' };
}

describe('limits in a rule of their own', () => {
  it('says so when another action of an allowing rule lacks the value (SPEC-v0 section 3.1 item 10)', () => {
    const climate = (actions: string[], decision: Rule['decision'] = 'allow') => rule({ resource: { category: 'climate' }, actions, decision });
    expect(limitsNeedOwnRule(climate(['read', 'set_mode']), devices, '°C')).toBe(true);
    expect(limitsNeedOwnRule(climate(['set_mode', 'set_temperature']), devices, '°C')).toBe(true);
    expect(limitsNeedOwnRule(climate(['set_mode']), devices, '°C')).toBe(false);
    expect(limitsNeedOwnRule(climate(['read']), devices, '°C')).toBe(false);
    expect(limitsNeedOwnRule(climate(['read', 'set_mode'], 'ask'), devices, '°C')).toBe(false);
  });
});

describe('evaluated values', () => {
  it('names them in the units of the form', () => {
    expect(valuesText({ mode: 'cool' }, 'en')).toBe('Mode: Cool');
    expect(valuesText({ temperature: 2150 }, 'en')).toMatch(/^Temperature: 21\.5\s?°C$/);
    expect(valuesText({ brightness: 40, x_unknown: 3 }, 'en')).toMatch(/^Brightness: 40\s?%, x_unknown: 3$/);
    expect(valuesText(undefined, 'en')).toBe('');
  });
});
