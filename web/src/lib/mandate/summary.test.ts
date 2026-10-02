// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { voiceAssistantDraft } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import { moveRule, withExpires, withLimit, withValidFrom } from './edit.ts';
import { cellText, settingLines } from './summary.ts';

beforeEach(() => setLocale('en', { reload: false }));

const base = voiceAssistantDraft;
const ctx = { locale: 'en', timeZone: 'Europe/Berlin' };
const people = new Map([
  ['u-admin', 'Markus'],
  ['u-partner', 'Al\u202Eex'],
]);

describe('settingLines', () => {
  it('has no lines without changed settings', () => {
    expect(settingLines({ name: 'A', draft: base }, { name: 'A', draft: base }, ctx, people)).toEqual([]);
  });

  it('shows old and new value in the household time zone and the UI language', () => {
    const next = {
      ...withExpires(withLimit(withValidFrom(base, '2026-10-31T23:00:00Z'), 1200), '2026-12-31T23:00:00Z'),
      approval: { timeout: 'PT45S', approvers: ['u-admin', 'u-partner', 'u-unknown'] },
    };
    const lines = settingLines({ name: 'Kitchen', draft: base }, { name: ' Küche\n2 ', draft: next }, ctx, people);
    expect(lines.map((l) => [l.label, l.from, l.to])).toEqual([
      ['Display name', 'Kitchen', 'Küche 2'],
      ['Valid from', 'October 1, 2026', 'November 1, 2026'],
      ['Valid until', 'no end date', 'December 31, 2026'],
      ['Rate limit', '60', '1,200'],
      ['Approval timeout', '2 minutes', '45 seconds'],
      ['Approvers', 'Markus', 'Markus, Alex, u-unknown'],
    ]);
  });

  it('names a new order of the rules without values', () => {
    const lines = settingLines({ name: 'A', draft: base }, { name: 'A', draft: moveRule(base, 0, 1) }, ctx, people);
    expect(lines).toEqual([{ setting: 'order', label: 'Order of the rules changed (no effect)', from: '', to: '' }]);
  });

  it('leaves an invalid start date empty', () => {
    const lines = settingLines({ name: 'A', draft: base }, { name: 'A', draft: withValidFrom(base, '') }, ctx, people);
    expect(lines.map((l) => [l.from, l.to])).toEqual([['October 1, 2026', '']]);
  });
});

describe('cellText', () => {
  it('names the decision, and the one at times if the cell depends on time', () => {
    expect(cellText({ decision: 'allow', timed: null })).toBe('Allowed');
    expect(cellText({ decision: 'default', timed: null })).toBe('Default: denied');
    expect(cellText({ decision: 'allow', timed: { decision: 'deny', rule: 1, demoted: false } })).toBe('Allowed, at times Denied');
  });
});
