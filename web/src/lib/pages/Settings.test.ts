// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { BrowserNotifier, type NotifyEnv } from '../app/notifier.svelte.ts';
import { AppState } from '../app/state.svelte.ts';
import type { SettingsSection } from '../router.ts';
import { setLocale } from '../paraglide/runtime.js';
import Settings from './Settings.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.useRealTimers();
});

interface Options {
  section?: SettingsSection | null;
  prepare?: (api: MockClient) => Promise<void> | void;
  notify?: NotifyEnv;
}

async function start({ section = null, prepare, notify = { hidden: () => true } }: Options = {}) {
  const api = createMockClient({ now: () => new Date(NOW) });
  await prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  const onestop = vi.fn();
  const reload = vi.fn();
  const memory = new Map<string, string>();
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => void memory.set(k, v), removeItem: (k: string) => void memory.delete(k) };
  const notifier = new BrowserNotifier(notify);
  const view = render(Settings, { app, notifier, section, onestop, storage, reload });
  return { api, app, onestop, reload, notifier, view };
}

const region = (name: string) => screen.getByRole('region', { name });
const card = async (name: string) => within(await screen.findByRole('region', { name: 'Approvers' })).findByRole('article', { name });

describe('Settings: frame', () => {
  it('lists the sections and moves the focus to the one in the URL', async () => {
    await start({ section: 'mcp' });
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    expect(within(nav).getAllByRole('link').map((a) => a.getAttribute('href'))).toEqual([
      '#/settings/approvers',
      '#/settings/defaults',
      '#/settings/ha',
      '#/settings/mcp',
      '#/settings/retention',
      '#/settings/estop',
      '#/settings/about',
    ]);
    expect(within(nav).getByRole('link', { name: 'MCP endpoint' }).getAttribute('aria-current')).toBe('location');
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { level: 2, name: 'MCP endpoint' })));
  });
});

describe('Settings: approvers', () => {
  it('shows each person with devices, reach and the UI channel', async () => {
    await start();
    const markus = await card('Markus');
    expect(within(markus).getByText('Gets normal and critical requests')).toBeTruthy();
    expect(within(markus).getByRole('group', { name: 'Pixel 9' })).toBeTruthy();
    expect(within(markus).getByRole('switch', { name: 'Critical requests too' }).getAttribute('aria-checked')).toBe('true');
    expect(within(markus).getByRole('switch', { name: 'Answer in Home-Mandate' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByText('Normal and critical requests reach someone.')).toBeTruthy();
  });

  it('saves a change at once and shows the reach the server computes', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    const put = vi.spyOn(api, 'putApprover');
    await fireEvent.click(within(markus).getByRole('switch', { name: 'Critical requests too' }));
    await waitFor(() => expect(put).toHaveBeenCalledWith('u-admin', expect.objectContaining({ devices: [{ service: 'mobile_app_pixel_9', critical: false }] })));
    await waitFor(() => expect(within(markus).getByText('Gets normal requests only')).toBeTruthy());
    expect(screen.getByText('Critical requests reach nobody and are declined.')).toBeTruthy();
  });

  it('adds a device with the suggestion for critical requests', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    await fireEvent.change(within(markus).getByLabelText('Device'), { target: { value: 'mobile_app_macbook' } });
    await fireEvent.click(within(markus).getByRole('button', { name: 'Add device' }));
    await waitFor(async () => expect((await api.approvers()).approvers[0]?.devices).toContainEqual({ service: 'mobile_app_macbook', critical: false }));
    expect(await within(markus).findByRole('group', { name: 'MacBook Pro' })).toBeTruthy();
  });

  it('adds a person with a device; the UI channel is only for administrators', async () => {
    await start();
    await card('Markus');
    await fireEvent.change(screen.getByLabelText('Person from Home Assistant'), { target: { value: 'u-partner' } });
    await fireEvent.change(screen.getAllByLabelText('Device').at(-1) as HTMLElement, { target: { value: 'mobile_app_iphone' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add person' }));
    const alex = await card('Alex');
    const ui = within(alex).getByRole('switch', { name: 'Answer in Home-Mandate' });
    expect(ui.getAttribute('aria-disabled')).toBe('true');
    expect(alex.textContent).toContain('Only for people with admin rights in Home Assistant.');
    expect(screen.getByText('Everyone from Home Assistant is already listed.')).toBeTruthy();
  });

  it('says why a save was refused and shows the server state again', async () => {
    await start({ prepare: (api) => void api.putApprover('u-admin', { devices: [{ service: 'mobile_app_pixel_9', critical: true }], ui: false, ui_critical: false, language: null }) });
    const markus = await card('Markus');
    await fireEvent.click(within(markus).getByRole('button', { name: /Remove .Pixel 9./ }));
    await waitFor(() => expect(within(markus).getByRole('alert').textContent).toContain('Check the devices'));
    expect(within(markus).getByRole('group', { name: 'Pixel 9' })).toBeTruthy();
  });

  it('asks inline before removing the last person who gets requests', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    await fireEvent.click(within(markus).getByRole('button', { name: /Remove .Markus./ }));
    const group = within(markus).getByRole('group', { name: /only person for approvals/ });
    expect(document.activeElement).toBe(within(group).getByRole('button', { name: 'Cancel' }));
    const remove = vi.spyOn(api, 'deleteApprover');
    await fireEvent.click(within(group).getByRole('button', { name: 'Remove anyway' }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith('u-admin'));
    expect(await screen.findByText('At least one person must receive approvals.')).toBeTruthy();
  });

  it('sends a test notification', async () => {
    const { api } = await start();
    const test = vi.spyOn(api, 'testApprover');
    await fireEvent.click(within(await card('Markus')).getByRole('button', { name: 'Send test' }));
    expect(test).toHaveBeenCalledWith('u-admin');
  });

  it('switches the neutral note in the bell', async () => {
    const { api } = await start();
    const bell = await screen.findByRole('switch', { name: 'Also in Home Assistant’s notification bell' });
    await waitFor(() => expect(bell.getAttribute('aria-disabled')).toBeNull());
    await fireEvent.click(bell);
    await waitFor(async () => expect((await api.settings()).bell).toBe(true));
  });

  it('offers browser notifications to the signed-in approver who answers in the UI', async () => {
    const requestPermission = vi.fn(async () => 'granted' as NotificationPermission);
    const Notification = Object.assign(function () {}, { permission: 'default', requestPermission }) as unknown as typeof globalThis.Notification;
    const { notifier } = await start({ notify: { Notification, hidden: () => true } });
    await card('Markus');
    await fireEvent.click(screen.getByRole('button', { name: 'Allow notifications' }));
    await waitFor(() => expect(notifier.state).toBe('on'));
    expect(requestPermission).toHaveBeenCalled();
    expect(screen.getByText('Switched on in this browser.')).toBeTruthy();
  });
});

describe('Settings: defaults', () => {
  it('saves valid changes after a pause and refuses invalid ones', async () => {
    const { api } = await start();
    const rate = (await screen.findByLabelText('Default rate limit')) as HTMLInputElement;
    const put = vi.spyOn(api, 'putSettings');
    await fireEvent.input(rate, { target: { value: '0' } });
    expect(await screen.findByText('Between 1 and 1000 actions per hour.')).toBeTruthy();
    await new Promise((r) => setTimeout(r, 450));
    expect(put).not.toHaveBeenCalled();
    await fireEvent.input(rate, { target: { value: '30' } });
    await waitFor(() => expect(put).toHaveBeenCalledWith(expect.objectContaining({ max_actions_per_hour: 30 })));
    expect(await within(region('Defaults')).findByText('Saved')).toBeTruthy();
  });

  it('changes the interface language and reloads to show it', async () => {
    const { api, reload } = await start();
    const select = (await screen.findByLabelText('Interface language')) as HTMLSelectElement;
    const set = vi.spyOn(api, 'setLanguage');
    await fireEvent.change(select, { target: { value: 'de' } });
    await waitFor(() => expect(set).toHaveBeenCalledWith('de'));
    expect(reload).toHaveBeenCalled();
  });
});

describe('Settings: system sections', () => {
  it('shows the Home Assistant connection with its user and fixed command list', async () => {
    await start();
    const ha = region('Home Assistant connection');
    expect(ha.textContent).toContain('2026.9.4');
    expect(ha.textContent).toContain('Home-Mandate');
    expect(within(ha).getByText('call_service')).toBeTruthy();
    expect(within(ha).queryByLabelText(/token/i)).toBeNull();
  });

  it('shows the MCP address and the certificate, amber when it is missing', async () => {
    await start({
      prepare: (api) => {
        const system = api.system.bind(api);
        api.system = async () => ({ ...(await system()), tls: { present: false, valid_until: null } });
      },
    });
    const mcp = region('MCP endpoint');
    expect((within(mcp).getByLabelText('MCP endpoint address') as HTMLInputElement).value).toBe('https://home.example:8765/mcp');
    expect(within(mcp).getByText(/No certificate found/)).toBeTruthy();
  });

  it('triggers the emergency stop through the frame and lifts it after an inline confirmation', async () => {
    const { onestop, app } = await start();
    const estop = region('Emergency stop');
    await fireEvent.click(within(estop).getByRole('button', { name: 'Trigger emergency stop …' }));
    expect(onestop).toHaveBeenCalled();
    await app.setEmergencyStop(true);
    const lift = await within(estop).findByRole('button', { name: 'Lift emergency stop …' });
    await fireEvent.click(lift);
    const group = within(estop).getByRole('group', { name: 'Lift emergency stop?' });
    expect(document.activeElement).toBe(within(group).getByRole('button', { name: 'Cancel' }));
    await fireEvent.click(within(group).getByRole('button', { name: 'Lift' }));
    await waitFor(() => expect(app.system?.emergency_stop.active).toBe(false));
  });

  it('names version, commit, license with the source code and the package licenses', async () => {
    await start();
    const about = region('About');
    expect(within(about).getByRole('link', { name: 'Source code' }).getAttribute('href')).toBe('https://github.com/home-mandate/home-mandate');
    expect(within(about).getByRole('link', { name: 'Licenses of the included packages' }).getAttribute('href')).toBe('./licenses.txt');
    expect(about.textContent).toContain('AGPL-3.0-or-later');
  });
});
