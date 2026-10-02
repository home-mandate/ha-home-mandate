// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { voiceAssistantDraft, voiceAssistantMandate } from '../api/fixtures.ts';
import type { MandateVersion } from '../api/types.ts';
import { currentNumber, draftOf, restoredDraft, shortDigest, versionAt } from './versions.ts';

const version = (number: number, digest: string): MandateVersion => ({ number, digest, created_at: '2026-10-01T08:00:00Z', created_by: 'u-admin', created_by_name: 'Markus' });

describe('versions', () => {
  it('shortens a digest to eight characters without its prefix', () => {
    expect(shortDigest('sha256:3f9a1c2e7b0d4e5f9a8c6b1d2e3f4a5b')).toBe('3f9a1c2e');
    expect(shortDigest('3f9a1c2e7b0d')).toBe('3f9a1c2e');
    expect(shortDigest('sha256:ab')).toBe('ab');
  });

  it('finds versions by the number the server gave them; the list is newest first', () => {
    const versions = [version(3, 'c'), version(2, 'b'), version(1, 'a')];
    expect(currentNumber(versions)).toBe(3);
    expect(currentNumber([])).toBe(0);
    expect(versionAt(versions, 3)?.digest).toBe('c');
    expect(versionAt(versions, 1)?.digest).toBe('a');
    for (const absent of [0, 4, -1, 1.5]) expect(versionAt(versions, absent)).toBeUndefined();
  });

  it('tells versions with the same digest apart (a restored version repeats one)', () => {
    const versions = [version(3, 'a'), version(2, 'b'), version(1, 'a')];
    expect(versionAt(versions, 3)).toBe(versions[0]);
    expect(versionAt(versions, 1)).toBe(versions[2]);
  });

  it('restores rules, approval settings and rate limit, and keeps the validity of the current version', () => {
    const old = { ...voiceAssistantDraft, rules: [], approval: { timeout: 'PT30S', approvers: ['u-partner'] }, limits: { max_actions_per_hour: 5 }, valid_from: '2026-01-01T00:00:00Z', expires: '2026-09-30T22:00:00Z' };
    const current = { ...voiceAssistantDraft, valid_from: '2026-10-01T00:00:00Z' };
    const restored = restoredDraft(current, old);
    expect(restored).toEqual({ rules: [], approval: old.approval, limits: old.limits, valid_from: '2026-10-01T00:00:00Z' });
    expect('expires' in restored).toBe(false);
    const limited = { ...current, expires: '2026-12-31T23:00:00Z' };
    expect(restoredDraft(limited, { ...old, expires: undefined }).expires).toBe('2026-12-31T23:00:00Z');
  });

  it('takes the editable part of a stored version', () => {
    expect(draftOf(voiceAssistantMandate)).toEqual(voiceAssistantDraft);
    expect('expires' in draftOf(voiceAssistantMandate)).toBe(false);
    expect(draftOf({ ...voiceAssistantMandate, expires: '2027-01-01T00:00:00Z' }).expires).toBe('2027-01-01T00:00:00Z');
  });
});
