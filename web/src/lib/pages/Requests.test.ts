// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { ApiError } from '../api/client.ts';
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

/** plain is text without the isolates around names, for comparing. */
const plain = (text: string | null | undefined) => (text ?? '').replace(/[\u2068\u2069]/g, '').replace(/\s+/g, ' ').trim();
/** live waits until the polite live region says text (start). */
const live = (start: string) =>
  waitFor(() => {
    const region = document.querySelector('[aria-live="polite"]');
    expect(plain(region?.textContent).startsWith(start)).toBe(true);
  });
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
    expect(plain(items[3]?.textContent)).toContain('Approved by Markus · after 0:42');
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
    const decline = await within(section).findByRole('button', { name: 'Decline' });
    decline.focus();
    await fireEvent.click(decline);
    expect(spy).toHaveBeenCalledWith('apr-ui', false);
    await waitFor(() => expect(within(section).getAllByRole('article')).toHaveLength(1));
    await live('Approval for Haustür ended: Declined by Markus · in Home-Mandate');
    expect(within(await history()).getAllByRole('listitem')).toHaveLength(6);
    // The focused card has left: the focus is on the section heading, not lost.
    await waitFor(() => expect(document.activeElement?.tagName).toBe('H2'));
    expect(plain(document.activeElement?.textContent)).toMatch(/^Pending/);
  });

  it('approves only after a confirmation that says what follows, with the focus on it', async () => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const section = await pending();
    const spy = vi.spyOn(api, 'answerApproval');
    const approve = await within(section).findByRole('button', { name: 'Approve' });
    await fireEvent.click(approve);
    expect(spy).not.toHaveBeenCalled();
    const group = within(section).getByRole('group', { name: 'Confirm approval' });
    expect(plain(group.querySelector('p')?.textContent)).toBe('Claude Code, unverifiedclaude.ai will then unlock Haustür.');
    await tick();
    expect(document.activeElement).toBe(within(group).getByRole('button', { name: 'Yes, approve' }));
    await fireEvent.click(within(group).getByRole('button', { name: 'Cancel' }));
    expect(spy).not.toHaveBeenCalled();
    await tick();
    expect(document.activeElement).toBe(within(section).getByRole('button', { name: 'Approve' }));
    await fireEvent.click(within(section).getByRole('button', { name: 'Approve' }));
    // The confirmation counts only after a pause (held key, double click).
    await new Promise((r) => setTimeout(r, 650));
    await fireEvent.click(within(section).getByRole('button', { name: 'Yes, approve' }));
    expect(spy).toHaveBeenCalledWith('apr-ui', true);
    await live('Approval for Haustür ended: Approved by Markus · in Home-Mandate');
  });

  it('says so when the request was answered elsewhere meanwhile', async () => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const show = vi.spyOn(toasts, 'show');
    const section = await pending();
    const decline = await within(section).findByRole('button', { name: 'Decline' });
    api.answerApproval = async () => {
      throw new ApiError('not_found', 404);
    };
    await fireEvent.click(decline);
    await waitFor(() => expect(show).toHaveBeenCalledWith({ kind: 'error', text: 'This approval was already answered or has ended.' }));
  });

  it('says that sending failed when the server answered with an error', async () => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const show = vi.spyOn(toasts, 'show');
    const decline = await within(await pending()).findByRole('button', { name: 'Decline' });
    api.answerApproval = async () => {
      throw new ApiError('internal', 500);
    };
    await fireEvent.click(decline);
    await waitFor(() => expect(show).toHaveBeenCalledWith({ kind: 'error', text: 'The answer couldn’t be sent. Answering on the phone still works.' }));
  });

  // Without an answer from the server the answer may still have arrived (review Sec L4).
  it.each([
    ['the server was not reached', () => new ApiError('unavailable', 0)],
    ['the error is no API error', () => null],
  ])('says that it is unclear whether the answer arrived when %s', async (_, error) => {
    const { api } = await start({}, (client) => client.control.openApproval(answerable()));
    const show = vi.spyOn(toasts, 'show');
    const decline = await within(await pending()).findByRole('button', { name: 'Decline' });
    api.answerApproval = async () => {
      throw error();
    };
    await fireEvent.click(decline);
    await waitFor(() => expect(show).toHaveBeenCalledWith({ kind: 'error', text: 'It’s unclear whether the answer arrived. The history shows how it ended.' }));
  });

  it('follows requests answered on a phone, and announces the result', async () => {
    const { api } = await start();
    const section = await pending();
    await within(section).findAllByRole('article');
    api.control.closeApproval('apr-1', 'approved', 'Alex', 'push');
    expect(await within(section).findByText('No pending approvals')).toBeTruthy();
    await live('Approval for Haustür ended: Approved by Alex · on the phone');
    // The same ending twice in a row is said again.
    const region = document.querySelector('[aria-live="polite"]') as HTMLElement;
    api.control.openApproval({ ...(approvalsOpenFixture[0] as ApprovalRequest), id: 'apr-2' });
    await waitFor(() => expect(within(section).getAllByRole('article')).toHaveLength(1));
    const seen: string[] = [];
    new MutationObserver(() => seen.push(plain(region.textContent))).observe(region, { childList: true, subtree: true, characterData: true });
    api.control.closeApproval('apr-2', 'approved', 'Alex', 'push');
    await waitFor(() => expect(seen).toContain(''));
    await live('Approval for Haustür ended: Approved by Alex · on the phone');
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

  it('switches between pending and history: a complete tab pattern', async () => {
    await start();
    const tabs = await screen.findByRole('tablist', { name: 'Approvals' });
    const [open, done] = within(tabs).getAllByRole('tab') as [HTMLElement, HTMLElement];
    expect(open.getAttribute('aria-selected')).toBe('true');
    expect([open.tabIndex, done.tabIndex]).toEqual([0, -1]);
    expect(screen.getByRole('tabpanel', { name: 'Pending' })).toBeTruthy();
    expect(screen.queryByRole('tabpanel', { name: 'History' })).toBeNull();
    await fireEvent.click(done);
    expect(done.getAttribute('aria-selected')).toBe('true');
    expect(within(screen.getByRole('tabpanel', { name: 'History' })).getAllByRole('listitem')).toHaveLength(5);
    expect(screen.queryByRole('tabpanel', { name: 'Pending' })).toBeNull();
    // Arrow keys move the selection and the focus; Home and End jump.
    done.focus();
    await fireEvent.keyDown(done, { key: 'ArrowRight' });
    await waitFor(() => expect(document.activeElement).toBe(open));
    expect(open.getAttribute('aria-selected')).toBe('true');
    await fireEvent.keyDown(open, { key: 'End' });
    await waitFor(() => expect(document.activeElement).toBe(done));
    await fireEvent.keyDown(done, { key: 'Home' });
    await waitFor(() => expect(document.activeElement).toBe(open));
  });
});
