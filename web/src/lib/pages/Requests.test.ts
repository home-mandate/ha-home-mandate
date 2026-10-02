// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { ApprovalRequest } from '../api/types.ts';
import { NOW, approvalsOpenFixture } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import { toasts } from '../ui/toasts.ts';
import Requests from './Requests.svelte';

// jsdom has no Web Animations; Svelte runs the leaving card's transition with them. This
// stand-in finishes at once (a browser runs the real one, see the Playwright specs). It
// stays for the whole file (each test file has its own environment): transitions of a
// test may still run while the next one starts.
function finishedAnimation(): Animation {
  return {
    cancel() {},
    currentTime: 0,
    set onfinish(done: (() => void) | null) {
      if (done) setTimeout(done, 0);
    },
  } as unknown as Animation;
}

beforeEach(() => {
  setLocale('en', { reload: false });
});
Element.prototype.animate = finishedAnimation;
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const desktop = () => vi.stubGlobal('matchMedia', (q: string) => ({ matches: !q.includes('reduce'), addEventListener: () => {}, removeEventListener: () => {} }));
const mobile = () => vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));
const answerable = (id = 'apr-ui'): ApprovalRequest => ({ ...(approvalsOpenFixture[0] as ApprovalRequest), id, can_answer: true });

async function start(options: MockOptions = {}, prepare?: (api: MockClient) => void) {
  const api = createMockClient({ now: () => new Date(NOW), ...options });
  prepare?.(api);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  render(Requests, { app });
  return { api, app };
}

const pending = () => screen.findByRole('region', { name: /^Pending/ });
const history = () => screen.findByRole('region', { name: 'History' });

describe('Requests', () => {
  beforeEach(desktop);

  it('shows pending approvals and the history side by side', async () => {
    await start();
    expect(await within(await pending()).findAllByRole('article')).toHaveLength(1);
    const items = await within(await history()).findAllByRole('listitem');
    expect(items).toHaveLength(5);
    expect(within(items[0] as HTMLElement).getByText('Declined by emergency stop')).toBeTruthy();
    expect(within(items[3] as HTMLElement).getByText('Approved by Markus · after 0:42')).toBeTruthy();
    expect(within(items[4] as HTMLElement).getByText('Timed out, declined')).toBeTruthy();
    expect(within(items[3] as HTMLElement).getByRole('link', { name: 'Entry no. 8' }).getAttribute('href')).toBe('#/audit/8');
  });

  it('offers no buttons for requests this person cannot answer here, and says to use the phone', async () => {
    await start();
    const section = await pending();
    await within(section).findAllByRole('article');
    expect(within(section).queryByRole('button', { name: 'Approve' })).toBeNull();
    expect(within(section).queryByRole('button', { name: 'Decline' })).toBeNull();
    expect(within(section).getByText('Approve or decline from the notification on your phone.')).toBeTruthy();
  });

  it('declines at once, and the history and a live region say so (F2)', async () => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const section = await pending();
    const spy = vi.spyOn(api, 'answerApproval');
    expect(await within(section).findByText('Approvals here count like those from a phone and are logged with your name.')).toBeTruthy();
    await fireEvent.click(await within(section).findByRole('button', { name: 'Decline' }));
    expect(spy).toHaveBeenCalledWith('apr-ui', false);
    await waitFor(() => expect(within(section).getAllByRole('article')).toHaveLength(1));
    expect(await screen.findByText(/^Approval for Haustür ended: Declined by Markus · in Home-Mandate/)).toBeTruthy();
    expect(within(await history()).getAllByRole('listitem')).toHaveLength(6);
  });

  it('approves only after a confirmation that says what follows, with the focus on it', async () => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const section = await pending();
    const spy = vi.spyOn(api, 'answerApproval');
    const approve = await within(section).findByRole('button', { name: 'Approve' });
    await fireEvent.click(approve);
    expect(spy).not.toHaveBeenCalled();
    const group = within(section).getByRole('group', { name: 'Confirm approval' });
    expect(within(group).getByText('Claude Code will then unlock Haustür.')).toBeTruthy();
    await tick();
    expect(document.activeElement).toBe(within(group).getByRole('button', { name: 'Yes, approve' }));
    await fireEvent.click(within(group).getByRole('button', { name: 'Cancel' }));
    expect(spy).not.toHaveBeenCalled();
    await tick();
    expect(document.activeElement).toBe(within(section).getByRole('button', { name: 'Approve' }));
    await fireEvent.click(within(section).getByRole('button', { name: 'Approve' }));
    await fireEvent.click(within(section).getByRole('button', { name: 'Yes, approve' }));
    expect(spy).toHaveBeenCalledWith('apr-ui', true);
    expect(await screen.findByText(/^Approval for Haustür ended: Approved by Markus · in Home-Mandate/)).toBeTruthy();
  });

  it('says so when the request was answered elsewhere meanwhile', async () => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const show = vi.spyOn(toasts, 'show');
    const section = await pending();
    const decline = await within(section).findByRole('button', { name: 'Decline' });
    api.answerApproval = async () => {
      throw Object.assign(new Error('gone'), { code: 'not_found', status: 404 });
    };
    await fireEvent.click(decline);
    await waitFor(() => expect(show).toHaveBeenCalledWith({ kind: 'error', text: 'This approval was already answered or has ended.' }));
  });

  it('follows requests answered on a phone, and announces the result', async () => {
    const { api } = await start();
    const section = await pending();
    await within(section).findAllByRole('article');
    api.control.closeApproval('apr-1', 'approved', 'Alex', 'push');
    expect(await within(section).findByText('No pending approvals')).toBeTruthy();
    expect(await screen.findByText(/^Approval for Haustür ended: Approved by Alex · on the phone/)).toBeTruthy();
  });

  it('says that approvals cannot be delivered while Home Assistant is down', async () => {
    const { api } = await start();
    await pending();
    api.control.setHaConnected(false);
    expect(await screen.findByText(/approvals can’t be delivered/)).toBeTruthy();
  });

  it('shows empty states for both lists', async () => {
    await start({}, (client) => {
      client.approvals = async () => ({ open: [], history: [] });
    });
    expect(await screen.findByText('No pending approvals')).toBeTruthy();
    expect(await screen.findByText('No answered approvals yet')).toBeTruthy();
  });
});

describe('Requests on mobile', () => {
  beforeEach(mobile);

  it('switches between pending and history', async () => {
    await start();
    const tabs = await screen.findByRole('tablist');
    const [open, done] = within(tabs).getAllByRole('tab');
    expect(open?.getAttribute('aria-selected')).toBe('true');
    expect(screen.queryByRole('region', { name: 'History' })).toBeNull();
    await fireEvent.click(done as HTMLElement);
    expect(done?.getAttribute('aria-selected')).toBe('true');
    expect(await history()).toBeTruthy();
    expect(screen.queryByRole('region', { name: /^Pending/ })).toBeNull();
  });
});
