// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import type { AuditEntry, AuditEvent, Reason } from '../api/types.ts';
import { auditFixture } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import { answeredAfter, approvalText, eventLabel, outcomeOf, reasonText } from './outcome.ts';

beforeEach(() => setLocale('en', { reload: false }));

const bySeq = (seq: number): AuditEntry => {
  const e = auditFixture.find((x) => x.seq === seq);
  if (!e) throw new Error(`no ${seq}`);
  return e;
};

describe('outcomeOf', () => {
  it('keeps decision and result apart: allowed but failed, allowed but rate limited', () => {
    expect(outcomeOf(bySeq(3))).toEqual({ tone: 'positive', text: 'Executed', why: null });
    expect(outcomeOf(bySeq(7))).toEqual({ tone: 'danger', text: 'Failed', why: 'Home Assistant: entity unavailable' });
    expect(outcomeOf(bySeq(14))).toEqual({ tone: 'danger', text: 'Declined', why: 'Rate limit' });
  });

  it('names who answered an approval, and how it ended otherwise', () => {
    expect(outcomeOf(bySeq(8))).toEqual({ tone: 'positive', text: 'Executed', why: 'Approved by Markus' });
    expect(outcomeOf(bySeq(9))).toEqual({ tone: 'danger', text: 'Declined by Alex', why: null });
    expect(outcomeOf(bySeq(4))).toEqual({ tone: 'danger', text: 'Timed out, declined', why: null });
    expect(outcomeOf(bySeq(10))).toEqual({ tone: 'warning', text: 'Invalid response discarded', why: null });
    expect(outcomeOf(bySeq(11))).toEqual({ tone: 'danger', text: 'Declined by emergency stop', why: null });
  });

  it('says why the mandate denied, or that sign-in failed', () => {
    expect(outcomeOf(bySeq(5))).toEqual({ tone: 'danger', text: 'Declined', why: 'Mandate' });
    const auth: AuditEntry = { ...bySeq(4), approval: undefined, result: { status: 'denied', denied_by: 'authentication' } };
    expect(outcomeOf(auth)).toEqual({ tone: 'danger', text: 'Declined', why: 'Sign-in' });
    const noApprover: AuditEntry = { ...bySeq(4), approval: undefined, result: { status: 'denied', denied_by: 'approval' } };
    expect(outcomeOf(noApprover)).toEqual({ tone: 'danger', text: 'Declined', why: 'Approval' });
  });

  it('shows a request without result as waiting, and nothing for administrative events', () => {
    expect(outcomeOf({ ...bySeq(4), result: undefined, approval: undefined })).toEqual({ tone: 'ask', text: 'Waiting for approval', why: null });
    expect(outcomeOf(bySeq(12))).toBeNull();
  });

  it('never shows hidden characters of an error from Home Assistant', () => {
    const failed: AuditEntry = { ...bySeq(7), result: { status: 'failed', error: 'bad\u202E\nthing' } };
    expect(outcomeOf(failed)?.why).toBe('bad thing');
  });
});

describe('eventLabel', () => {
  it('has a label for every event type', () => {
    const events: AuditEvent[] = ['decision', 'mandate.created', 'mandate.updated', 'mandate.revoked', 'agent.registered', 'agent.revoked',
      'emergency_stop.activated', 'emergency_stop.released', 'auth.rejected', 'log.truncated'];
    const labels = events.map(eventLabel);
    expect(new Set(labels).size).toBe(events.length);
    expect(labels).toContain('Emergency stop triggered');
    expect(labels).toContain('Agent approved');
  });
});

describe('reasonText', () => {
  it('explains every reason code in plain words', () => {
    const reasons: Reason[] = ['invalid_mandate', 'invalid_request', 'unknown_category', 'unknown_action', 'revoked', 'not_yet_valid',
      'expired', 'no_match', 'critical_demotion', 'rule'];
    const texts = reasons.map(reasonText);
    expect(new Set(texts).size).toBe(reasons.length);
    expect(reasonText('no_match')).toBe('No rule applies, so it was denied.');
  });
});

describe('approvalText', () => {
  it('says who answered, where, and how fast', () => {
    const e = bySeq(8);
    expect(approvalText(e)).toBe('Approved by Markus');
    const ui: AuditEntry = { ...e, approval: { ...(e.approval as NonNullable<AuditEntry['approval']>), via: 'ui' } };
    expect(approvalText(ui)).toBe('Approved by Markus · in Home-Mandate');
    expect(approvalText({ ...e, approval: { ...(e.approval as NonNullable<AuditEntry['approval']>), via: 'push' } })).toBe(
      'Approved by Markus · on the phone',
    );
    expect(approvalText(bySeq(3))).toBeNull();
  });
});

describe('answeredAfter', () => {
  it('shows minutes and seconds, with hours only when needed', () => {
    expect(answeredAfter('2026-10-02T15:00:00Z', '2026-10-02T15:00:42Z')).toBe('0:42');
    expect(answeredAfter('2026-10-02T15:00:00Z', '2026-10-02T15:02:05Z')).toBe('2:05');
    expect(answeredAfter('2026-10-02T15:00:00Z', '2026-10-02T16:00:05Z')).toBe('1:00:05');
    expect(answeredAfter('2026-10-02T15:00:05Z', '2026-10-02T15:00:00Z')).toBe('0:00');
    expect(answeredAfter('nonsense', '2026-10-02T15:00:00Z')).toBe('');
  });
});
