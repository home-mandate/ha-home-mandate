// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { ApiError } from '../api/client.ts';
import { createMockClient, MOCK_EXPIRED_CODE, MOCK_PAIRING_CODE, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AgentPair from './AgentPair.svelte';
import { addCriticalTemplate, DOORS } from '../test/critical.ts';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.useRealTimers();
});

async function start(prepare?: (api: MockClient) => void) {
  const api = createMockClient({ now: () => new Date(NOW) });
  prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  const view = render(AgentPair, { app, now: Date.now() });
  return { api, app, view };
}

const codeField = () => screen.getByLabelText('Pairing code') as HTMLInputElement;
const checkButton = () => screen.getByRole('button', { name: 'Check code' });
/** The text of every alert on the page (the choice of template has its own). */
const alerts = () => screen.getAllByRole('alert').map((a) => a.textContent ?? '').join(' | ');
const step = () => screen.getByRole('list', { name: /Step \d of 3/ });

/** The alert at the choice of template. */
const choice = () => {
  const group = screen.getByRole('group', { name: 'New mandate from template' });
  const id = group.getAttribute('aria-describedby');
  return (id ? document.getElementById(id) : group.querySelector('[role="alert"]')) as HTMLElement;
};

async function toMandate() {
  await enter(MOCK_PAIRING_CODE);
  await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
  await screen.findByRole('heading', { name: /What may/ });
}

async function enter(code: string) {
  await fireEvent.input(codeField(), { target: { value: code } });
  await fireEvent.submit(codeField().form as HTMLFormElement);
}

describe('AgentPair', () => {
  it('walks through code, check and mandate to the connected agent', async () => {
    const { api } = await start();
    expect(step().getAttribute('aria-label')).toBe('Step 1 of 3');
    await enter('bcdf ghjk');

    const verify = await screen.findByRole('heading', { name: 'Is this the right agent?' });
    expect(document.activeElement).toBe(verify);
    expect(step().getAttribute('aria-label')).toBe('Step 2 of 3');
    expect(screen.getByText('kitchen-tablet')).toBeTruthy();
    expect(screen.getByText('Küchen-Tablet').getAttribute('title')).toBe('Stated by the agent, unverified');
    expect(screen.getByText(/^Code .BCDF-GHJK. belongs to this request$/)).toBeTruthy();
    expect(screen.getByText(/from .192\.168\.1\.42./)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    const mandate = await screen.findByRole('heading', { name: /What may .*Küchen-Tablet.* do\?/ });
    expect(document.activeElement).toBe(mandate);
    const name = screen.getByLabelText('Display name') as HTMLInputElement;
    expect(name.value).toBe('Küchen-Tablet');
    // The empty template is chosen until the person picks another (decision G1).
    expect((screen.getByRole('radio', { name: 'empty' }) as HTMLInputElement).checked).toBe(true);
    // Each option says in plain words what the template does; base templates with their description.
    const readOnly = screen.getByRole('radio', { name: 'Read only' });
    expect(document.getElementById(readOnly.getAttribute('aria-describedby') ?? '')?.textContent).toMatch(
      /May read the state of every device.*May.*All devices: read.*Everything else is forbidden\./,
    );
    await fireEvent.click(readOnly);
    await fireEvent.input(name, { target: { value: ' Tablet Küche ' } });
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));

    const done = await screen.findByRole('heading', { name: /Tablet Küche.* is connected/ });
    expect(document.activeElement).toBe(done);
    expect(approve).toHaveBeenCalledWith({
      code: 'BCDFGHJK',
      pairing_id: 'pg-kitchen-tablet',
      display_name: 'Tablet Küche',
      template: 'hm-read-only',
      template_digest: (await api.template('hm-read-only')).digest,
      mandate_name: 'Read only',
    });
    expect(approve).toHaveBeenCalledTimes(1);
    const agent = (await api.agents()).at(-1);
    expect(screen.getByRole('link', { name: 'Go to agent' }).getAttribute('href')).toBe(`#/agents/id/${encodeURIComponent(agent?.client_id ?? '')}`);
    expect(screen.getByRole('link', { name: 'Adjust mandate' }).getAttribute('href')).toBe(`#/mandates/${agent?.mandate?.id}`);
    expect(screen.queryByRole('list', { name: /Step/ })).toBeNull();
  });

  it('approves with a critical template only after the separate confirmation (U9)', async () => {
    const { api } = await start((a) => void addCriticalTemplate(a));
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await screen.findByRole('heading', { name: /What may/ });
    await fireEvent.click(screen.getByRole('radio', { name: new RegExp(DOORS, 'i') }));
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    const box = await screen.findByRole('alertdialog', { name: 'Critical actions without approval' });
    expect(approve).toHaveBeenCalledTimes(1);
    expect(approve.mock.calls[0]?.[0]).not.toHaveProperty('confirm_critical');
    expect(box.textContent).toContain('Küchen-Tablet');
    expect(within(box).getByRole('listitem').textContent).toContain('unlock');
    await fireEvent.click(within(box).getByRole('button', { name: 'Allow without approval' }));
    await screen.findByRole('heading', { name: /is connected/ });
    expect(approve).toHaveBeenLastCalledWith(expect.objectContaining({ template: DOORS, confirm_critical: true }));
  });

  it('does not send an incomplete code or one outside the alphabet, and says why', async () => {
    const { api } = await start();
    const check = vi.spyOn(api, 'pairingCheck');
    await enter('BCDF-GH');
    await waitFor(() => expect(alerts()).toContain('8 characters'));
    expect(document.activeElement).toBe(codeField());
    await enter('BCDF-GHJ0');
    expect(check).not.toHaveBeenCalled();
    expect(checkButton().getAttribute('aria-describedby')).toBe(codeField().getAttribute('aria-describedby'));
  });

  it('says when a code is wrong or expired, as an alert tied to the field', async () => {
    await start();
    await enter('XXXX-XXXX');
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('doesn’t match any waiting agent');
    expect(codeField().getAttribute('aria-invalid')).toBe('true');
    expect(codeField().getAttribute('aria-describedby')).toBe(alert.id);
    await enter(MOCK_EXPIRED_CODE);
    await waitFor(() => expect(alerts()).toContain('expired'));
    await fireEvent.input(codeField(), { target: { value: 'B' } });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('locks the field after too many attempts until the lock time has passed', async () => {
    const { view, app } = await start();
    const now = Date.now();
    for (let i = 0; i < 5; i++) await enter('XXXX-XXXX');
    await waitFor(() => expect(alerts()).toContain('locked for 10 minutes'));
    // Read-only, not disabled: the field and its message stay reachable.
    expect(codeField().readOnly).toBe(true);
    expect(codeField().getAttribute('aria-disabled')).toBe('true');
    expect(document.activeElement).toBe(codeField());
    await view.rerender({ app, now: now + 9.5 * 60_000 });
    // The alert does not count down (it would be announced again every minute).
    expect(alerts()).toContain('locked for 10 minutes');
    await view.rerender({ app, now: now + 10 * 60_000 });
    expect(codeField().readOnly).toBe(false);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByText('The lock has ended. You can enter the code now.')).toBeTruthy();
  });

  it('declines the pairing for "This isn’t my agent" and starts over', async () => {
    const { api } = await start();
    await enter(MOCK_PAIRING_CODE);
    await screen.findByRole('heading', { name: 'Is this the right agent?' });
    const deny = vi.spyOn(api, 'pairingDeny');
    await fireEvent.click(screen.getByRole('button', { name: 'This isn’t my agent' }));
    await waitFor(() => expect(document.activeElement).toBe(codeField()));
    expect(deny).toHaveBeenCalledWith({ code: 'BCDFGHJK', pairing_id: 'pg-kitchen-tablet' });
    expect(codeField().value).toBe('');
    await expect(api.pairingCheck(MOCK_PAIRING_CODE)).rejects.toMatchObject({ code: 'pairing_code_expired' });
  });

  it('goes back to the code with the reason when the code runs out before approval', async () => {
    await start((api) => {
      api.pairingApprove = async () => Promise.reject(new ApiError('pairing_code_expired', 410));
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(alerts()).toContain('expired'));
    expect(codeField().value).toBe('BCDFGHJK');
    expect(step().getAttribute('aria-label')).toBe('Step 1 of 3');
  });

  it('shows the lock when approving runs into it, with the field read-only', async () => {
    await start((api) => {
      api.pairingApprove = async () => Promise.reject(new ApiError('pairing_locked', 429, undefined, 300));
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(alerts()).toContain('locked for 5 minutes'));
    expect(codeField().readOnly).toBe(true);
    await waitFor(() => expect(document.activeElement).toBe(codeField()));
  });

  it('starts over when the code now belongs to another request than the one checked', async () => {
    await start((api) => {
      api.pairingApprove = async () => Promise.reject(new ApiError('conflict', 409));
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(alerts()).toContain('expired'));
    expect(step().getAttribute('aria-label')).toBe('Step 1 of 3');
  });

  it('finds the agent when the approval went through but its answer was lost', async () => {
    await start((api) => {
      const approve = api.pairingApprove.bind(api);
      api.pairingApprove = async (req) => {
        await approve(req);
        throw new ApiError('unavailable', 0);
      };
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Approve agent' }));
    expect(await screen.findByRole('heading', { name: /is connected/ })).toBeTruthy();
  });

  it('does not take another admission of the same client for this one (security S11)', async () => {
    await start((api) => {
      const approve = api.pairingApprove.bind(api);
      api.pairingApprove = async (req) => {
        // Someone else admitted the same client under another name; this request is refused.
        await approve({ ...req, display_name: 'Someone else' });
        throw new ApiError('conflict', 409);
      };
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(step().getAttribute('aria-label')).toBe('Step 1 of 3'));
    expect(screen.queryByRole('heading', { name: /is connected/ })).toBeNull();
  });

  it('keeps the display name and template when going back and on again', async () => {
    await start();
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('radio', { name: 'Read only' }));
    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Tablet' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    expect(((await screen.findByLabelText('Display name')) as HTMLInputElement).value).toBe('Tablet');
    expect((screen.getByRole('radio', { name: 'Read only' }) as HTMLInputElement).checked).toBe(true);
  });

  it('refuses a display name that shows nothing', async () => {
    const { api } = await start();
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    const name = await screen.findByLabelText('Display name');
    await fireEvent.input(name, { target: { value: '\u3164\u2800 .' } });
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(name.getAttribute('aria-invalid')).toBe('true'));
    expect(approve).not.toHaveBeenCalled();
  });

  it('warns when the request does not come from the home network', async () => {
    await start((api) => {
      const check = api.pairingCheck.bind(api);
      api.pairingCheck = async (code) => ({ ...(await check(code)), requested_from: '203.0.113.7' });
    });
    await enter(MOCK_PAIRING_CODE);
    expect(await screen.findByText(/not come from your home network/)).toBeTruthy();
  });

  it('keeps the choices when approval fails for another reason, and says so', async () => {
    await start((api) => {
      api.pairingApprove = async () => Promise.reject(new ApiError('unavailable', 0));
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('radio', { name: 'Read only' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(alerts()).toContain('the agent keeps waiting'));
    expect((screen.getByRole('radio', { name: 'Read only' }) as HTMLInputElement).checked).toBe(true);
  });

  it('says when nobody could approve for the template, and keeps the choices', async () => {
    await start((api) => {
      api.pairingApprove = async () => Promise.reject(new ApiError('no_approvers', 422));
    });
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('radio', { name: 'Voice assistant (cautious)' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(alerts()).toContain('Nobody could approve requests of this template'));
    expect((screen.getByRole('radio', { name: 'Voice assistant (cautious)' }) as HTMLInputElement).checked).toBe(true);
  });

  it('does not offer hidden base templates; one hidden meanwhile is no choice any more, said at the choice', async () => {
    const { api } = await start((a) => void a.setTemplateHidden('hm-light-climate', true));
    await toMandate();
    await screen.findByRole('radio', { name: 'Read only' });
    expect(screen.queryByRole('radio', { name: 'Light and climate' })).toBeNull();
    await fireEvent.click(screen.getByRole('radio', { name: 'Read only' }));
    await api.setTemplateHidden('hm-read-only', true);
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(choice().textContent).toContain('no longer exists or is hidden'));
    await waitFor(() => expect(screen.queryByRole('radio', { name: 'Read only' })).toBeNull());
    expect(screen.getAllByRole('radio').some((r) => (r as HTMLInputElement).checked)).toBe(false);
    // Approving again without a choice sends nothing and says so at the choice.
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    expect(approve).toHaveBeenCalledTimes(1);
    expect(document.activeElement?.getAttribute('type')).toBe('radio');
    await fireEvent.click(screen.getByRole('radio', { name: 'empty' }));
    expect(choice().textContent).toBe('');
  });

  it('binds the approval to the template as shown: changed meanwhile, the person chooses again', async () => {
    const { api } = await start();
    await toMandate();
    await fireEvent.click(await screen.findByRole('radio', { name: 'voice-assistant' }));
    const seen = await api.template('voice-assistant');
    await api.putTemplate('voice-assistant', { draft: { ...seen.draft, rules: [] }, base_digest: seen.digest });
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(choice().textContent).toContain('This template was changed in the meantime'));
    expect(approve).toHaveBeenCalledTimes(1);
    expect(approve.mock.calls[0]?.[0]).toMatchObject({ template: 'voice-assistant', template_digest: seen.digest });
    // Still on the mandate step with the code kept, nothing chosen, nobody admitted.
    expect((screen.getByRole('radio', { name: 'voice-assistant' }) as HTMLInputElement).checked).toBe(false);
    expect((await api.agents()).some((a) => a.display_name === 'Küchen-Tablet')).toBe(false);
    // The new form says what the template allows now; choosing it again admits.
    const radio = screen.getByRole('radio', { name: 'voice-assistant' });
    expect(document.getElementById(radio.getAttribute('aria-describedby') ?? '')?.textContent).not.toContain('Lights');
    await fireEvent.click(radio);
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await screen.findByRole('heading', { name: /is connected/ });
    expect(approve).toHaveBeenLastCalledWith(expect.objectContaining({ template_digest: (await api.template('voice-assistant')).digest }));
  });

  it('preselects the most cautious template: one that grants nothing, else the first base template', async () => {
    await start(async (a) => void (await a.deleteTemplate('empty')));
    await toMandate();
    expect(((await screen.findByRole('radio', { name: 'Read only' })) as HTMLInputElement).checked).toBe(true);
  });

  it('asks for a display name before approving', async () => {
    const { api } = await start();
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    const name = await screen.findByLabelText('Display name');
    await fireEvent.input(name, { target: { value: '   ' } });
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));
    await waitFor(() => expect(document.activeElement).toBe(name));
    expect(name.getAttribute('aria-invalid')).toBe('true');
    expect(approve).not.toHaveBeenCalled();
  });

  it('goes back from the mandate to the check', async () => {
    await start();
    await enter(MOCK_PAIRING_CODE);
    await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Back' }));
    const verify = await screen.findByRole('heading', { name: 'Is this the right agent?' });
    expect(document.activeElement).toBe(verify);
    expect(within(step()).getAllByRole('listitem')[0]?.getAttribute('aria-current')).toBeNull();
  });
});
