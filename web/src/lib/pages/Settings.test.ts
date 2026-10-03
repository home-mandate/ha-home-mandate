// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { ApiError } from '../api/client.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { BrowserNotifier, type NotifyEnv } from '../app/notifier.svelte.ts';
import { AppState } from '../app/state.svelte.ts';
import type { SettingsSection } from '../router.ts';
import { setLocale } from '../paraglide/runtime.js';
import Settings from './Settings.svelte';
import { toasts } from '../ui/toasts.ts';

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

  it('marks in the index the section that is scrolled into view (review 5e)', async () => {
    let report: ((entries: { target: Element; isIntersecting: boolean }[]) => void) | null = null;
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(cb: typeof report) {
          report = cb;
        }
        observe() {}
        disconnect() {}
      },
    );
    await start({ section: 'approvers' });
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    const current = () => within(nav).getAllByRole('link').filter((l) => l.getAttribute('aria-current') === 'location').map((l) => l.textContent);
    expect(current()).toEqual(['Approvers']);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { level: 2, name: 'Approvers' })));
    await fireEvent.wheel(window); // the person scrolls
    const ha = screen.getByRole('heading', { level: 2, name: 'Home Assistant connection' }).parentElement as Element;
    report!([{ target: ha, isIntersecting: true }]);
    await waitFor(() => expect(current()).toEqual(['Home Assistant connection']));
    vi.unstubAllGlobals();
  });

  it('keeps the chosen section marked until the person scrolls (review M1)', async () => {
    let report: ((entries: { target: Element; isIntersecting: boolean }[]) => void) | null = null;
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(cb: typeof report) {
          report = cb;
        }
        observe() {}
        disconnect() {}
      },
    );
    await start({ section: 'about' });
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    const current = () => within(nav).getAllByRole('link').filter((l) => l.getAttribute('aria-current') === 'location').map((l) => l.textContent);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { level: 2, name: 'About' })));
    const ha = screen.getByRole('heading', { level: 2, name: 'Home Assistant connection' }).parentElement as Element;
    report!([{ target: ha, isIntersecting: true }]);
    await new Promise((r) => setTimeout(r, 20));
    expect(current()).toEqual(['About']);
    await fireEvent.wheel(window);
    await waitFor(() => expect(current()).toEqual(['Home Assistant connection']));
    vi.unstubAllGlobals();
  });
});

describe('Settings: approvers', () => {
  it('shows each person with devices, reach and the UI channel', async () => {
    await start();
    const markus = await card('Markus');
    expect(within(markus).getByText('Gets normal and critical requests')).toBeTruthy();
    expect(within(markus).getByRole('group', { name: 'Pixel 9' })).toBeTruthy();
    expect(within(markus).getByText('mobile_app_pixel_9')).toBeTruthy();
    expect(within(markus).getByRole('switch', { name: 'Critical requests too' }).getAttribute('aria-checked')).toBe('true');
    expect(within(markus).getByRole('switch', { name: 'Answer in Home-Mandate' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByText('Normal and critical requests reach someone.')).toBeTruthy();
  });

  it('asks before critical requests would reach nobody, then saves and shows the new reach', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    const put = vi.spyOn(api, 'putApprover');
    const toggle = within(markus).getByRole('switch', { name: 'Critical requests too' });
    await fireEvent.click(toggle);
    const confirm = within(markus).getByRole('group', { name: /critical requests reach nobody/ });
    expect(document.activeElement).toBe(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(put).not.toHaveBeenCalled();
    await fireEvent.click(within(confirm).getByRole('button', { name: 'Change anyway' }));
    await waitFor(() => expect(put).toHaveBeenCalledWith('u-admin', expect.objectContaining({ devices: [{ service: 'mobile_app_pixel_9', critical: false }] })));
    await waitFor(() => expect(within(markus).getByText('Gets normal requests only')).toBeTruthy());
    expect(screen.getByText('Critical requests reach nobody and are declined.')).toBeTruthy();
    expect(screen.getByText(/^.?Markus.?: saved\.$/)).toBeTruthy();
  });

  it('returns the focus to the switch when the confirmation is cancelled', async () => {
    await start();
    const markus = await card('Markus');
    const toggle = within(markus).getByRole('switch', { name: 'Critical requests too' });
    toggle.focus();
    await fireEvent.click(toggle);
    await fireEvent.click(within(markus).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(document.activeElement).toBe(toggle));
    expect(toggle.getAttribute('aria-checked')).toBe('true');
  });

  it('asks before critical requests go to the UI or to a device without the suggestion', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    await fireEvent.click(within(markus).getByRole('switch', { name: 'Critical requests in Home-Mandate too' }));
    expect(within(markus).getByRole('group', { name: /A computer doesn’t ask for unlocking/ })).toBeTruthy();
    await fireEvent.click(within(markus).getByRole('button', { name: 'Switch on' }));
    await waitFor(async () => expect((await api.approvers()).approvers[0]?.ui_critical).toBe(true));

    await fireEvent.change(within(markus).getByLabelText('Device'), { target: { value: 'mobile_app_macbook' } });
    await fireEvent.click(within(markus).getByRole('button', { name: 'Add device' }));
    const mac = await within(markus).findByRole('group', { name: 'MacBook Pro' });
    // Decision S11: the Mac starts without critical requests.
    expect(within(mac).getByRole('switch').getAttribute('aria-checked')).toBe('false');
    await fireEvent.click(within(mac).getByRole('switch'));
    expect(within(markus).getByRole('group', { name: /doesn’t ask for unlocking before a button counts/ })).toBeTruthy();
  });

  it('keeps a switch as the server has it when a save fails', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    api.putApprover = async () => Promise.reject(new ApiError('unavailable', 0));
    const ui = within(markus).getByRole('switch', { name: 'Answer in Home-Mandate' });
    await fireEvent.click(ui);
    await waitFor(() => expect(within(markus).getByRole('alert').textContent).toContain('wasn’t saved'));
    expect(ui.getAttribute('aria-checked')).toBe('true');
  });

  it('runs quick changes one after another, each on the latest state', async () => {
    const { api } = await start();
    const markus = await card('Markus');
    await fireEvent.change(within(markus).getByLabelText('Notification language'), { target: { value: 'de' } });
    await fireEvent.click(within(markus).getByRole('switch', { name: 'Answer in Home-Mandate' }));
    await waitFor(async () => expect((await api.approvers()).approvers[0]).toMatchObject({ language: 'de', ui: false }));
  });

  it('adds a person with their own device preselected, critical as suggested', async () => {
    const { api } = await start();
    await card('Markus');
    const device = screen.getAllByLabelText('Device').at(-1) as HTMLSelectElement;
    expect(device.value).toBe('mobile_app_iphone'); // Alex's own phone, not Markus's
    expect([...device.options].find((o) => o.value === 'mobile_app_pixel_9')?.textContent).toContain('Device of');
    await fireEvent.click(screen.getByRole('button', { name: 'Add person' }));
    const alex = await card('Alex');
    expect((await api.approvers()).approvers.find((a) => a.user_id === 'u-partner')?.devices).toEqual([{ service: 'mobile_app_iphone', critical: true }]);
    expect(within(alex).getByRole('switch', { name: 'Answer in Home-Mandate' }).getAttribute('aria-disabled')).toBe('true');
    expect(alex.textContent).toContain('Only for people with admin rights in Home Assistant.');
    expect(screen.getByText('Everyone from Home Assistant is already listed.')).toBeTruthy();
  });

  it('says when adding a person fails and keeps the choice', async () => {
    await start({ prepare: (api) => void (api.putApprover = async () => Promise.reject(new ApiError('invalid_input', 400, '/devices'))) });
    await card('Markus');
    await fireEvent.click(screen.getByRole('button', { name: 'Add person' }));
    await waitFor(() => expect(within(region('Approvers')).getAllByRole('alert').some((a) => a.textContent?.includes('Check the devices'))).toBe(true));
    expect((screen.getByLabelText('Person from Home Assistant') as HTMLSelectElement).value).toBe('u-partner');
  });

  it('says why a save was refused and shows the server state again', async () => {
    await start({ prepare: (api) => void api.putApprover('u-admin', { devices: [{ service: 'mobile_app_pixel_9', critical: true }], ui: false, ui_critical: false, language: null }) });
    const markus = await card('Markus');
    await fireEvent.click(within(markus).getByRole('button', { name: /Remove .Pixel 9./ }));
    // Markus is the only one for critical requests: removing the device asks first (decision S10).
    await fireEvent.click(within(markus).getByRole('button', { name: 'Change anyway' }));
    await waitFor(() => expect(within(markus).getByRole('alert').textContent).toContain('Check the devices'));
    expect(within(markus).getByRole('group', { name: 'Pixel 9' })).toBeTruthy();
  });

  it('shows reach only in an open Home-Mandate as such', async () => {
    await start({ prepare: (api) => void api.putApprover('u-admin', { devices: [], ui: true, ui_critical: true, language: null }) });
    const markus = await card('Markus');
    expect(within(markus).getByText('Gets requests only while Home-Mandate is open')).toBeTruthy();
    expect(screen.getByText(/Requests reach nobody by push/)).toBeTruthy();
  });

  it('keeps the focus in the card after removing a device (review a11y L5)', async () => {
    await start();
    const markus = await card('Markus');
    await fireEvent.click(within(markus).getByRole('button', { name: /^Remove .?Pixel 9.?$/ }));
    // Without the phone, critical requests would reach nobody: the card asks first.
    await fireEvent.click(await within(markus).findByRole('button', { name: 'Change anyway' }));
    await waitFor(() => expect(within(markus).queryByText('mobile_app_pixel_9')).toBeNull());
    await waitFor(() => expect(markus.contains(document.activeElement)).toBe(true));
    expect(document.activeElement?.matches('button, select')).toBe(true);
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
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText('Person from Home Assistant')));
  });

  it('sends a test notification once while one is on its way', async () => {
    let done: () => void = () => {};
    const { api } = await start({ prepare: (api) => void (api.testApprover = () => new Promise<void>((resolve) => (done = resolve))) });
    const test = vi.spyOn(api, 'testApprover');
    const button = within(await card('Markus')).getByRole('button', { name: 'Send test' });
    await fireEvent.click(button);
    await fireEvent.click(button);
    expect(test).toHaveBeenCalledTimes(1);
    expect(test).toHaveBeenCalledWith('u-admin');
    done();
  });

  it('switches the neutral note in the bell', async () => {
    const { api } = await start();
    const bell = await screen.findByRole('switch', { name: 'Also in Home Assistant’s notification bell' });
    await waitFor(() => expect(bell.getAttribute('aria-disabled')).toBeNull());
    await fireEvent.click(bell);
    await waitFor(async () => expect((await api.settings()).bell).toBe(true));
    await waitFor(() => expect(bell.getAttribute('aria-checked')).toBe('true'));
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

  it('says so when the save on leaving the page fails (review M2)', async () => {
    const { api, view } = await start();
    const rate = (await screen.findByLabelText('Default rate limit')) as HTMLInputElement;
    const show = vi.spyOn(toasts, 'show');
    api.putSettings = async () => {
      throw new ApiError('internal', 500);
    };
    await fireEvent.input(rate, { target: { value: '30' } });
    view.unmount();
    await waitFor(() => expect(show).toHaveBeenCalledWith({ kind: 'error', text: 'Not saved. Please try again.' }));
  });

  it('keeps both a bell switch and a rate change made at the same time', async () => {
    const { api } = await start();
    const rate = (await screen.findByLabelText('Default rate limit')) as HTMLInputElement;
    const bell = screen.getByRole('switch', { name: 'Also in Home Assistant’s notification bell' });
    await waitFor(() => expect(bell.getAttribute('aria-disabled')).toBeNull());
    await fireEvent.input(rate, { target: { value: '25' } });
    await fireEvent.click(bell);
    await waitFor(async () => expect(await api.settings()).toMatchObject({ bell: true, max_actions_per_hour: 25 }));
  });

  it('follows a change made elsewhere', async () => {
    const { api } = await start();
    const rate = (await screen.findByLabelText('Default rate limit')) as HTMLInputElement;
    await api.putSettings({ approval_timeout: 'PT2M', max_actions_per_hour: 99, bell: false });
    await waitFor(() => expect(rate.value).toBe('99'));
  });

  it('applies the interface language with its own button and reloads to show it', async () => {
    const { api, reload } = await start();
    const select = (await screen.findByLabelText('Interface language')) as HTMLSelectElement;
    const set = vi.spyOn(api, 'setLanguage');
    await fireEvent.change(select, { target: { value: 'de' } });
    expect(set).not.toHaveBeenCalled();
    expect([...select.options].find((o) => o.value === 'de')?.getAttribute('lang')).toBe('de');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
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
    await waitFor(() => expect(document.activeElement).toBe(within(estop).getByRole('button', { name: 'Trigger emergency stop …' })));
  });

  it('names version, commit, license with the source code and the package licenses', async () => {
    await start();
    const about = region('About');
    expect(within(about).getByRole('link', { name: /^Source code/ }).getAttribute('href')).toBe('https://github.com/home-mandate/home-mandate');
    expect(within(about).getByRole('link', { name: /^Licenses of the included packages/ }).getAttribute('href')).toBe('./licenses.txt');
    expect(about.textContent).toContain('AGPL-3.0-or-later');
  });
});
