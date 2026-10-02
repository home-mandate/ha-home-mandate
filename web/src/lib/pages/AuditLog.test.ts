// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AuditLog from './AuditLog.svelte';

beforeEach(() => {
  setLocale('en', { reload: false });
  window.history.replaceState(null, '', '#/audit');
});
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});

const now = () => new Date(NOW);
const desktop = () => vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
const mobile = () => vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));

async function start(query: Record<string, string[]> = {}, options: MockOptions = {}, prepare?: (api: MockClient) => Promise<void> | void) {
  const api = createMockClient({ now, ...options });
  await prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(AuditLog, { app, now: Date.parse(NOW), query });
  return { api, app };
}

const list = () => screen.findByRole('region', { name: 'Events' });
const count = async (text: string) => waitFor(() => expect(screen.getByRole('status').textContent).toBe(text));
const rows = (region: HTMLElement) => within(region).getAllByRole('link').filter((l) => l.closest('li'));

describe('AuditLog', () => {
  beforeEach(desktop);

  it('lists the entries newest first, grouped by household day, with the number of matches', async () => {
    await start();
    const region = await list();
    await count('15 entries');
    const headings = within(region).getAllByRole('heading', { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(['Today', 'Yesterday']);
    expect(rows(region)).toHaveLength(15);
  });

  it('keeps decision and result apart, and shows administrative events with their own label', async () => {
    await start();
    const region = await list();
    await count('15 entries');
    const [auth, rate] = rows(region);
    expect(within(auth as HTMLElement).getByText('Sign-in rejected')).toBeTruthy();
    expect(within(auth as HTMLElement).queryByText('Allowed')).toBeNull();
    expect(within(rate as HTMLElement).getByText('Allowed')).toBeTruthy();
    expect(within(rate as HTMLElement).getByText('Declined')).toBeTruthy();
    expect(within(rate as HTMLElement).getByText('Rate limit')).toBeTruthy();
    expect(within(rate as HTMLElement).getByText('No. 14')).toBeTruthy();
  });

  it('filters by decision chips and writes the filter into the URL', async () => {
    await start();
    await count('15 entries');
    const chip = screen.getByRole('button', { name: 'Ask first' });
    await fireEvent.click(chip);
    expect(chip.getAttribute('aria-pressed')).toBe('true');
    await count('5 entries');
    expect(window.location.hash).toBe('#/audit?decision=ask');
  });

  it('starts with the filters from the URL and resets them', async () => {
    await start({ type: ['emergency_stop.activated'] });
    await count('1 entry');
    expect(within(await list()).getByText('Emergency stop triggered')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Reset filters' }));
    await count('15 entries');
    expect(window.location.hash).toBe('#/audit');
  });

  it('says when no entry matches and offers to reset', async () => {
    await start({ agent: ['pair:nobody'] });
    expect(await screen.findByText('No entries for these filters')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: 'Reset filters' }).length).toBeGreaterThan(0);
  });

  it('shows the details of a selected entry next to the list', async () => {
    await start();
    const region = await list();
    await count('15 entries');
    const approved = rows(region).find((r) => within(r).queryByText('No. 8'));
    await fireEvent.click(approved as HTMLElement);
    const detail = await screen.findByRole('complementary', { name: 'Entry no. 8' });
    expect(within(detail).getByText('Approved by Markus · after 0:42')).toBeTruthy();
    expect(within(detail).getByText('A rule of the mandate decided.')).toBeTruthy();
    expect(within(detail).getByText('Haustür')).toBeTruthy();
    expect(within(detail).getByText('lock.front_door')).toBeTruthy();
    expect(within(detail).getByText('Technical details')).toBeTruthy();
    expect(window.location.hash).toBe('#/audit?seq=8');
  });

  it('marks the entries from the first broken one on, and says so in the details', async () => {
    await start({ seq: ['10'] }, {}, (api) => api.control.breakChain(10));
    const region = await list();
    await count('15 entries');
    const marked = rows(region).filter((r) => within(r).queryByRole('img', { name: 'This entry doesn’t fit the chain. Its content may have been altered.' }));
    expect(marked.map((r) => within(r).getByText(/^No\./).textContent)).toEqual(['No. 15', 'No. 14', 'No. 13', 'No. 12', 'No. 11', 'No. 10']);
    const detail = await screen.findByRole('complementary', { name: 'Entry no. 10' });
    expect(within(detail).getByRole('alert').textContent).toContain('doesn’t fit the chain');
  });

  it('loads older entries with a stable cursor', async () => {
    await start({}, {}, async (api) => {
      for (let i = 0; i < 40; i++) await api.setEmergencyStop(i % 2 === 0);
    });
    const region = await list();
    await count('55 entries');
    expect(rows(region)).toHaveLength(50);
    await fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(rows(region)).toHaveLength(55));
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('does not move the list for new entries, but offers them', async () => {
    const { app } = await start();
    const region = await list();
    await count('15 entries');
    await app.setEmergencyStop(true);
    const pill = await screen.findByRole('button', { name: '1 new entry' });
    expect(rows(region)).toHaveLength(15);
    await fireEvent.click(pill);
    await waitFor(() => expect(rows(region)).toHaveLength(16));
    expect(within(rows(region)[0] as HTMLElement).getByText('Emergency stop triggered')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /new entr/ })).toBeNull();
  });

  it('verifies the chain on request', async () => {
    const { api } = await start();
    await count('15 entries');
    const spy = vi.spyOn(api, 'verifyAudit');
    await fireEvent.click(screen.getByRole('button', { name: 'Verify now' }));
    await waitFor(() => expect(spy).toHaveBeenCalledOnce());
    expect(await screen.findByText(/Last checked now/)).toBeTruthy();
  });

  it('says that new entries are still written when loading fails', async () => {
    await start({}, { failures: { audit: 'unavailable' } });
    expect(await screen.findByText('Couldn’t load the audit log')).toBeTruthy();
    expect(screen.getByText('New entries are still being written.')).toBeTruthy();
  });

  it('links to the approvals with the number of pending ones', async () => {
    await start();
    const nav = await screen.findByRole('navigation', { name: 'Audit log' });
    expect(within(nav).getByRole('link', { name: 'Events' }).getAttribute('aria-current')).toBe('page');
    const requests = within(nav).getByRole('link', { name: /Approvals/ });
    expect(requests.getAttribute('href')).toBe('#/audit/requests');
    await waitFor(() => expect(requests.textContent).toContain('1'));
  });
});

describe('AuditLog on mobile', () => {
  beforeEach(mobile);

  it('opens an entry on its own page', async () => {
    await start();
    const region = await list();
    await count('15 entries');
    const row = rows(region).find((r) => within(r).queryByText('No. 8'));
    expect(row?.getAttribute('href')).toBe('#/audit/8');
    expect(screen.queryByRole('complementary')).toBeNull();
  });
});
