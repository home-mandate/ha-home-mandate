// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, HOSTILE_NAME } from '../api/fixtures.ts';
import type { Rule } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { clockText, conditionsText, daysText, listText, ruleLine, ruleText, windowText } from './text.ts';

beforeEach(() => setLocale('en', { reload: false }));

const rule = (patch: Partial<Rule> = {}): Rule => ({ id: 'r', resource: { category: 'light' }, actions: ['read', 'turn_on'], decision: 'allow', ...patch });

describe('time and days', () => {
  it('formats clock times in the habit of the locale', () => {
    expect(clockText(22 * 60, 'de')).toBe('22:00');
    expect(clockText(6 * 60 + 30, 'en')).toMatch(/^06:30\sAM$/);
  });

  it('says when a window runs overnight', () => {
    expect(windowText('07:00-22:00', 'de')).toBe('07:00 to 22:00');
    expect(windowText('22:00-06:00', 'de')).toBe('22:00 to 06:00 the next day');
    expect(windowText('nonsense', 'de')).toBe('');
    expect(windowText(undefined, 'de')).toBe('');
  });

  it('writes runs of three or more days as a range', () => {
    expect(daysText(['mon', 'tue', 'wed', 'thu', 'fri'], 'en')).toMatch(/^Mon\s–\sFri$/);
    expect(daysText(['mon', 'tue', 'sat'], 'en')).toBe('Mon, Tue, Sat');
    expect(daysText(['mon', 'wed', 'thu', 'fri', 'sun'], 'de')).toMatch(/^Mo, Mi\s–\sFr und So$/);
    expect(daysText(['sat', 'sun'], 'de')).toBe('Sa, So');
  });

  it('has no text for every day, no day or no condition', () => {
    expect(daysText(undefined, 'en')).toBe('');
    expect(daysText([], 'en')).toBe('');
    expect(daysText(['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'], 'en')).toBe('');
  });

  it('joins weekdays and window', () => {
    expect(conditionsText(rule({ conditions: { weekdays: ['sat', 'sun'], time_window: '08:00-12:00' } }), 'de')).toBe('Sa, So · 08:00 to 12:00');
    expect(conditionsText(rule({ conditions: { time_window: '08:00-12:00' } }), 'de')).toBe('08:00 to 12:00');
    expect(conditionsText(rule(), 'de')).toBe('');
  });

  it('lists in the habit of the locale', () => {
    expect(listText(['a', 'b', 'c'], 'en')).toBe('a, b, c');
    expect(listText(['a', 'b', 'c'], 'de')).toBe('a, b und c');
  });
});

describe('ruleText', () => {
  const text = (r: Rule) => ruleText(r, devicesFixture, 'en');

  it('names category, area and actions', () => {
    expect(text(rule({ resource: { category: 'light', area: 'kitchen' } }))).toEqual({ subject: 'Lights', area: 'Küche', actions: 'read, turn on', conditions: '' });
    expect(ruleLine(text(rule({ resource: { category: 'light', area: 'kitchen' } })))).toBe('Lights · Küche: read, turn on');
    expect(ruleLine(text(rule()))).toBe('Lights: read, turn on');
  });

  it('names all devices and all actions', () => {
    expect(ruleLine(text(rule({ resource: { any: true }, actions: ['*'] })))).toBe('All devices: all actions');
    expect(ruleLine(text(rule({ resource: { area: 'garage' }, actions: ['read'] })))).toBe('All devices · Garage: read');
  });

  it('names a single device and uses its category for the action names', () => {
    const door = text(rule({ resource: { entity_id: 'lock.front_door' }, actions: ['read', 'unlock'] }));
    expect(door).toMatchObject({ subject: 'Haustür', area: '', actions: 'read, unlock' });
    expect(text(rule({ resource: { entity_id: 'light.kitchen', category: 'light', area: 'kitchen' }, actions: ['set'] }))).toMatchObject({ subject: 'Küchenlicht', area: '', actions: 'adjust' });
  });

  it('shows ids for devices and areas the catalog does not know', () => {
    expect(text(rule({ resource: { entity_id: 'lock.gone' } })).subject).toBe('lock.gone');
    expect(text(rule({ resource: { category: 'light', area: 'attic' } })).area).toBe('attic');
  });

  it('keeps hostile device names as plain text without hidden characters', () => {
    expect(text(rule({ resource: { entity_id: 'script.demo' } })).subject).toBe(HOSTILE_NAME);
    const catalog = { areas: [{ id: 'kitchen', name: 'K\u202Eüche\n2' }], devices: [] };
    expect(ruleText(rule({ resource: { category: 'light', area: 'kitchen' } }), catalog, 'en').area).toBe('Küche 2');
  });

  it('isolates the names inside a plain-text sentence on request', () => {
    expect(ruleLine(text(rule({ resource: { category: 'light', area: 'kitchen' } })), true)).toBe('\u2068Lights\u2069 · \u2068Küche\u2069: read, turn on');
  });

  it('carries the conditions', () => {
    expect(text(rule({ conditions: { time_window: '22:00-06:00' } })).conditions).toMatch(/^10:00\sPM to 06:00\sAM the next day$/);
  });
});

describe('limits in the sentence', () => {
  it('follows the actions they limit', () => {
    const limited = rule({ resource: { category: 'climate' }, actions: ['set_temperature'], constraints: { temperature: { min: 1600, max: 2300 } } });
    expect(ruleText(limited, devicesFixture, 'en').actions).toMatch(/^set temperature \(Temperature 16\s?(°C)?\s?–\s?23\s?°C\)$/);
  });
});
