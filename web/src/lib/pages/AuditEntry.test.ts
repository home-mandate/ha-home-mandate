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
    expect(detail.textContent?.replace(/[\u2068\u2069]/g, '')).toContain('Declined by Alex');
    expect(within(detail).getByText('Allowed, but the action is critical, so approval was requested.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Back: Audit log' }).getAttribute('href')).toBe('#/audit');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Entry no. 9');
  });

  it('leads back to the list it was opened from, with its filters', async () => {
    const { app } = await start(9, {}, () => {});
    app.auditReturn = { list: '#/audit?period=7d', seq: 9, count: 50 };
    cleanup();
    render(AuditEntry, { app, seq: 9 });
    await screen.findByRole('article', { name: 'Entry no. 9' });
    expect(screen.getByRole('link', { name: 'Back: Audit log' }).getAttribute('href')).toBe('#/audit?period=7d');
  });

  it('says so when the entry does not exist (any more)', async () => {
    await start(999);
    expect(await screen.findByText('Page not found')).toBeTruthy();
  });

  it('warns when the entry does not fit the chain', async () => {
    await start(12, {}, (api) => api.control.breakChain(12));
    const detail = await screen.findByRole('article', { name: 'Entry no. 12' });
    expect(within(detail).getByText('This entry doesn’t fit the chain. Its content may have been altered.')).toBeTruthy();
    expect(within(detail).getByText('Emergency stop triggered')).toBeTruthy();
    expect(within(detail).getByText('Markus')).toBeTruthy();
  });

  it('says that entries are still written when loading fails', async () => {
    await start(9, { failures: { audit: 'unavailable' } });
    expect(await screen.findByText('Couldn’t load the audit log')).toBeTruthy();
  });
});

describe('removed mandates (#21)', () => {
  it('names the mandate of an entry, also after it was removed', async () => {
    const api = createMockClient({ now: () => new Date(NOW) });
    await api.revokeAgent('pair:voice-assistant');
    await api.removeAgent({ client_id: 'pair:voice-assistant', mandates: true });
    const seq = (await api.audit({ event: 'mandate.removed' })).entries[0]?.seq ?? 0;
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    render(AuditEntry, { app, seq });
    const detail = await screen.findByRole('article', { name: `Entry no. ${seq}` });
    expect(within(detail).getByText('Mandate removed')).toBeTruthy();
    expect(within(detail).getByText('Sprachassistent Küche')).toBeTruthy();
  });
});
