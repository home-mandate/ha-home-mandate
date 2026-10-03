// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import type { ApprovalRequest } from '../api/types.ts';
import { NOW, approvalsOpenFixture } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import Overview from './Overview.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
});

const now = () => new Date(NOW);

async function start(options: MockOptions = {}, prepare?: (api: MockClient) => void | Promise<void>) {
  const api = createMockClient({ now, ...options });
  await prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(Overview, { app, now: Date.parse(NOW) });
  return { api, app };
}

/** plain is text without the isolates around names, for comparing. */
const plain = (text: string | null | undefined) => (text ?? '').replace(/[\u2068\u2069]/g, '');

/** tiles waits until the status tiles show content, not the skeleton. */
async function tiles(): Promise<HTMLElement> {
  const region = await screen.findByRole('region', { name: 'Status at a glance' });
  await within(region).findByText('Home Assistant');
  return region;
}
const tile = (region: HTMLElement, label: string) => {
  const el = within(region).getByText(label).closest('.tile');
  if (!(el instanceof HTMLElement)) throw new Error(`no tile ${label}`);
  return within(el);
};

describe('Overview', () => {
  it('shows Home Assistant, emergency stop, agents and pending approvals at a glance', async () => {
    await start();
    const region = await tiles();
    expect(tile(region, 'Home Assistant').getByText('Connected')).toBeTruthy();
    expect(tile(region, 'Emergency stop').getByText('Off')).toBeTruthy();
    expect(tile(region, 'Emergency stop').getByText('Agents act within their mandates')).toBeTruthy();
    await waitFor(() => expect(tile(region, 'Active agents').getByText('4')).toBeTruthy());
    expect(tile(region, 'Active agents').getByText('of 4 approved')).toBeTruthy();
    expect(tile(region, 'Pending approvals').getByText('1')).toBeTruthy();
    expect(tile(region, 'Pending approvals').getByText('Approve on your phone')).toBeTruthy();
  });

  it('says the times are household times', async () => {
    await start();
    expect(await screen.findByText('Times in household time (Europe/Berlin)')).toBeTruthy();
  });

  it('lists pending approvals; answering is on the phone unless this person may answer here', async () => {
    await start();
    const section = await screen.findByRole('region', { name: /Pending approvals/ });
    const cards = await within(section).findAllByRole('article');
    expect(cards).toHaveLength(1);
    expect(within(section).getByText('Approve or decline from the notification on your phone.')).toBeTruthy();
    expect(within(section).queryByRole('link', { name: 'Answer in Approvals' })).toBeNull();
  });

  it('links to the approvals page when this person may answer a request here (F2)', async () => {
    await start({}, (api) => {
      const request = { ...(approvalsOpenFixture[0] as ApprovalRequest), id: 'apr-ui', can_answer: true };
      api.control.closeApproval('apr-1', 'timeout', null);
      api.control.openApproval(request);
    });
    const section = await screen.findByRole('region', { name: /Pending approvals/ });
    const link = await within(section).findByRole('link', { name: 'Answer in Approvals' });
    expect(link.getAttribute('href')).toBe('#/audit/requests');
    expect(within(section).getByText('Approve or decline on your phone or under Approvals.')).toBeTruthy();
    expect(tile(await tiles(), 'Pending approvals').getByText('Approve here or on your phone')).toBeTruthy();
  });

  it('shows the five latest decisions with device names and a link to the audit log', async () => {
    await start();
    const section = await screen.findByRole('region', { name: 'Recent activity' });
    const items = await within(section).findAllByRole('listitem');
    expect(items).toHaveLength(5);
    const first = within(items[0] as HTMLElement);
    expect(first.getByText('Allowed')).toBeTruthy();
    expect(first.getByText(/Küchenlicht · turn off/)).toBeTruthy();
    expect(first.getByText('Sprachassistent').getAttribute('title')).toBe('Stated by the agent, unverified');
    expect(within(section).getByRole('link', { name: 'Open audit log' }).getAttribute('href')).toBe('#/audit');
    expect(within(section).getByText('Audit log complete and unaltered')).toBeTruthy();
  });

  it('marks critical requests in the activity', async () => {
    await start();
    const section = await screen.findByRole('region', { name: 'Recent activity' });
    const items = await within(section).findAllByRole('listitem');
    const critical = items.filter((li) => within(li).queryByTitle('Critical'));
    expect(critical.length).toBeGreaterThan(0);
    expect(within(critical[0] as HTMLElement).getByText(/Haustür|Garagentor|Kamera Einfahrt|Alarmanlage/)).toBeTruthy();
  });

  it('welcomes a household without agents with the two first steps', async () => {
    await start({}, (api) => {
      api.agents = async () => [];
    });
    const welcome = await screen.findByRole('region', { name: 'Welcome to Home-Mandate' });
    expect(within(welcome).getByText('0 of 2 done')).toBeTruthy();
    expect(within(welcome).getByRole('link', { name: 'Connect agent' }).getAttribute('href')).toBe('#/agents/pair');
    const locked = within(welcome).getByRole('button', { name: 'Create mandate' });
    expect(locked.getAttribute('aria-disabled')).toBe('true');
    expect(locked.getAttribute('aria-describedby')).toBeTruthy();
    expect(within(welcome).getByText('Until then, everything is denied.')).toBeTruthy();
    expect(tile(await tiles(), 'Active agents').getByText('None connected yet')).toBeTruthy();
  });

  it('says what keeps working when loading fails, and recovers on retry', async () => {
    let fail = true;
    const { api } = await start({}, (client) => {
      const approvals = client.approvals.bind(client);
      client.approvals = async () => {
        if (fail) throw new Error('down');
        return approvals();
      };
    });
    expect(await screen.findByText('Couldn’t load the overview')).toBeTruthy();
    expect(screen.getByText(/Mandates and the emergency stop keep working/)).toBeTruthy();
    fail = false;
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('region', { name: /Pending approvals/ })).toBeTruthy();
    expect(api).toBeTruthy();
  });

  it('shows the emergency stop: no active agents, the stop active, no pending approvals', async () => {
    const { app } = await start();
    await tiles();
    await app.setEmergencyStop(true);
    const region = await tiles();
    await waitFor(() => expect(tile(region, 'Emergency stop').getByText('Active')).toBeTruthy());
    expect(tile(region, 'Emergency stop').getByText('All agents blocked')).toBeTruthy();
    await waitFor(() => expect(tile(region, 'Active agents').getByText('0')).toBeTruthy());
    await waitFor(() => expect(tile(region, 'Pending approvals').getByText('0')).toBeTruthy());
  });

  it('says that approvals cannot be delivered while Home Assistant is down', async () => {
    const { api } = await start();
    await tiles();
    api.control.setHaConnected(false);
    expect(await screen.findByText(/approvals can’t be delivered/)).toBeTruthy();
    expect(tile(await tiles(), 'Home Assistant').getByText('Disconnected')).toBeTruthy();
  });

  it('follows new and answered approvals live', async () => {
    const { api } = await start();
    const section = await screen.findByRole('region', { name: /Pending approvals/ });
    await within(section).findAllByRole('article');
    api.control.openApproval({ ...(approvalsOpenFixture[0] as ApprovalRequest), id: 'apr-2', device_name: 'Garagentor' });
    await waitFor(() => expect(within(section).getAllByRole('article')).toHaveLength(2));
    api.control.closeApproval('apr-1', 'approved', 'Markus');
    api.control.closeApproval('apr-2', 'rejected', 'Alex');
    expect(await within(section).findByText('No pending approvals')).toBeTruthy();
  });

  it('announces a new request and reloads once for a burst of events (review L24)', async () => {
    const { api } = await start();
    const section = await screen.findByRole('region', { name: /Pending approvals/ });
    await within(section).findAllByRole('article');
    const loads = vi.spyOn(api, 'approvals');
    api.control.openApproval({ ...(approvalsOpenFixture[0] as ApprovalRequest), id: 'apr-2', device_name: 'Garagentor' });
    api.control.emit({ type: 'audit.appended', seq: 99 });
    api.control.emit({ type: 'agents.changed' });
    const said = await screen.findByText((_, el) => el?.getAttribute('role') === 'status' && plain(el.textContent).startsWith('New approval request: Claude Code'));
    expect(plain(said.textContent)).toBe('New approval request: Claude Code wants to unlock Garagentor');
    await waitFor(() => expect(within(section).getAllByRole('article')).toHaveLength(2));
    expect(loads).toHaveBeenCalledOnce();
  });
});
