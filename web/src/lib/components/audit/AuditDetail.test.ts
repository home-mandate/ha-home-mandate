// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import type { AuditEntry } from '../../api/types.ts';
import { setLocale } from '../../paraglide/runtime.js';
import AuditDetail from './AuditDetail.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => cleanup());

const entry = (parameters?: Record<string, number | string>): AuditEntry => ({
  id: '0192-1',
  seq: 1,
  recorded_at: '2026-10-21T18:00:00.000Z',
  event: 'decision',
  agent: { client_id: 'hm-client:voice-1', display_name: 'Voice' },
  request: {
    time: '2026-10-21T18:00:00.000Z',
    resource: { entity_id: 'climate.office', category: 'climate' },
    action: 'set_mode',
    ...(parameters ? { parameters } : {}),
  },
  evaluation: { decision: 'allow', reason: 'rule', rule_id: 'r-modes' },
  result: { status: 'executed' },
  digest: 'sha256:' + '0'.repeat(64),
  prev: null,
});

const CONFIRM_WHY = 'In audit mode the matching rule would have denied the request. Because another rule asks, approval was requested before it was carried out.';

const props = (e: AuditEntry) => ({ entry: e, catalog: null, ctx: { locale: 'en', timeZone: 'UTC' }, broken: false, headingId: 'h' });

describe('AuditDetail', () => {
  it('shows the values that were evaluated', () => {
    render(AuditDetail, props(entry({ mode: 'cool' })));
    expect(screen.getByText('Values')).toBeTruthy();
    expect(screen.getByText('Mode: Cool')).toBeTruthy();
  });

  it('has no values row for an action without parameters', () => {
    render(AuditDetail, props(entry()));
    expect(screen.queryByText('Values')).toBeNull();
  });

  it('says why approval was requested for a deny that audit mode did not enforce', () => {
    const confirmed: AuditEntry = {
      ...entry(),
      evaluation: { decision: 'deny', reason: 'rule', rule_id: 'no-office', approval_timeout: 'PT2M', enforced: false, confirm: true },
      approval: { outcome: 'approved', by_name: 'Markus', via: 'ui', at: '2026-10-21T18:00:30.000Z' },
    };
    render(AuditDetail, props(confirmed));
    expect(screen.getByText(CONFIRM_WHY)).toBeTruthy();
  });

  it('has no such note for a deny that was not enforced without confirmation', () => {
    const plain: AuditEntry = {
      ...entry(),
      evaluation: { decision: 'deny', reason: 'rule', rule_id: 'no-office', enforced: false },
    };
    render(AuditDetail, props(plain));
    expect(screen.getByText('not enforced (audit mode)')).toBeTruthy();
    expect(screen.queryByText(CONFIRM_WHY)).toBeNull();
  });
});

describe('AuditDetail: entries of audit/v1', () => {
  const v1 = (e: Partial<AuditEntry>): AuditEntry => ({ ...entry(), agent: undefined, request: undefined, evaluation: undefined, result: undefined, ...e });

  it('says what happened under a neutral label, with the certificate and its validity', () => {
    render(
      AuditDetail,
      props(
        v1({
          event: 'cert.unexpected',
          certificate: {
            digest: 'NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs',
            status: 'alarm',
            names: ['hm.example.org'],
            issuer: 'CN=Evil‮ CA',
            public_key: 'NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs',
            not_before: '2026-11-01T00:00:00.000Z',
            not_after: '2027-01-30T00:00:00.000Z',
            logged: '2026-11-01T00:05:00.000Z',
          },
        }),
      ),
    );
    expect(screen.getByText('Details')).toBeTruthy();
    expect(screen.queryByText('Change')).toBeNull();
    expect(screen.getByText('CN=Evil CA')).toBeTruthy();
    expect(screen.getByText('Valid')).toBeTruthy();
  });

  it('shows an entry with a date it cannot read instead of failing', () => {
    render(AuditDetail, props(v1({ event: 'cert.unexpected', certificate: { status: 'alarm', names: ['x.example'], not_before: 'soon', not_after: 'later' } })));
    expect(screen.getByText('Details')).toBeTruthy();
    expect(screen.queryByText('Valid')).toBeNull();
  });
});
