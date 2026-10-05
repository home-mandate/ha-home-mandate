// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import App from './App.svelte';
import { createMockClient, type MockOptions } from './lib/api/mock.ts';
import type { ApiClient, EventHandlers } from './lib/api/client.ts';
import { AppState } from './lib/app/state.svelte.ts';
import { setLocale } from './lib/paraglide/runtime.js';

beforeEach(() => {
  setLocale('en', { reload: false });
  // jsdom has no animation frames; a browser does (Playwright covers the real path).
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => setTimeout(() => cb(performance.now()), 16));
  vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id));
});
afterEach(async () => {
  cleanup();
  window.location.hash = '';
  // Let the hashchange of this reset fire now, not into the next test's app.
  await new Promise((r) => setTimeout(r, 0));
  document.body.replaceChildren();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

async function start(options: MockOptions = {}, hash = '') {
  window.location.hash = hash;
  const api = createMockClient(options);
  const app = new AppState(api);
  render(App, { app });
  await app.start();
  await tick();
  return { api, app };
}

const navigate = async (hash: string) => {
  window.location.hash = hash;
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  await tick();
};

describe('App frame', () => {
  it('shows the audit log and a single entry', async () => {
    await start({}, '#/audit');
    expect(screen.getByRole('heading', { level: 1, name: 'Audit log' })).toBeTruthy();
    await navigate('#/audit/9');
    expect(await screen.findByRole('heading', { level: 1, name: 'Entry no. 9' })).toBeTruthy();
    // A filter changes the URL without a navigation; the "Events" link resets it.
    await navigate('#/audit');
    await fireEvent.click(await screen.findByRole('button', { name: 'Ask first' }));
    expect(window.location.hash).toBe('#/audit?decision=ask');
    await navigate('#/audit');
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Ask first' }).getAttribute('aria-pressed')).toBe('false'));
    await navigate('#/audit/requests');
    expect(await screen.findByRole('region', { name: 'History' })).toBeTruthy();
  });

  it('forgets the way back into the audit log when going elsewhere (review a11y L7)', async () => {
    const { app } = await start({}, '#/audit/8');
    app.auditReturn = { list: '#/audit?period=7d', seq: 8, count: 50 };
    await navigate('#/agents');
    expect(app.auditReturn).toBeNull();
  });

  it('shows the overview at the start', async () => {
    await start();
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeTruthy();
    expect(await screen.findByRole('region', { name: 'Status at a glance' })).toBeTruthy();
  });

  it('names the page in the title from the start, also after a reload (review a11y M6)', async () => {
    await start({}, '#/mandates');
    expect(document.title).toBe('Mandates – Home-Mandate');
  });

  it('shows the sections with the current one marked, and the page title', async () => {
    await start({}, '#/mandates');
    const nav = screen.getByRole('navigation', { name: 'Sections' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((l) => l.textContent)).toEqual(['Overview', 'Agents', 'Mandates', 'Audit log', 'Settings']);
    expect(within(nav).getByRole('link', { name: 'Mandates' }).getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Mandates');
  });

  it('opens the mandate list, the editor of a mandate and its versions', async () => {
    await start({}, '#/mandates');
    const nav = screen.getByRole('navigation', { name: 'Sections' });
    await fireEvent.click(await screen.findByRole('link', { name: 'Sprachassistent Küche' }));
    await navigate('#/mandates/mandate-voice');
    expect(await screen.findByRole('heading', { level: 1, name: 'Sprachassistent Küche' })).toBeTruthy();
    expect(within(nav).getByRole('link', { name: 'Mandates' }).getAttribute('aria-current')).toBe('page');
    await navigate('#/mandates/mandate-voice/versions');
    expect(await screen.findByRole('heading', { level: 1, name: 'Versions' })).toBeTruthy();
    expect(within(nav).getByRole('link', { name: 'Mandates' }).getAttribute('aria-current')).toBe('page');
    // Another mandate gets its own editor, not the state of the previous one.
    await navigate('#/mandates/mandate-claude');
    expect(await screen.findByRole('heading', { level: 1, name: 'Claude Code' })).toBeTruthy();
  });

  it('shows not found with a way back, and follows hash changes', async () => {
    await start({}, '#/nowhere');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Page not found');
    expect(screen.getByRole('link', { name: 'Go to overview' }).getAttribute('href')).toBe('#/');
    await navigate('#/settings');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Settings');
  });

  // Decision H1 (03.10.): an explicit confirmation instead of holding for 2 s, so every way of
  // input (tap, switch, voice, keyboard, screen reader) reaches it.
  it('triggers the emergency stop after the explicit confirmation, which starts on Cancel', async () => {
    await start();
    await fireEvent.click(screen.getByRole('button', { name: 'Emergency stop' }));
    const sheet = await screen.findByRole('alertdialog', { name: 'Trigger emergency stop?' });
    await vi.waitFor(() => expect(document.activeElement).toBe(within(sheet).getByRole('button', { name: 'Cancel' })));
    const confirm = within(sheet).getByRole('button', { name: 'Trigger emergency stop' });
    // Armed only after a moment: a double click or a held Enter cannot confirm (decision H1).
    expect(confirm.getAttribute('aria-disabled')).toBe('true');
    await fireEvent.click(confirm);
    expect(screen.getByRole('alertdialog')).toBeTruthy();
    await vi.waitFor(() => expect(confirm.getAttribute('aria-disabled')).toBeNull(), { timeout: 2000 });
    await fireEvent.click(confirm);
    await vi.waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(screen.getByRole('button', { name: 'Emergency stop on' })).toBeTruthy();
  });

  it('cancels with a click beside the sheet, Escape or Cancel, but not when the window loses focus', async () => {
    const { api } = await start();
    const stop = vi.spyOn(api, 'setEmergencyStop');
    const open = async () => {
      await fireEvent.click(screen.getByRole('button', { name: 'Emergency stop' }));
      return screen.findByRole('alertdialog');
    };
    await open();
    window.dispatchEvent(new Event('blur'));
    expect(screen.getByRole('alertdialog')).toBeTruthy();
    const backdrop = document.querySelector('.backdrop') as HTMLElement;
    await fireEvent.pointerDown(backdrop);
    await fireEvent.click(backdrop);
    await vi.waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    await open();
    await fireEvent.keyDown(window, { key: 'Escape' });
    await vi.waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    const sheet = await open();
    await fireEvent.click(within(sheet).getByRole('button', { name: 'Cancel' }));
    await vi.waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(stop).not.toHaveBeenCalled();
  });

  it('sends the active emergency stop button to lifting it in the settings', async () => {
    const { app } = await start();
    await app.setEmergencyStop(true);
    await tick();
    await fireEvent.click(screen.getByRole('button', { name: 'Emergency stop on' }));
    expect(window.location.hash).toBe('#/settings/estop');
  });

  it('orders the banners: emergency stop, chain, clock, Home Assistant', async () => {
    const { api, app } = await start();
    api.control.setHaConnected(false);
    api.control.setClockBehind(true);
    api.control.breakChain(18342);
    await app.setEmergencyStop(true);
    await tick();
    // The toast region is an (empty) alert too; the banners carry a title.
    const titles = screen
      .getAllByRole('alert')
      .map((a) => a.querySelector('strong')?.textContent)
      .filter(Boolean);
    expect(titles).toEqual(['Emergency stop active', 'Audit chain broken at entry no. 18,342', 'Clock of the host is wrong', 'Home Assistant unreachable']);
  });

  it('opens the broken entry from the chain banner', async () => {
    const { api } = await start();
    api.control.breakChain(7);
    await tick();
    await fireEvent.click(screen.getByRole('button', { name: 'Go to entry' }));
    expect(window.location.hash).toBe('#/audit/7');
  });

  it('shows the lost connection only after 5 s', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(new Date('2026-10-02T17:42:00Z'));
    let handlers: EventHandlers | null = null;
    const base = createMockClient();
    const api: ApiClient = { ...base, events: (h) => ((handlers = h), h.onState('open'), { reconnect() {}, close() {} }) };
    const app = new AppState(api);
    render(App, { app });
    await app.start();
    (handlers as unknown as EventHandlers).onState('closed');
    vi.advanceTimersByTime(4000);
    await tick();
    expect(screen.queryByText('Connection to Home-Mandate lost')).toBeNull();
    vi.advanceTimersByTime(1000);
    await tick();
    expect(screen.getByText('Connection to Home-Mandate lost')).toBeTruthy();
  });

  it('shows no access without sections or emergency stop for a non-admin', async () => {
    await start({ failures: { session: 'forbidden' } });
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('No access');
    expect(screen.queryByRole('navigation')).toBeNull();
    expect(screen.queryByRole('button', { name: /Emergency stop/ })).toBeNull();
  });

  it('says what keeps working when the service cannot be reached', async () => {
    await start({ failures: { system: 'unavailable' } });
    const alerts = screen.getAllByRole('alert').map((a) => a.textContent ?? '');
    expect(alerts.some((t) => t.includes('keep working'))).toBe(true);
  });

  it('keeps the sheet open and reports inside it when triggering fails', async () => {
    const api = createMockClient({ failures: { setEmergencyStop: 'unavailable' } });
    const app = new AppState(api);
    render(App, { app });
    await app.start();
    await tick();
    await fireEvent.click(screen.getByRole('button', { name: 'Emergency stop' }));
    const sheet = await screen.findByRole('alertdialog');
    const confirm = within(sheet).getByRole('button', { name: 'Trigger emergency stop' });
    await vi.waitFor(() => expect(confirm.getAttribute('aria-disabled')).toBeNull(), { timeout: 2000 });
    await fireEvent.click(confirm);
    await vi.waitFor(() => expect(within(sheet).getByRole('alert').textContent).toContain('could not be triggered'));
    expect(screen.getByRole('alertdialog')).toBeTruthy();
    expect(sheet.textContent).not.toContain('Emergency stop triggered');
  });

  it('moves focus to the page heading and updates the title on navigation (review a11y L2)', async () => {
    await start();
    await navigate('#/agents');
    await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { level: 1, name: 'Agents' })));
    expect(document.title).toBe('Agents – Home-Mandate');
  });

  it('moves focus to the content with the skip link', async () => {
    await start();
    await fireEvent.click(screen.getByRole('link', { name: 'Skip to content' }));
    expect(document.activeElement?.id).toBe('main');
    expect(window.location.hash).toBe('');
  });
});
