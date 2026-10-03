// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { ApiError } from '../api/client.ts';
import { createMockClient, MOCK_EXPIRED_CODE, MOCK_PAIRING_CODE, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import AgentPair from './AgentPair.svelte';

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
const step = () => screen.getByRole('list', { name: /Step \d of 3/ });

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
    expect((screen.getByRole('radio', { name: 'Empty' }) as HTMLInputElement).checked).toBe(true);
    await fireEvent.click(screen.getByRole('radio', { name: 'Read only' }));
    await fireEvent.input(name, { target: { value: ' Tablet Küche ' } });
    const approve = vi.spyOn(api, 'pairingApprove');
    await fireEvent.click(screen.getByRole('button', { name: 'Approve agent' }));

    const done = await screen.findByRole('heading', { name: /Tablet Küche.* is connected/ });
    expect(document.activeElement).toBe(done);
    expect(approve).toHaveBeenCalledWith({
      code: 'BCDFGHJK',
      pairing_id: 'pg-kitchen-tablet',
      display_name: 'Tablet Küche',
      template: 'read-only',
      mandate_name: 'Read only',
    });
    expect(approve).toHaveBeenCalledTimes(1);
    const agent = (await api.agents()).at(-1);
    expect(screen.getByRole('link', { name: 'Go to agent' }).getAttribute('href')).toBe(`#/agents/${encodeURIComponent(agent?.client_id ?? '')}`);
    expect(screen.getByRole('link', { name: 'Adjust mandate' }).getAttribute('href')).toBe(`#/mandates/${agent?.mandate?.id}`);
    expect(screen.queryByRole('list', { name: /Step/ })).toBeNull();
  });

  it('does not send an incomplete code or one outside the alphabet, and says why', async () => {
    const { api } = await start();
    const check = vi.spyOn(api, 'pairingCheck');
    await enter('BCDF-GH');
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('8 characters'));
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
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('expired'));
    await fireEvent.input(codeField(), { target: { value: 'B' } });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('locks the field after too many attempts until the lock time has passed', async () => {
    const { view, app } = await start();
    const now = Date.now();
    for (let i = 0; i < 5; i++) await enter('XXXX-XXXX');
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('locked for 10 minutes'));
    // Read-only, not disabled: the field and its message stay reachable.
    expect(codeField().readOnly).toBe(true);
    expect(codeField().getAttribute('aria-disabled')).toBe('true');
    expect(document.activeElement).toBe(codeField());
    await view.rerender({ app, now: now + 9.5 * 60_000 });
    // The alert does not count down (it would be announced again every minute).
    expect(screen.getByRole('alert').textContent).toContain('locked for 10 minutes');
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
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('expired'));
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
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('locked for 5 minutes'));
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
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('expired'));
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
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('the agent keeps waiting'));
    expect((screen.getByRole('radio', { name: 'Read only' }) as HTMLInputElement).checked).toBe(true);
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
