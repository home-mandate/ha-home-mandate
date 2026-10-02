// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AuditEntry from './AuditEntry.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
});

async function start(seq: number, options: MockOptions = {}, prepare?: (api: MockClient) => void) {
  const api = createMockClient({ now: () => new Date(NOW), ...options });
  prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(AuditEntry, { app, seq });
  return { api, app };
}

describe('AuditEntry', () => {
  it('shows one entry on its own page with a way back', async () => {
    await start(9);
    const detail = await screen.findByRole('article', { name: 'Entry no. 9' });
    expect(within(detail).getByText('Declined by Alex')).toBeTruthy();
    expect(within(detail).getByText('Allowed, but the action is critical, so approval was requested.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Audit log' }).getAttribute('href')).toBe('#/audit');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Entry no. 9');
  });

  it('says so when the entry does not exist (any more)', async () => {
    await start(999);
    expect(await screen.findByText('Page not found')).toBeTruthy();
  });

  it('warns when the entry does not fit the chain', async () => {
    await start(12, {}, (api) => api.control.breakChain(12));
    const detail = await screen.findByRole('article', { name: 'Entry no. 12' });
    expect(within(detail).getByRole('alert').textContent).toContain('doesn’t fit the chain');
    expect(within(detail).getByText('Emergency stop triggered')).toBeTruthy();
    expect(within(detail).getByText('Markus')).toBeTruthy();
  });

  it('says that entries are still written when loading fails', async () => {
    await start(9, { failures: { audit: 'unavailable' } });
    expect(await screen.findByText('Couldn’t load the audit log')).toBeTruthy();
  });
});
