// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { ApiError } from '../api/client.ts';
import { toasts } from '../ui/toasts.ts';
import type { ApprovalPage, ApprovalRequest } from '../api/types.ts';
import { NOW, approvalsOpenFixture } from '../api/fixtures.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import RequestPage from './RequestPage.svelte';

const ID = '0123456789abcdef0123456789abcdef';

beforeEach(() => {
  setLocale('en', { reload: false });
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));
});
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const request = (patch: Partial<ApprovalRequest> = {}): ApprovalRequest => ({ ...(approvalsOpenFixture[0] as ApprovalRequest), id: ID, ...patch });

async function start(prepare?: (api: MockClient) => void) {
  const api = createMockClient({ now: () => new Date(NOW) });
  prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(RequestPage, { app, id: ID });
  return { api, app };
}

/** plain is text without the isolates around names, for comparing. */
const plain = (text: string | null | undefined) => (text ?? '').replace(/[⁨⁩]/g, '').replace(/\s+/g, ' ').trim();

describe('RequestPage', () => {
  it('shows the request and answers it with large buttons, then only the outcome', async () => {
    const { api } = await start((client) => client.control.openApproval(request({ can_answer: true })));
    expect(await screen.findByRole('article')).toBeTruthy();
    const spy = vi.spyOn(api, 'answerApproval');
    await fireEvent.click(screen.getByRole('button', { name: 'Decline' }));
    expect(spy).toHaveBeenCalledWith(ID, false);
    expect(await screen.findByText('Declined')).toBeTruthy();
    expect(screen.queryByRole('article')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull();
    // Nobody's name on the page; the live region says the outcome.
    expect(document.body.textContent).not.toContain('Markus');
    await waitFor(() => expect(plain(document.querySelector('[aria-live="polite"]')?.textContent)).toBe('Declined'));
  });

  it('asks before approving, as on the list', async () => {
    const { api } = await start((client) => client.control.openApproval(request({ can_answer: true })));
    const spy = vi.spyOn(api, 'answerApproval');
    await fireEvent.click(await screen.findByRole('button', { name: 'Approve' }));
    expect(screen.getByRole('group', { name: 'Confirm approval' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Yes, approve' })).toBeTruthy();
    expect(spy).not.toHaveBeenCalled();
  });

  it('offers no buttons to an approver who answers on the phone', async () => {
    await start((client) => client.control.openApproval(request({ can_answer: false })));
    expect(await screen.findByRole('article')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Decline' })).toBeNull();
    expect(screen.getByText('Approve or decline from the notification on your phone.')).toBeTruthy();
  });

  it('follows the request when it ends elsewhere, e.g. on a phone', async () => {
    const { api } = await start((client) => client.control.openApproval(request({ can_answer: true })));
    await screen.findByRole('article');
    api.control.closeApproval(ID, 'approved', 'Markus', 'push');
    expect(await screen.findByText('Approved')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Decline' })).toBeNull();
  });

  it('says the same for anyone else and for an unknown request', async () => {
    await start();
    expect(await screen.findByText(/You can’t answer this request/)).toBeTruthy();
    expect(screen.queryByRole('article')).toBeNull();
  });

  it('shows why a request ended before an answer', async () => {
    await start((client) => vi.spyOn(client, 'approval').mockResolvedValue({ status: 'closed', outcome: 'cancelled', cause: 'withdrawn' } satisfies ApprovalPage));
    expect(await screen.findByText('Withdrawn by the agent')).toBeTruthy();
    expect(screen.getByText('This approval request has ended.')).toBeTruthy();
  });

  it('links back to all approvals', async () => {
    await start();
    expect(screen.getByRole('link', { name: /All approvals/ }).getAttribute('href')).toBe('#/audit/requests');
  });

  it('moves the focus to the heading when the answered button is gone', async () => {
    await start((client) => client.control.openApproval(request({ can_answer: true })));
    const decline = await screen.findByRole('button', { name: 'Decline' });
    decline.focus();
    await fireEvent.click(decline);
    await screen.findByRole('heading', { name: 'Declined' });
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { level: 1 })));
  });

  it('says when an answer failed and offers the buttons again', async () => {
    const { api } = await start((client) => client.control.openApproval(request({ can_answer: true })));
    vi.spyOn(api, 'answerApproval').mockRejectedValue(new ApiError('rate_limited', 429));
    const show = vi.spyOn(toasts, 'show');
    await fireEvent.click(await screen.findByRole('button', { name: 'Decline' }));
    await waitFor(() => expect(show).toHaveBeenCalledWith(expect.objectContaining({ kind: 'error' })));
    await waitFor(() => expect((screen.getByRole('button', { name: 'Decline' }) as HTMLButtonElement).getAttribute('aria-busy')).not.toBe('true'));
  });

  it('loads once more when the load after an answer fails', async () => {
    const { api } = await start((client) => client.control.openApproval(request({ can_answer: true })));
    await screen.findByRole('article');
    const load = vi.spyOn(api, 'approval');
    load.mockRejectedValueOnce(new ApiError('unavailable', 0));
    await fireEvent.click(screen.getByRole('button', { name: 'Decline' }));
    expect(await screen.findByRole('heading', { name: 'Declined' })).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it('says when the request cannot be loaded', async () => {
    await start((client) => vi.spyOn(client, 'approval').mockRejectedValue(new ApiError('unavailable', 0)));
    expect(await screen.findByText('Couldn’t load the approval request')).toBeTruthy();
  });
});
