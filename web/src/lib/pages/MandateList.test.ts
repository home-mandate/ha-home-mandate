// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { BIDI_NAME, NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import MandateList from './MandateList.svelte';
import { addCriticalTemplate, DOORS } from '../test/critical.ts';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  window.location.hash = '';
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});

async function start(options: MockOptions = {}, prepare?: (api: MockClient) => Promise<void>) {
  const api = createMockClient(options);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  await prepare?.(api);
  render(MandateList, { app, now: Date.parse(NOW) });
  return { api, app };
}

const mobile = () => vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));

describe('MandateList', () => {
  it('lists the mandates with agent, rules, validity and status', async () => {
    await start();
    const table = await screen.findByRole('table', { name: 'Mandates' });
    const rows = within(table).getAllByRole('row');
    expect(rows).toHaveLength(5);
    const first = within(rows[1] as HTMLElement);
    expect(first.getByRole('link', { name: 'Sprachassistent Küche' }).getAttribute('href')).toBe('#/mandates/mandate-voice');
    expect(first.getByText('Sprachassistent').getAttribute('title')).toBe('Stated by the agent, unverified');
    expect(first.getByText('5 rules')).toBeTruthy();
    expect(first.getByText('no end date')).toBeTruthy();
    expect(first.getByText('Active')).toBeTruthy();
  });

  it('shows agent names without hidden characters and as text', async () => {
    await start();
    await screen.findByRole('table');
    expect(BIDI_NAME).toContain('\u202E');
    expect(screen.getByText('Helfer gnalnegrom x')).toBeTruthy();
    expect(document.querySelector('img')).toBeNull();
  });

  it('shows the last valid day in the household time zone and the status from the dates', async () => {
    await start({}, async (api) => {
      const { document: doc, summary } = await api.mandate('mandate-long');
      const draft = { rules: doc.rules, approval: doc.approval, limits: doc.limits, valid_from: doc.valid_from, expires: '2026-10-01T22:00:00Z' };
      await api.putMandate('mandate-long', { name: 'Old', draft, base_digest: summary.digest });
      await api.revokeMandate('mandate-bidi');
    });
    const table = await screen.findByRole('table');
    const old = within(within(table).getByRole('link', { name: 'Old' }).closest('tr') as HTMLElement);
    // Expires 2026-10-02 00:00 in Berlin: the last valid day is October 1.
    expect(old.getByText('October 1, 2026')).toBeTruthy();
    expect(old.getByText('Expired')).toBeTruthy();
    expect(within(table).getByText('Revoked')).toBeTruthy();
  });

  it('shows the templates in the decision language', async () => {
    await start();
    const section = await screen.findByRole('region', { name: 'Start from a template' });
    const cards = within(section).getAllByRole('article');
    expect(cards.map((c) => within(c).getByRole('heading').textContent)).toEqual(['Read only', 'Voice assistant', 'Empty']);
    expect(within(cards[0] as HTMLElement).getByText('All devices: read')).toBeTruthy();
    expect(within(cards[1] as HTMLElement).getByText('Camera: all actions')).toBeTruthy();
    expect(within(cards[2] as HTMLElement).getByText('Default: denied')).toBeTruthy();
    expect(within(section).getByText('Locks, gates, alarms and cameras are never allowed in templates.')).toBeTruthy();
  });

  it('says that mandates keep applying when loading fails, and recovers on retry', async () => {
    let fail = true;
    const api = createMockClient();
    const mandates = api.mandates.bind(api);
    api.mandates = async () => {
      if (fail) throw new Error('down');
      return mandates();
    };
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    render(MandateList, { app, now: Date.parse(NOW) });
    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText('Couldn’t load mandates')).toBeTruthy();
    expect(within(alert).getByText('Existing mandates keep applying unchanged.')).toBeTruthy();
    fail = false;
    await fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('table')).toBeTruthy();
  });

  it('shows a skeleton while loading', async () => {
    const api = createMockClient();
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    api.mandates = () => new Promise(() => {});
    render(MandateList, { app, now: Date.parse(NOW) });
    expect(screen.getByRole('status', { name: 'Loading …' }).getAttribute('aria-busy')).toBe('true');
    expect(screen.queryByRole('button', { name: 'New mandate' })).toBeNull();
  });

  it('explains the consequence when there is no mandate yet', async () => {
    const api = createMockClient();
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    api.mandates = async () => [];
    render(MandateList, { app, now: Date.parse(NOW) });
    expect(await screen.findByRole('heading', { name: 'No mandate yet' })).toBeTruthy();
    expect(screen.getByText('A mandate defines what an agent may do. Without one, everything is denied.')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByRole('region', { name: 'Start from a template' })).toBeTruthy();
  });

  it('still lists the mandates when Home Assistant has no device catalog', async () => {
    await start({ failures: { devices: 'unavailable' } });
    expect(await screen.findByRole('table')).toBeTruthy();
    expect(screen.getByText('lock.front_door: read, unlock')).toBeTruthy();
  });

  it('still lists the mandates when templates or agents cannot be loaded', async () => {
    await start({ failures: { templates: 'unavailable', agents: 'internal' } });
    expect(await screen.findByRole('table')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Start from a template' })).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'New mandate' }));
    expect(within(await screen.findByRole('dialog')).queryByRole('button', { name: 'Create mandate' })).toBeNull();
  });

  it('leaves out a template that cannot be loaded and keeps the others', async () => {
    const api = createMockClient();
    const template = api.template.bind(api);
    api.template = async (name) => {
      if (name === 'empty') throw new Error('gone');
      return template(name);
    };
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    render(MandateList, { app, now: Date.parse(NOW) });
    const section = await screen.findByRole('region', { name: 'Start from a template' });
    expect(within(section).getAllByRole('article')).toHaveLength(2);
  });

  it('shows cards instead of a table on mobile', async () => {
    mobile();
    await start();
    const link = await screen.findByRole('link', { name: /Sprachassistent Küche/ });
    expect(link.getAttribute('href')).toBe('#/mandates/mandate-voice');
    expect(within(link).getByText('5 rules · Valid until no end date')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  it('reloads when mandates change elsewhere', async () => {
    const { api } = await start();
    await screen.findByRole('table');
    const { document: doc, summary } = await api.mandate('mandate-voice');
    const draft = { rules: doc.rules.slice(0, 2), approval: doc.approval, limits: doc.limits, valid_from: doc.valid_from };
    await api.putMandate('mandate-voice', { name: 'Renamed', draft, base_digest: summary.digest });
    const link = await screen.findByRole('link', { name: 'Renamed' });
    expect(within(link.closest('tr') as HTMLElement).getByText('2 rules')).toBeTruthy();
  });

  it('marks a mandate whose rules name a device Home Assistant renamed', async () => {
    const { api } = await start();
    const table = await screen.findByRole('table');
    expect(within(table).queryByText(/no longer exist/)).toBeNull();
    api.control.renameDevice('lock.front_door', 'lock.front_door_main');
    const row = within(within(table).getByRole('link', { name: 'Sprachassistent Küche' }).closest('tr') as HTMLElement);
    expect(await row.findByText('1 rule names a device or area that no longer exists')).toBeTruthy();
  });

  it('marks it on mobile too', async () => {
    mobile();
    const { api } = await start();
    await screen.findByRole('link', { name: /Sprachassistent Küche/ });
    api.control.renameDevice('lock.front_door', 'lock.front_door_main');
    const links = await screen.findAllByRole('link', { name: /no longer exists/ });
    expect(links.map((l) => l.getAttribute('href'))).toContain('#/mandates/mandate-voice');
  });

  it('stops listening when it goes away', async () => {
    const { api } = await start();
    await screen.findByRole('table');
    const mandates = vi.spyOn(api, 'mandates');
    cleanup();
    api.control.emit({ type: 'mandates.changed', id: 'mandate-voice' });
    await Promise.resolve();
    expect(mandates).not.toHaveBeenCalled();
  });
});

describe('new mandate', () => {
  it('says why there is nothing to create when every active agent has a mandate', async () => {
    await start();
    await fireEvent.click(await screen.findByRole('button', { name: 'New mandate' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mandate' });
    expect(within(dialog).getByText(/Every active agent already has a mandate/)).toBeTruthy();
    expect(within(dialog).getByRole('link', { name: 'Go to agents' }).getAttribute('href')).toBe('#/agents');
    expect(within(dialog).queryByRole('button', { name: 'Create mandate' })).toBeNull();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('creates a mandate from the chosen template and opens it', async () => {
    const { api } = await start({}, async (a) => void (await a.revokeMandate('mandate-long')));
    const section = await screen.findByRole('region', { name: 'Start from a template' });
    await fireEvent.click(within(section).getByRole('button', { name: 'Use template: Read only' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mandate' });
    const template = within(dialog).getByLabelText('Template') as HTMLSelectElement;
    expect(template.value).toBe('read-only');
    const name = within(dialog).getByLabelText('Display name') as HTMLInputElement;
    expect(name.value).toBe('Read only');
    // The name follows the template until someone types one.
    await fireEvent.change(template, { target: { value: 'empty' } });
    expect(name.value).toBe('Empty');
    await fireEvent.input(name, { target: { value: 'Tablet' } });
    await fireEvent.change(template, { target: { value: 'read-only' } });
    expect(name.value).toBe('Tablet');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    await waitFor(() => expect(window.location.hash).toMatch(/^#\/mandates\/mandate-mock-\d+$/));
    const created = (await api.mandates()).find((x) => x.name === 'Tablet');
    expect(created).toMatchObject({ client_id: 'pair:long', rule_count: 1, status: 'active' });
  });

  it('creates from a critical template only after the separate confirmation (U9)', async () => {
    const { api } = await start({}, async (a) => {
      await a.revokeMandate('mandate-long');
      await addCriticalTemplate(a);
    });
    await fireEvent.click(await screen.findByRole('button', { name: 'New mandate' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mandate' });
    await fireEvent.change(within(dialog).getByLabelText('Template') as HTMLSelectElement, { target: { value: DOORS } });
    const create = vi.spyOn(api, 'createMandate');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    const box = await within(dialog).findByRole('alertdialog', { name: 'Critical actions without approval' });
    expect(within(box).getByRole('listitem').textContent).toContain('unlock');
    expect(window.location.hash).not.toMatch(/^#\/mandates\/mandate-mock/);
    // Choosing another template drops the confirmation.
    await fireEvent.change(within(dialog).getByLabelText('Template') as HTMLSelectElement, { target: { value: 'read-only' } });
    expect(within(dialog).queryByRole('alertdialog')).toBeNull();
    await fireEvent.change(within(dialog).getByLabelText('Template') as HTMLSelectElement, { target: { value: DOORS } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    const again = await within(dialog).findByRole('alertdialog');
    await fireEvent.click(within(again).getByRole('button', { name: 'Cancel' }));
    expect(within(dialog).queryByRole('alertdialog')).toBeNull();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    await fireEvent.click(within(await within(dialog).findByRole('alertdialog')).getByRole('button', { name: 'Allow without approval' }));
    await waitFor(() => expect(create).toHaveBeenLastCalledWith(expect.objectContaining({ template: DOORS, confirm_critical: true })));
    await waitFor(() => expect(window.location.hash).toMatch(/^#\/mandates\/mandate-mock-\d+$/));
  });

  it('asks for a name and reports a conflict without closing', async () => {
    const { api } = await start({}, async (a) => void (await a.revokeMandate('mandate-long')));
    await fireEvent.click(await screen.findByRole('button', { name: 'New mandate' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mandate' });
    const name = within(dialog).getByLabelText('Display name');
    await fireEvent.input(name, { target: { value: '  ' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    expect(within(dialog).getByText('Enter a name of up to 80 characters.')).toBeTruthy();
    expect(name.getAttribute('aria-invalid')).toBe('true');
    await waitFor(() => expect(document.activeElement).toBe(name));

    await fireEvent.input(name, { target: { value: 'Tablet' } });
    // Someone else creates the mandate first.
    await api.createMandate({ client_id: 'pair:long', template: 'empty' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    expect(await within(dialog).findByText('This agent has a mandate by now.')).toBeTruthy();
    expect(window.location.hash).toBe('');
  });

  it('reports any other failure', async () => {
    const { api } = await start({}, async (a) => void (await a.revokeMandate('mandate-long')));
    await fireEvent.click(await screen.findByRole('button', { name: 'New mandate' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mandate' });
    api.createMandate = async () => {
      throw new Error('boom');
    };
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create mandate' }));
    expect(await within(dialog).findByText('Couldn’t create the mandate. Please try again.')).toBeTruthy();
  });
});
