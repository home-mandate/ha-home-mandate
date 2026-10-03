// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { ApiError } from '../api/client.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AgentDetail from './AgentDetail.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
});

const CLAUDE = 'https://claude.ai/oauth/claude-code-client-metadata';
const VOICE = 'pair:voice-assistant';

async function start(id: string, prepare?: (api: MockClient) => void) {
  const api = createMockClient({ now: () => new Date(NOW) });
  prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(AgentDetail, { app, id, now: Date.parse(NOW) });
  return { api, app };
}

const section = (name: string) => screen.getByRole('region', { name });
const plain = (text: string | null | undefined) => (text ?? '').replace(/[⁨⁩]/g, '').replace(/\s+/g, ' ').trim();

describe('AgentDetail', () => {
  it('shows identity, sign-in way, redirect address and who approved it', async () => {
    await start(CLAUDE);
    expect(await screen.findByRole('heading', { level: 1, name: 'Claude Code' })).toBeTruthy();
    const identity = section('Identity');
    expect(within(identity).getByText('claude.ai')).toBeTruthy();
    expect(within(identity).getByText('Browser sign-in')).toBeTruthy();
    expect(within(identity).getByText('https://agent.example/oauth/callback')).toBeTruthy();
    expect(plain(identity.textContent)).toContain('by Markus');
  });

  it('shows a paired agent without redirect address', async () => {
    await start(VOICE);
    const identity = await waitFor(() => section('Identity'));
    expect(within(identity).getByText('Pairing code')).toBeTruthy();
    expect(within(identity).queryByText('Redirect address')).toBeNull();
  });

  it('shows activity, the rate limit use and the latest log entries without repeating the name', async () => {
    await start(VOICE);
    const activity = await waitFor(() => section('Last activity'));
    expect(activity.textContent).toContain('8 requests today');
    expect(section('Mandate').textContent).toContain('1 of 60 actions in the last hour');
    const log = section('This agent’s audit log');
    expect(within(log).getAllByRole('listitem')).toHaveLength(5);
    expect(within(log).queryByText('Sprachassistent')).toBeNull();
    expect(within(log).getByRole('link', { name: /Show all/ }).getAttribute('href')).toBe('#/audit?agent=pair%3Avoice-assistant');
  });

  it('revokes after a confirmation that starts on Cancel, and then offers nothing to change', async () => {
    const { api } = await start(CLAUDE);
    const end = await screen.findByRole('button', { name: 'Revoke access' });
    await fireEvent.click(end);
    const dialog = await screen.findByRole('alertdialog');
    expect(plain(within(dialog).getByRole('heading').textContent)).toBe('Revoke access for Claude Code?');
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(dialog.textContent).toContain('Pending approvals are declined.');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke access' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect((await api.agents()).find((a) => a.client_id === CLAUDE)?.status).toBe('revoked');
    await waitFor(() => expect(plain(document.body.textContent)).toContain('Revoked on'));
    expect(plain(document.body.textContent)).toContain('by Markus. This agent can’t do anything anymore.');
    expect(screen.queryByRole('button', { name: 'Revoke access' })).toBeNull();
    expect(screen.queryByLabelText('Change mandate')).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { level: 1 })));
  });

  it('keeps the dialog open with a message when revoking fails, and returns the focus on cancel', async () => {
    await start(CLAUDE, (api) => {
      api.revokeAgent = async () => Promise.reject(new ApiError('unavailable', 0));
    });
    const end = await screen.findByRole('button', { name: 'Revoke access' });
    await fireEvent.click(end);
    const dialog = await screen.findByRole('alertdialog');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke access' }));
    await waitFor(() => expect(within(dialog).getByRole('alert').textContent).toContain('emergency stop'));
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(document.activeElement).toBe(end));
  });

  it('changes the mandate to a template as a new version', async () => {
    const { api } = await start(VOICE);
    const select = (await screen.findByLabelText('Change mandate')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'read-only' } });
    const apply = vi.spyOn(api, 'applyTemplate');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(apply).toHaveBeenCalledWith('mandate-voice', expect.objectContaining({ template: 'read-only' })));
    const detail = await api.mandate('mandate-voice');
    expect(detail.versions).toHaveLength(2);
    expect(detail.document.rules.map((r) => r.id)).toEqual(['read']);
  });

  it('offers a new mandate to an active agent whose mandate was revoked', async () => {
    const { api } = await start(VOICE, (api) => void api.revokeMandate('mandate-voice'));
    const select = await screen.findByLabelText('New mandate from template');
    expect(select).toBeTruthy();
    const create = vi.spyOn(api, 'createMandate');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ client_id: VOICE })));
  });

  it('looks like any missing page for an unknown agent', async () => {
    await start('pair:nobody');
    expect(await screen.findByText('Page not found')).toBeTruthy();
  });
});
