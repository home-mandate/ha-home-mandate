// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import type { ApprovalHistoryEntry } from '../api/types.ts';
import { approvalsHistoryFixture } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import { historyOutcome } from './history.ts';

beforeEach(() => setLocale('en', { reload: false }));

/** iso is a name as the sentences show it: isolated (first strong isolate … pop). */
const iso = (name: string) => `\u2068${name}\u2069`;

const base = approvalsHistoryFixture[1] as ApprovalHistoryEntry; // approved by Markus after 0:42
const with_ = (patch: Partial<ApprovalHistoryEntry>): ApprovalHistoryEntry => ({ ...base, ...patch });

describe('historyOutcome', () => {
  it('gives every ending its own form and words (design README 6.9)', () => {
    expect(historyOutcome(base)).toEqual({ icon: 'allow', tone: 'positive', text: `Approved by ${iso('Markus')} · after 0:42`, hint: null, dashed: false });
    expect(historyOutcome(with_({ outcome: 'rejected', by_name: 'Alex', answered_at: '2026-10-02T15:00:08Z' }))).toEqual({
      icon: 'deny', tone: 'danger', text: `Declined by ${iso('Alex')} · after 0:08`, hint: null, dashed: false,
    });
    expect(historyOutcome(with_({ outcome: 'timeout', by_name: null }))).toEqual({ icon: 'ask', tone: 'muted', text: 'Timed out, declined', hint: null, dashed: true });
    expect(historyOutcome(with_({ outcome: 'invalid_response', by_name: 'Alex' }))).toMatchObject({
      icon: 'warning', tone: 'warning', text: 'Invalid response discarded', hint: expect.stringContaining('without permission'),
    });
    expect(historyOutcome(with_({ outcome: 'emergency_stop', by_name: null }))).toMatchObject({ icon: 'power', text: 'Declined by emergency stop' });
    expect(historyOutcome(with_({ outcome: 'revoked', by_name: null }))).toMatchObject({ icon: 'deny', text: 'Declined because the agent was revoked' });
  });

  it('says where an answer was given when it came from the UI or a phone (F2)', () => {
    expect(historyOutcome(with_({ via: 'ui' })).text).toBe(`Approved by ${iso('Markus')} · in Home-Mandate · after 0:42`);
    expect(historyOutcome(with_({ via: 'push' })).text).toBe(`Approved by ${iso('Markus')} · on the phone · after 0:42`);
  });

  it('never shows hidden characters of a name', () => {
    expect(historyOutcome(with_({ by_name: 'Ma\u202Erkus\n' })).text).toBe(`Approved by ${iso('Markus')} · after 0:42`);
  });
});
