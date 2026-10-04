// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { setLocale } from '../paraglide/runtime.js';
import { actionLabel, categoryLabel, decisionLabel, weekdayNames, WEEKDAYS } from './labels.ts';

beforeEach(() => setLocale('en', { reload: false }));

describe('actionLabel', () => {
  it('names the actions of the vocabulary', () => {
    expect(actionLabel('lock', 'unlock')).toBe('unlock');
    expect(actionLabel('climate', 'set_temperature')).toBe('set temperature');
    expect(actionLabel(undefined, 'read')).toBe('read');
  });

  it('uses the design names where the specification name differs (decision D10)', () => {
    expect(actionLabel('light', 'set')).toBe('adjust');
    expect(actionLabel('other', 'set')).toBe('set');
    expect(actionLabel('media', 'turn_on')).toBe('on');
    expect(actionLabel('media', 'turn_off')).toBe('off');
    expect(actionLabel('light', 'turn_on')).toBe('turn on');
    expect(actionLabel('media', 'set_volume')).toBe('volume');
  });

  it('names "*" and shows unknown actions as they are, without hidden characters', () => {
    expect(actionLabel('light', '*')).toBe('all actions');
    expect(actionLabel('light', 'dim')).toBe('dim');
    expect(actionLabel('light', 'constructor')).toBe('constructor');
    expect(actionLabel('light', 'a\u202Eb')).toBe('ab');
  });
});

describe('categoryLabel', () => {
  it('names categories, "all" and shows extension categories as they are', () => {
    expect(categoryLabel('gate')).toBe('Gate/garage');
    expect(categoryLabel('all')).toBe('All devices');
    expect(categoryLabel('paperless:document')).toBe('paperless:document');
    expect(categoryLabel('toString')).toBe('toString');
  });
});

describe('decisionLabel', () => {
  it('keeps "default" apart from "deny"', () => {
    expect(decisionLabel('allow')).toBe('Allowed');
    expect(decisionLabel('ask')).toBe('Ask first');
    expect(decisionLabel('deny')).toBe('Denied');
    expect(decisionLabel('default')).toBe('Default: denied');
  });
});

describe('weekdayNames', () => {
  it('gives short names from Intl, Monday first', () => {
    expect(WEEKDAYS).toEqual(['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']);
    expect(weekdayNames('en')).toMatchObject({ mon: 'Mon', sun: 'Sun' });
    expect(weekdayNames('de')).toMatchObject({ mon: 'Mo', sun: 'So' });
  });
});
