// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setLocale } from '../paraglide/runtime.js';
import { actionLabel, categoryLabel, decisionLabel, weekdayNames, weekdayOrder, WEEKDAYS } from './labels.ts';

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

describe('weekdayOrder', () => {
  afterEach(() => vi.restoreAllMocks());

  const SUNDAY_FIRST = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
  const SATURDAY_FIRST = ['sat', 'sun', 'mon', 'tue', 'wed', 'thu', 'fri'];

  it("starts the week on the first day of the household's country", () => {
    expect(weekdayOrder('de', 'DE')).toEqual(WEEKDAYS);
    expect(weekdayOrder('en', 'US')).toEqual(SUNDAY_FIRST);
    expect(weekdayOrder('de', 'US')).toEqual(SUNDAY_FIRST);
    // English alone would mean the US: the country decides.
    expect(weekdayOrder('en', 'GB')).toEqual(WEEKDAYS);
    expect(weekdayOrder('en', 'AU')).toEqual(WEEKDAYS);
  });

  it('starts on Saturday where the country does', () => {
    expect(weekdayOrder('de', 'AF')).toEqual(SATURDAY_FIRST);
    expect(weekdayOrder('en', 'EG')).toEqual(SATURDAY_FIRST);
  });

  it('starts on Monday without a country or with a value that is none', () => {
    expect(weekdayOrder('en', null)).toEqual(WEEKDAYS);
    expect(weekdayOrder('en', '')).toEqual(WEEKDAYS);
    expect(weekdayOrder('en', 'United States')).toEqual(WEEKDAYS);
    expect(weekdayOrder('en', 'u\u202Es')).toEqual(WEEKDAYS);
  });

  /** without runs check with the named members of Intl.Locale.prototype missing, as in an older browser. */
  function without(names: readonly ('getWeekInfo' | 'weekInfo')[], check: () => void) {
    const proto = Intl.Locale.prototype;
    const saved = names.map((name) => [name, Object.getOwnPropertyDescriptor(proto, name)] as const);
    for (const name of names) Object.defineProperty(proto, name, { value: undefined, configurable: true });
    try {
      check();
    } finally {
      for (const [name, descriptor] of saved) if (descriptor) Object.defineProperty(proto, name, descriptor);
    }
  }

  it('uses the older weekInfo where getWeekInfo is missing', () => {
    without(['getWeekInfo'], () => expect(weekdayOrder('en', 'US')).toEqual(SUNDAY_FIRST));
  });

  it('starts on Monday when the browser cannot tell', () => {
    without(['getWeekInfo', 'weekInfo'], () => expect(weekdayOrder('en', 'US')).toEqual(WEEKDAYS));
  });

  it('starts on Monday when the browser reports a day that is none', () => {
    vi.spyOn(Intl.Locale.prototype, 'getWeekInfo' as never).mockReturnValue({ firstDay: 9 } as never);
    expect(weekdayOrder('en', 'US')).toEqual(WEEKDAYS);
  });
});
