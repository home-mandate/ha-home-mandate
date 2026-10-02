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
const count = async (text: string) => waitFor(() => expect(within(screen.getByRole('search')).getByRole('status').textContent).toBe(text));
/** plain is text without the isolates around names, for comparing. */
const plain = (text: string | null | undefined) => (text ?? '').replace(/[\u2068\u2069]/g, '').replace(/\s+/g, ' ').trim();
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
    expect(plain(detail.textContent)).toContain('Approved by Markus · after 0:42');
    // The keyboard and screen reader land on the details.
    expect(document.activeElement?.textContent).toBe('Entry no. 8');
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
    // A note, not an alert: every later entry is affected, selecting one must not interrupt.
    expect(within(detail).getByText('This entry doesn’t fit the chain. Its content may have been altered.')).toBeTruthy();
    expect(within(detail).queryByRole('alert')).toBeNull();
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

describe('AuditLog: races and edge cases', () => {
  beforeEach(desktop);

  it('drops older entries that arrive after the filter changed (load more vs filter)', async () => {
    let release: () => void = () => {};
    const { api } = await start({}, {}, async (client) => {
      for (let i = 0; i < 40; i++) await client.setEmergencyStop(i % 2 === 0);
    });
    const region = await list();
    await count('55 entries');
    const audit = api.audit.bind(api);
    api.audit = async (q) => {
      if (q.before !== undefined) await new Promise<void>((r) => (release = r));
      return audit(q);
    };
    await fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Ask first' }));
    await count('5 entries');
    release();
    await new Promise((r) => setTimeout(r, 20));
    expect(rows(region)).toHaveLength(5);
  });

  it('shows a bookmarked entry that is not on the first page, and says when one does not exist', async () => {
    await start({ seq: ['2'] }, {}, async (client) => {
      for (let i = 0; i < 40; i++) await client.setEmergencyStop(i % 2 === 0);
    });
    await count('55 entries');
    expect(await screen.findByRole('complementary', { name: 'Entry no. 2' })).toBeTruthy();
    cleanup();
    await start({ seq: ['999'] });
    await count('15 entries');
    expect(await screen.findByText('Page not found')).toBeTruthy();
  });

  it('keeps quiet when counting new entries fails', async () => {
    const { api, app } = await start();
    await count('15 entries');
    const audit = api.audit.bind(api);
    api.audit = async (q) => (q.limit === 1 ? Promise.reject(new Error('down')) : audit(q));
    await app.setEmergencyStop(true);
    await new Promise((r) => setTimeout(r, 700));
    expect(screen.queryByRole('button', { name: /new entr/ })).toBeNull();
  });

  it('leaves clicks with a modifier key to the browser (new tab)', async () => {
    await start();
    const region = await list();
    await count('15 entries');
    const row = rows(region).find((r) => within(r).queryByText('No. 8')) as HTMLElement;
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true });
    row.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(screen.queryByRole('complementary')).toBeNull();
  });

  it('keeps "Reset filters" in place, disabled without filters, so the focus stays', async () => {
    await start({ decision: ['ask'] });
    await count('5 entries');
    const reset = within(screen.getByRole('search')).getByRole('button', { name: 'Reset filters' });
    reset.focus();
    await fireEvent.click(reset);
    await count('15 entries');
    expect(document.activeElement).toBe(reset);
    expect(reset.getAttribute('aria-disabled')).toBe('true');
  });

  it('shows a filter value from the URL that is no option, so nothing is filtered invisibly', async () => {
    await start({ agent: ['pair:unknown\u202E'] });
    await count('0 entries');
    const select = screen.getByLabelText('Agent') as HTMLSelectElement;
    expect(select.selectedOptions[0]?.textContent).toBe('pair:unknown');
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
