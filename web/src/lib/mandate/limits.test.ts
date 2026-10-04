// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture } from '../api/fixtures.ts';
import type { Rule } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { fromInput, limitableParameters, limitsText, toInput, withConstraint } from './limits.ts';

beforeEach(() => setLocale('en', { reload: false }));

const devices = devicesFixture.devices;
const rule = (patch: Partial<Rule> = {}): Rule => ({ id: 'r', resource: { category: 'light' }, actions: ['set'], decision: 'allow', ...patch });

describe('which values a rule can limit', () => {
  it('offers the parameter every chosen action carries', () => {
    expect(limitableParameters(rule(), devices)).toEqual(['brightness']);
    expect(limitableParameters(rule({ resource: { category: 'climate' }, actions: ['set_temperature'] }), devices)).toEqual(['temperature']);
    expect(limitableParameters(rule({ resource: { entity_id: 'climate.hvac' }, actions: ['set_temperature'] }), devices)).toEqual(['temperature']);
    expect(limitableParameters(rule({ resource: { category: 'cover' }, actions: ['set_position'] }), devices)).toEqual(['position']);
    expect(limitableParameters(rule({ resource: { category: 'media' }, actions: ['set_volume'] }), devices)).toEqual(['volume']);
  });

  it('offers nothing when an action has no such value, for "all actions", without a category or for deny and ask', () => {
    expect(limitableParameters(rule({ actions: ['set', 'turn_on'] }), devices)).toEqual([]);
    expect(limitableParameters(rule({ actions: ['*'] }), devices)).toEqual([]);
    expect(limitableParameters(rule({ actions: [] }), devices)).toEqual([]);
    expect(limitableParameters(rule({ resource: { any: true } }), devices)).toEqual([]);
    expect(limitableParameters(rule({ decision: 'deny' }), devices)).toEqual([]);
    expect(limitableParameters(rule({ decision: 'ask' }), devices)).toEqual([]);
  });
});

describe('units', () => {
  it('shows temperatures in degrees and stores hundredths', () => {
    expect(toInput('temperature', 2150)).toBe('21.5');
    expect(fromInput('temperature', '21.5')).toBe(2150);
    expect(fromInput('temperature', '21,5')).toBe(2150);
    expect(fromInput('temperature', '-3')).toBe(-300);
    expect(fromInput('temperature', '21.555')).toBe('invalid');
  });

  it('keeps percent as it is, within 0 to 100', () => {
    expect(toInput('brightness', 80)).toBe('80');
    expect(fromInput('brightness', ' 80 ')).toBe(80);
    expect(fromInput('brightness', '')).toBeNull();
    for (const bad of ['101', '-1', '50.5', 'abc', '1e2', 'Infinity']) expect(fromInput('brightness', bad), bad).toBe('invalid');
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
    setLocale('de', { reload: false });
    expect(limitsText(rule({ constraints: { temperature: { max: 2350 } } }), 'de')).toBe('Temperatur bis 23,5 °C');
  });
});
