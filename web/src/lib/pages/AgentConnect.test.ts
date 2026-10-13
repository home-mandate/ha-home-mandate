// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, MOCK_PAIRING_CODE, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AgentConnect from './AgentConnect.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
});

/** settle lets the page load the agents it starts from. */
const settle = () => new Promise((r) => setTimeout(r, 20));

async function start(options: MockOptions = {}) {
  const api: MockClient = createMockClient({ now: () => new Date(NOW), ...options });
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(AgentConnect, { app });
  await settle();
  return api;
}

const connected = () => screen.queryByRole('region', { name: /is connected/ });

describe('AgentConnect (#35)', () => {
  it('shows the agent admitted through the browser sign-in, with the next steps', async () => {
    const api = await start();
    expect(connected()).toBeNull();
    expect(screen.getByText('Once you’ve approved it, the agent appears here with the next steps.')).toBeTruthy();

    const clientId = api.control.admitByBrowser('Claude Code', 'hm-voice-cautious');

    const heading = await screen.findByRole('heading', { name: 'Claude Code is connected' });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    const region = connected() as HTMLElement;
    expect(within(region).getByRole('link', { name: 'Go to agent' }).getAttribute('href')).toBe(`#/agents/id/${encodeURIComponent(clientId)}`);
    expect(within(region).getByRole('link', { name: 'Adjust mandate' }).getAttribute('href')).toMatch(/^#\/mandates\//);
    const steps = within(region).getByRole('list', { name: 'Next steps' });
    expect(steps.textContent).toMatch(/Check what the mandate .* allows/);
    expect(steps.textContent).toContain('ask your agent “What are you allowed to do in my home?”');
    expect(await within(region).findByText('Approval requests of this mandate reach someone.')).toBeTruthy();
    // The steps stay, for another agent.
    expect(screen.getByText('Enter this address in your agent as an MCP server.')).toBeTruthy();
  });

  it('warns and points to the settings when nobody can answer approval requests', async () => {
    const api = await start({ noApprovers: true });
    api.control.admitByBrowser('Claude Code', 'hm-voice-cautious');
    const region = await screen.findByRole('region', { name: /is connected/ });
    expect(await within(region).findByText(/Nobody can answer approval requests for critical actions of this mandate yet/)).toBeTruthy();
    expect(within(region).getByRole('link', { name: 'Set up approvers' }).getAttribute('href')).toBe('#/settings/approvers');
  });

  it('says so when it cannot tell whether approval requests reach anyone', async () => {
    const api = await start({ failures: { mandateApprovers: 'unavailable' } });
    api.control.admitByBrowser('Claude Code', 'hm-voice-cautious');
    const region = await screen.findByRole('region', { name: /is connected/ });
    expect(await within(region).findByText('Could not check whether anyone can answer approval requests of this mandate.')).toBeTruthy();
  });

  it('ignores the agents that were there before', async () => {
    const api = await start();
    api.control.emit({ type: 'agents.changed' });
    await new Promise((r) => setTimeout(r, 300)); // after the coalesced reload
    expect(connected()).toBeNull();
  });
});

describe('AgentConnect (#35), changes on the way', () => {
  it('finds the agent when the first load of the list failed', async () => {
    const api: MockClient = createMockClient({ now: () => new Date(NOW) });
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    const agents = api.agents.bind(api);
    let calls = 0;
    api.agents = async () => {
      if (++calls === 1) throw new Error('offline');
      return agents();
    };
    render(AgentConnect, { app });
    await settle();
    api.control.admitByBrowser('Claude Code', 'hm-voice-cautious');
    expect(await screen.findByRole('heading', { name: 'Claude Code is connected' })).toBeTruthy();
  });

  it('shows the newest of two agents and drops one that was revoked', async () => {
    const api = await start();
    api.control.admitByBrowser('First', 'hm-read-only');
    await screen.findByRole('heading', { name: 'First is connected' });
    const second = api.control.admitByBrowser('Second', 'hm-read-only');
    await screen.findByRole('heading', { name: 'Second is connected' });
    await api.revokeAgent(second);
    await waitFor(() => expect(connected()).toBeNull());
  });

  it('does not show an agent paired by code', async () => {
    const api = await start();
    const candidate = await api.pairingCheck(MOCK_PAIRING_CODE);
    await api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: candidate.pairing_id, display_name: 'Tablet', template: 'hm-read-only' });
    await new Promise((r) => setTimeout(r, 300)); // after the coalesced reload
    expect(connected()).toBeNull();
  });
});
