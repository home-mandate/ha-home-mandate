// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { previewApprovers, type PreviewInput } from './mock-approvers.ts';
import { createMockClient } from './mock.ts';
import type { Approver, Category, MandateDraft, Rule } from './types.ts';

const rule = (decision: Rule['decision'], category: Category | undefined, actions: string[], extra: Partial<Rule> = {}): Rule => ({
  id: 'r-1',
  resource: category === undefined ? { any: true } : { category },
  actions,
  decision,
  ...extra,
});

const draft = (rules: Rule[], approvers = ['$approvers']): MandateDraft => ({
  rules,
  approval: { timeout: 'PT2M', approvers },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
});

const approver = (user_id: string, normal: Approver['reach']['normal'], critical: Approver['reach']['critical']): Approver => ({
  user_id,
  name: user_id,
  devices: [],
  ui: false,
  ui_critical: false,
  language: null,
  reach: { normal, critical },
});

const input = (over: Partial<PreviewInput>): PreviewInput => ({
  draft: draft([]),
  placeholder: ['u-me'],
  approvers: [],
  names: { 'u-me': 'Me' },
  self: 'u-me',
  serviceUser: 'u-hm',
  haDown: false,
  ...over,
});

describe('previewApprovers', () => {
  it('lists the people of the placeholder and named ones, each once, with their reach', () => {
    const got = previewApprovers(
      input({
        draft: draft([rule('ask', 'lock', ['unlock'], { approval: { timeout: 'PT1M', approvers: ['u-other', '$approvers'] } })], ['$approvers', 'u-hm']),
        placeholder: ['u-me', 'u-anna'],
        approvers: [approver('u-anna', 'push', 'none'), approver('u-hm', 'push', 'push')],
        names: { 'u-me': 'Me', 'u-anna': '<b>Anna</b>' },
      }),
    );
    expect(got.people).toEqual([
      { user_id: 'u-me', name: 'Me', normal: 'none', critical: 'none', self: true, service: false },
      { user_id: 'u-anna', name: '<b>Anna</b>', normal: 'push', critical: 'none', self: false, service: false },
      { user_id: 'u-hm', name: null, normal: 'none', critical: 'none', self: false, service: true },
      { user_id: 'u-other', name: null, normal: 'none', critical: 'none', self: false, service: false },
    ]);
    expect(got.normal).toBe('not_needed');
    expect(got.critical).toBe('nobody');
  });

  it.each([
    ['ask on an ordinary action', [rule('ask', 'light', ['turn_on'])], 'nobody', 'not_needed'],
    ['ask without a category', [rule('ask', undefined, ['turn_on'])], 'nobody', 'nobody'],
    ['allow of a critical action becomes ask', [rule('allow', 'lock', ['unlock'])], 'not_needed', 'nobody'],
    ['allow_critical asks nobody', [rule('allow', 'lock', ['unlock'], { allow_critical: true })], 'not_needed', 'not_needed'],
    ['deny asks nobody', [rule('deny', 'lock', ['*'])], 'not_needed', 'not_needed'],
  ])('%s', (_name, rules, normal, critical) => {
    const got = previewApprovers(input({ draft: draft(rules) }));
    expect([got.normal, got.critical]).toEqual([normal, critical]);
  });

  it('says reachable when someone is, and unknown, never reachable, without Home Assistant', () => {
    const base = input({ draft: draft([rule('ask', 'lock', ['lock', 'unlock'])]), approvers: [approver('u-me', 'ui', 'push')] });
    expect([previewApprovers(base).normal, previewApprovers(base).critical]).toEqual(['reachable', 'reachable']);
    const down = previewApprovers({ ...base, haDown: true });
    expect([down.normal, down.critical]).toEqual(['unknown', 'unknown']);
    expect(down.people[0]).toMatchObject({ name: null, normal: 'unknown', critical: 'unknown' });
  });
});

describe('mock templateApprovers', () => {
  it('answers for the signed-in human and refuses hidden or unknown templates', async () => {
    const api = createMockClient();
    const got = await api.templateApprovers('hm-voice-cautious');
    expect(got.people[0]).toMatchObject({ user_id: 'u-admin', name: 'Markus', self: true, critical: 'push' });
    expect(got.critical).toBe('reachable');
    await expect(api.templateApprovers('nope')).rejects.toMatchObject({ code: 'not_found' });
    const alone = await createMockClient({ noApprovers: true }).templateApprovers('hm-voice-cautious');
    expect(alone.people).toEqual([{ user_id: 'u-admin', name: 'Markus', normal: 'none', critical: 'none', self: true, service: false }]);
    expect(alone.critical).toBe('nobody');
    await api.setTemplateHidden('hm-voice-cautious', true);
    await expect(api.templateApprovers('hm-voice-cautious')).rejects.toMatchObject({ code: 'not_found' });
  });
});
