// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { voiceAssistantDraft } from '../api/fixtures.ts';
import type { MandateDraft } from '../api/types.ts';
import { APPROVERS_PLACEHOLDER, isPlaceholder, usesPlaceholder, withApprovers } from './placeholder.ts';

const everyone = { timeout: 'PT2M', approvers: [APPROVERS_PLACEHOLDER, 'u-partner'] };
const draft: MandateDraft = {
  ...voiceAssistantDraft,
  approval: everyone,
  rules: [
    { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['unlock'], decision: 'ask', approval: { timeout: 'PT1M', approvers: [APPROVERS_PLACEHOLDER] } },
    { id: 'lights', resource: { category: 'light' }, actions: ['turn_on'], decision: 'allow' },
  ],
};

describe('placeholder', () => {
  it('is only the exact value', () => {
    expect(isPlaceholder('$approvers')).toBe(true);
    expect(isPlaceholder('$Approvers')).toBe(false);
    expect(isPlaceholder('u-admin')).toBe(false);
  });

  it('is found at the mandate and in rules', () => {
    expect(usesPlaceholder(draft)).toBe(true);
    expect(usesPlaceholder({ ...draft, approval: { timeout: 'PT2M', approvers: ['u-admin'] }, rules: [] })).toBe(false);
    expect(usesPlaceholder(voiceAssistantDraft)).toBe(false);
  });

  it('is replaced by the people, without duplicates, everywhere', () => {
    const out = withApprovers(draft, ['u-admin', 'u-partner']);
    expect(out?.approval.approvers).toEqual(['u-admin', 'u-partner']);
    expect(out?.rules[0]?.approval?.approvers).toEqual(['u-admin', 'u-partner']);
    expect(out?.rules[1]).toBe(draft.rules[1]);
    expect(draft.approval.approvers).toEqual([APPROVERS_PLACEHOLDER, 'u-partner']);
  });

  it('cannot stand for nobody', () => {
    expect(withApprovers(draft, [])).toBeNull();
    expect(withApprovers(voiceAssistantDraft, [])).toEqual(voiceAssistantDraft);
  });
});
