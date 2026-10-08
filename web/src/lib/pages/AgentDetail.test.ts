// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { ApiError } from '../api/client.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AgentDetail from './AgentDetail.svelte';
import { addCriticalTemplate, DOORS } from '../test/critical.ts';
import { toasts } from '../ui/toasts.ts';

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

const section = (name: string) => screen.getByRole('group', { name });
const plain = (text: string | null | undefined) => (text ?? '').replace(/[\u2068\u2069]/g, '').replace(/\s+/g, ' ').trim();

describe('AgentDetail', () => {
  it('shows identity, sign-in way, redirect address and who approved it', async () => {
    await start(CLAUDE);
    expect(await screen.findByRole('heading', { level: 1, name: 'Claude Code, unverified' })).toBeTruthy();
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
    expect(activity.querySelector('time')?.getAttribute('datetime')).toMatch(/^2026-/);
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
    expect(screen.queryByLabelText('Take over rules from a template')).toBeNull();
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

  it('shows the revoke when its answer was lost but it went through', async () => {
    await start(CLAUDE, (api) => {
      const revoke = api.revokeAgent.bind(api);
      api.revokeAgent = async (id) => {
        await revoke(id);
        throw new ApiError('unavailable', 0);
      };
    });
    await fireEvent.click(await screen.findByRole('button', { name: 'Revoke access' }));
    const dialog = await screen.findByRole('alertdialog');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke access' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(plain(document.body.textContent)).toContain('Revoked on');
  });

  it('cannot be cancelled while the revoke runs', async () => {
    let finish: () => void = () => {};
    await start(CLAUDE, (api) => {
      const revoke = api.revokeAgent.bind(api);
      api.revokeAgent = (id) => new Promise((resolve) => (finish = () => void revoke(id).then(resolve)));
    });
    await fireEvent.click(await screen.findByRole('button', { name: 'Revoke access' }));
    const dialog = await screen.findByRole('alertdialog');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke access' }));
    const cancel = within(dialog).getByRole('button', { name: 'Cancel' });
    expect(cancel.getAttribute('aria-disabled')).toBe('true');
    await fireEvent.click(cancel);
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('alertdialog')).toBeTruthy();
    finish();
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  });

  it('says when the mandate changed since the page showed it', async () => {
    const { api } = await start(VOICE);
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
    // Someone else saves a new version; the page still shows the old one.
    const detail = await api.mandate('mandate-voice');
    const stale = await api.agents();
    api.agents = async () => stale; // the page keeps seeing the old version
    await api.applyTemplate('mandate-voice', { template: 'empty', base_digest: detail.summary.digest });
    await fireEvent.change(select, { target: { value: 'read-only' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('changed in the meantime'));
  });

  it('shows the chosen template in plain words and does not offer hidden base templates', async () => {
    await start(VOICE, (api) => void api.setTemplateHidden('hm-light-climate', true));
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
    const options = [...select.options].map((o) => o.textContent);
    expect(options).toEqual(['Read only', 'Voice assistant (cautious)', 'empty', 'read-only', 'voice-assistant']);
    await fireEvent.change(select, { target: { value: 'hm-voice-cautious' } });
    const words = screen.getByRole('group', { name: 'What the template Voice assistant (cautious) allows' });
    expect(words.textContent).toContain('Opens locks only after you confirm it on your phone');
    expect(within(words).getByText('Lock: unlock, open')).toBeTruthy();
    expect(within(words).getByText('Everything else is forbidden.')).toBeTruthy();
  });

  it('says when nobody could approve for the template (no_approvers)', async () => {
    const { api } = await start(VOICE);
    api.applyTemplate = async () => Promise.reject(new ApiError('no_approvers', 422));
    await screen.findByLabelText('Take over rules from a template');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('Nobody could approve requests of this template'));
  });

  it('changes the mandate to a template as a new version', async () => {
    const { api } = await start(VOICE);
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
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
    // The server names it after the agent.
    expect(create.mock.calls[0]?.[0]).not.toHaveProperty('name');
  });

  it('asks for the separate confirmation when a template allows critical actions (U9)', async () => {
    const { api } = await start(VOICE, (api) => void addCriticalTemplate(api));
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: DOORS } });
    const apply = vi.spyOn(api, 'applyTemplate');
    const button = screen.getByRole('button', { name: 'Apply' });
    await fireEvent.click(button);
    const box = await screen.findByRole('alertdialog', { name: 'Critical actions without approval' });
    expect(apply).toHaveBeenCalledTimes(1);
    expect(apply.mock.calls[0]?.[1]).not.toHaveProperty('confirm_critical');
    // The safe choice has the focus; the rule is named; nothing was stored.
    await waitFor(() => expect(document.activeElement).toBe(within(box).getByRole('button', { name: 'Cancel' })));
    expect(within(box).getByRole('listitem').textContent).toContain('unlock');
    expect((await api.mandate('mandate-voice')).versions).toHaveLength(1);
    await fireEvent.click(within(box).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(document.activeElement).toBe(button);
    // Again, and this time confirmed.
    await fireEvent.click(button);
    const again = await screen.findByRole('alertdialog');
    await fireEvent.click(within(again).getByRole('button', { name: 'Allow without approval' }));
    await waitFor(() => expect(apply).toHaveBeenLastCalledWith('mandate-voice', expect.objectContaining({ template: DOORS, confirm_critical: true })));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect((await api.mandate('mandate-voice')).versions).toHaveLength(2);
  });

  it('cancels the confirmation with Escape and when another template is chosen', async () => {
    await start(VOICE, (api) => void addCriticalTemplate(api));
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: DOORS } });
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    const box = await screen.findByRole('alertdialog');
    await fireEvent.keyDown(box, { key: 'Escape' });
    expect(screen.queryByRole('alertdialog')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await screen.findByRole('alertdialog');
    await fireEvent.change(select, { target: { value: 'read-only' } });
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  });

  it('confirms a critical template for a new mandate too', async () => {
    const { api } = await start(VOICE, (api) => {
      void api.revokeMandate('mandate-voice');
      void addCriticalTemplate(api);
    });
    const select = (await screen.findByLabelText('New mandate from template')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: DOORS } });
    const create = vi.spyOn(api, 'createMandate');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    const box = await screen.findByRole('alertdialog');
    await fireEvent.click(within(box).getByRole('button', { name: 'Allow without approval' }));
    await waitFor(() => expect(create).toHaveBeenLastCalledWith(expect.objectContaining({ template: DOORS, confirm_critical: true })));
  });

  it('says so when the critical rules cannot be loaded', async () => {
    const { api } = await start(VOICE, (api) => void addCriticalTemplate(api));
    api.template = async () => {
      throw new ApiError('unavailable', 0);
    };
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: DOORS } });
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    const box = await screen.findByRole('alertdialog');
    expect(box.textContent).toContain('could not be loaded');
  });

  it('says where the rules came from and how to change the name (#16)', async () => {
    await start(VOICE);
    const mandate = await waitFor(() => section('Mandate'));
    await waitFor(() => expect(plain(mandate.textContent)).toContain('Rules last taken from the template voice-assistant on'));
    expect(within(mandate).getByRole('link', { name: 'Change name' }).getAttribute('href')).toBe('#/mandates/mandate-voice');
    expect(screen.getByText(/The same mandate gets a new version/)).toBeTruthy();
  });

  it('says so when a template brings no change', async () => {
    const { api } = await start(VOICE);
    const select = (await screen.findByLabelText('Take over rules from a template')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'voice-assistant' } });
    const apply = vi.spyOn(api, 'applyTemplate');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(apply).toHaveBeenCalled());
    await waitFor(() => expect(toasts.list().map((t) => t.text)).toContain('No changes — already up to date'));
    expect((await api.mandate('mandate-voice')).versions).toHaveLength(1);
  });

  it('proposes the agent’s name only while the mandate is named after its previous template', async () => {
    const { api } = await start(VOICE, (api) => {
      void api.mandate('mandate-voice').then(({ summary, document }) =>
        api.putMandate('mandate-voice', {
          name: 'voice-assistant',
          draft: { rules: document.rules, approval: document.approval, limits: document.limits, valid_from: document.valid_from },
          base_digest: summary.digest,
        }),
      );
    });
    const group = await screen.findByRole('group', { name: 'Name of the mandate' });
    const take = within(group).getByRole('radio', { name: /Rename to/ }) as HTMLInputElement;
    const keep = within(group).getByRole('radio', { name: /Keep/ }) as HTMLInputElement;
    expect(plain(take.parentElement?.textContent)).toBe('Rename to Sprachassistent');
    expect(plain(keep.parentElement?.textContent)).toBe('Keep voice-assistant');
    expect(take.checked).toBe(true);
    // Keeping the name sends none.
    await fireEvent.click(keep);
    const select = screen.getByLabelText('Take over rules from a template') as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'read-only' } });
    const apply = vi.spyOn(api, 'applyTemplate');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(apply).toHaveBeenCalledTimes(1));
    expect(apply.mock.calls[0]?.[1]).not.toHaveProperty('name');
    // The name is now the one of another template than the rules came from: nothing proposed.
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Name of the mandate' })).toBeNull());
  });

  it('renames to the proposed name when applying', async () => {
    const { api } = await start(VOICE, (api) => {
      void api.mandate('mandate-voice').then(({ summary, document }) =>
        api.putMandate('mandate-voice', {
          name: 'voice-assistant',
          draft: { rules: document.rules, approval: document.approval, limits: document.limits, valid_from: document.valid_from },
          base_digest: summary.digest,
        }),
      );
    });
    await screen.findByRole('group', { name: 'Name of the mandate' });
    const select = screen.getByLabelText('Take over rules from a template') as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'read-only' } });
    const apply = vi.spyOn(api, 'applyTemplate');
    await fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(apply).toHaveBeenCalledWith('mandate-voice', expect.objectContaining({ template: 'read-only', name: 'Sprachassistent' })));
    await waitFor(async () => expect((await api.mandate('mandate-voice')).summary.name).toBe('Sprachassistent'));
  });

  it('looks like any missing page for an unknown agent', async () => {
    await start('pair:nobody');
    expect(await screen.findByText('Page not found')).toBeTruthy();
  });
});
