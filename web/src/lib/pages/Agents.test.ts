// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { HOSTILE_NAME, NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import Agents from './Agents.svelte';
import AgentConnect from './AgentConnect.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});

const desktop = () => vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
const mobile = () => vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));

async function start(options: MockOptions = {}, prepare?: (api: MockClient) => Promise<void> | void) {
  const api = createMockClient({ now: () => new Date(NOW), ...options });
  await prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(Agents, { app, now: Date.parse(NOW) });
  return { api, app };
}

describe('Agents', () => {
  beforeEach(desktop);

  it('lists the agents with client identity, mandate, last activity and status, active ones first', async () => {
    await start();
    const table = await screen.findByRole('table', { name: 'Agents' });
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows).toHaveLength(5);
    const claude = within(rows[1] as HTMLElement);
    // The agent's name is its claim, so screen readers hear ", unverified"; the mandate link is the mandate's name.
    const name = claude.getByRole('link', { name: 'Claude Code, unverified' });
    const mandate = claude.getByRole('link', { name: 'Claude Code' });
    expect(name?.getAttribute('href')).toBe('#/agents/https%3A%2F%2Fclaude.ai%2Foauth%2Fclaude-code-client-metadata');
    expect(mandate?.getAttribute('href')).toBe('#/mandates/mandate-claude');
    expect(claude.getByText('claude.ai')).toBeTruthy();
    expect(rows[1]?.textContent).toContain('30 minutes ago');
    // Relative times are <time> elements with the moment (review 5d), not plain text with a title.
    const seen = claude.getByText('30 minutes ago');
    expect(seen.tagName).toBe('TIME');
    expect(Number.isNaN(Date.parse(seen.getAttribute('datetime') ?? ''))).toBe(false);
    expect(claude.getByText('Active')).toBeTruthy();
    const last = within(rows.at(-1) as HTMLElement);
    expect(last.getByText('Revoked')).toBeTruthy();
    expect(last.getByText('No mandate')).toBeTruthy();
  });

  it('says when an agent has not made a request yet, and marks a paired identifier as unverified', async () => {
    await start();
    const table = await screen.findByRole('table', { name: 'Agents' });
    expect(within(table).getAllByText('No requests yet').length).toBeGreaterThan(0);
    expect(within(table).getByText('voice-assistant').closest('.claimed')?.textContent).toContain('unverified');
  });

  it('shows hostile names as text', async () => {
    await start();
    const table = await screen.findByRole('table', { name: 'Agents' });
    expect(table.querySelector('script, img')).toBeNull();
    expect(table.textContent).toContain(HOSTILE_NAME);
  });

  it('shows the ways to add an agent on request and moves the focus to the first', async () => {
    await start();
    await screen.findByRole('table', { name: 'Agents' });
    const add = screen.getByRole('button', { name: 'Add agent' });
    expect(add.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByRole('link', { name: /pairing code/i })).toBeNull();
    await fireEvent.click(add);
    expect(add.getAttribute('aria-expanded')).toBe('true');
    const code = await screen.findByRole('link', { name: /With a pairing code/ });
    expect(code.getAttribute('href')).toBe('#/agents/pair');
    expect(screen.getByRole('link', { name: /With browser sign-in/ }).getAttribute('href')).toBe('#/agents/browser');
    expect(document.activeElement).toBe(code);
  });

  it('offers the ways at once when there is no agent yet', async () => {
    await start({}, (api) => {
      api.agents = async () => [];
    });
    expect(await screen.findByText('No agent connected yet')).toBeTruthy();
    expect(screen.getByRole('link', { name: /With a pairing code/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Add agent' })).toBeNull();
  });

  it('says on a load error that admitted agents keep working', async () => {
    await start({ failures: { agents: 'unavailable' } });
    expect(await screen.findByText('Couldn’t load agents')).toBeTruthy();
    expect(screen.getByText(/keep working/)).toBeTruthy();
  });

  it('shows a revoked mandate of an active agent, but not again for a revoked agent', async () => {
    const { api } = await start();
    await screen.findByRole('table', { name: 'Agents' });
    await api.revokeMandate('mandate-voice');
    const row = await waitFor(() => {
      const r = within(screen.getByRole('table', { name: 'Agents' })).getAllByRole('row')[1] as HTMLElement;
      expect(within(r).getAllByText('Revoked')).toHaveLength(1);
      return r;
    });
    expect(within(row).getByText('Active')).toBeTruthy();
  });

  it('reloads when agents change', async () => {
    const { api } = await start();
    await screen.findByRole('table', { name: 'Agents' });
    await api.revokeAgent('https://claude.ai/oauth/claude-code-client-metadata');
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Agents' })).getAllByText('Revoked')).toHaveLength(2));
  });

  it('says that the list changed, after one reload for a burst of events', async () => {
    const { api } = await start();
    await screen.findByRole('table', { name: 'Agents' });
    const loads = vi.spyOn(api, 'agents');
    api.control.emit({ type: 'agents.changed' });
    api.control.emit({ type: 'mandates.changed', id: 'mandate-voice' });
    expect(await screen.findByText('Agent list updated')).toBeTruthy();
    expect(screen.getByText('Agent list updated').getAttribute('role')).toBe('status');
    expect(loads).toHaveBeenCalledOnce();
  });
});

describe('Agents on mobile', () => {
  beforeEach(mobile);

  it('shows cards that link to the agent', async () => {
    await start();
    const list = await screen.findByRole('list');
    const links = within(list).getAllByRole('link');
    expect(links).toHaveLength(5);
    expect(links[0]?.getAttribute('href')).toBe('#/agents/pair%3Avoice-assistant');
  });
});

describe('AgentConnect', () => {
  it('shows the MCP address to copy and an example for Claude Code', async () => {
    const api = createMockClient();
    const app = new AppState(api);
    await app.start();
    const copy = vi.fn(async () => {});
    render(AgentConnect, { app, copy });
    const field = screen.getByLabelText('MCP endpoint address') as HTMLInputElement;
    expect(field.value).toBe('https://home.example:8765/mcp');
    await fireEvent.click(screen.getByRole('button', { name: /Copy/ }));
    expect(copy).toHaveBeenCalledWith('https://home.example:8765/mcp');
    expect(screen.getByText("claude mcp add --transport http home-mandate 'https://home.example:8765/mcp'")).toBeTruthy();
  });

  it('explains a missing address instead of showing an empty field', async () => {
    const api = createMockClient();
    const system = api.system.bind(api);
    api.system = async () => ({ ...(await system()), mcp_url: null });
    const app = new AppState(api);
    await app.start();
    render(AgentConnect, { app });
    expect(screen.queryByLabelText('MCP endpoint address')).toBeNull();
    expect(screen.getByText(/no valid TLS certificate/)).toBeTruthy();
  });
});
