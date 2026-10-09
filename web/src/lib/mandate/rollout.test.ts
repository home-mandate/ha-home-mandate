// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { voiceAssistantDraft } from '../api/fixtures.ts';
import type { RolloutResult, TemplateUser } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { grantsCritical, merged, preselected, refusedCount, resultText, retryable, targetsOf } from './rollout.ts';

beforeEach(() => setLocale('en', { reload: false }));

const user = (id: string, extra: Partial<TemplateUser> = {}): TemplateUser => ({
  mandate_id: id,
  mandate_name: id,
  client_id: `pair:${id}`,
  agent_display_name: id,
  digest: `sha256:${id}`,
  taken_at: '2026-10-06T16:09:00.000Z',
  template_digest: 'sha256:old',
  edited_since: false,
  up_to_date: false,
  ...extra,
});

describe('rollout', () => {
  it('preselects the mandates that are neither edited since nor up to date', () => {
    const users = [user('a'), user('b', { edited_since: true }), user('c', { up_to_date: true })];
    expect([...preselected(users)]).toEqual(['a']);
  });

  it('makes the targets of the chosen mandates, in the order of the list', () => {
    const users = [user('a'), user('b'), user('c')];
    expect(targetsOf(users, new Set(['c', 'a']))).toEqual([
      { mandate_id: 'a', base_digest: 'sha256:a' },
      { mandate_id: 'c', base_digest: 'sha256:c' },
    ]);
  });

  it('names every result and tells which can be tried again', () => {
    const all: RolloutResult[] = ['updated', 'unchanged', 'conflict', 'revoked', 'not_found', 'failed', 'skipped'];
    expect(all.map(resultText)).toEqual([
      'Updated',
      'No change needed',
      'Not updated: changed in the meantime',
      'Not updated: revoked',
      'Not updated: no longer exists',
      'Not updated: something went wrong',
      'Not updated: not attempted, time ran out',
    ]);
    // A retry loads the mandate again first; revoked and removed ones have nothing to load.
    expect(all.filter(retryable)).toEqual(['conflict', 'failed', 'skipped']);
    expect(refusedCount({ a: 'updated', b: 'conflict', c: 'revoked', d: 'unchanged' })).toBe(2);
  });

  it('keeps the newest result per mandate', () => {
    expect(merged({ a: 'conflict', b: 'updated' }, [{ mandate_id: 'a', result: 'updated', digest: null }])).toEqual({ a: 'updated', b: 'updated' });
  });

  it('tells whether a template grants critical actions without approval', () => {
    expect(grantsCritical(voiceAssistantDraft)).toBe(false);
    const rule = { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['unlock'], decision: 'allow' as const, allow_critical: true as const };
    expect(grantsCritical({ ...voiceAssistantDraft, rules: [rule] })).toBe(true);
  });
});
