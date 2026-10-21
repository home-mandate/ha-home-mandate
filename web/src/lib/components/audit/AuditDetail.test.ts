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
});
