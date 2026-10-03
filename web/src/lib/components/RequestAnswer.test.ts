// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { ApprovalRequest } from '../api/types.ts';
import { approvalsOpenFixture } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import RequestAnswer from './RequestAnswer.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(cleanup);

const base = { ...(approvalsOpenFixture[0] as ApprovalRequest), can_answer: true };

function show(request: Partial<ApprovalRequest> = {}) {
  let clock = 1000;
  const onanswer = vi.fn();
  render(RequestAnswer, { request: { ...base, ...request }, busy: false, describedBy: 'card-title', onanswer, now: () => clock });
  return { onanswer, advance: (ms: number) => (clock += ms) };
}

describe('RequestAnswer', () => {
  it('names which request the buttons answer', () => {
    show();
    expect(screen.getByRole('button', { name: 'Decline' }).getAttribute('aria-describedby')).toBe('card-title');
    expect(screen.getByRole('button', { name: 'Approve' }).getAttribute('aria-describedby')).toBe('card-title');
  });

  it('says what follows with the agent marked as its claim, its checked client, and the device isolated', async () => {
    show();
    await fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const group = screen.getByRole('group', { name: 'Confirm approval' });
    const sentence = group.querySelector('p') as HTMLElement;
    expect(sentence.textContent?.replace(/\s+/g, ' ').trim()).toBe('Claude Code, unverifiedclaude.ai will then unlock Haustür.');
    expect(within(sentence).getByText('Claude Code').getAttribute('title')).toBe('Stated by the agent, unverified');
    expect(within(sentence).getByText('Haustür').tagName).toBe('BDI');
  });

  it('repeats the values and the critical marker in the confirmation (security S1, S9)', async () => {
    show({ critical: true, params: [{ name: 'brightness_pct', value: '100' }] });
    await fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const group = screen.getByRole('group', { name: 'Confirm approval' });
    expect(within(group).getByText('Critical')).toBeTruthy();
    expect(within(group).getByText(/With these values:/).textContent?.replace(/\s+/g, ' ')).toBe('With these values: brightness_pct=100');
  });

  it('keeps hostile names as text: no mark, no hidden characters, no markup', async () => {
    show({ agent: { client_id: 'pair:voice-assistant', display_name: 'Anna\u0001 (verified)\u202E<img src=x onerror=alert(1)>' }, device_name: 'Tür\u0001\u2067x' });
    await fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const sentence = screen.getByRole('group', { name: 'Confirm approval' }).querySelector('p') as HTMLElement;
    for (const hidden of ['\u0001', '\u202E', '\u2067']) expect(sentence.textContent).not.toContain(hidden);
    expect(sentence.querySelector('img')).toBeNull();
    expect(sentence.textContent).toContain('will then unlock');
    expect(sentence.querySelector('bdi[title]')?.getAttribute('title')).toBe('Stated by the agent, unverified');
  });

  it('declines at once, approves only after the confirmation is armed', async () => {
    const { onanswer, advance } = show();
    await fireEvent.click(screen.getByRole('button', { name: 'Decline' }));
    expect(onanswer).toHaveBeenCalledWith(false);
    await fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    await tick();
    const yes = screen.getByRole('button', { name: 'Yes, approve' });
    expect(document.activeElement).toBe(yes);
    // A held Enter or the second click of a double click: too early, ignored.
    await fireEvent.click(yes);
    expect(onanswer).toHaveBeenCalledTimes(1);
    advance(700);
    await fireEvent.click(yes);
    expect(onanswer).toHaveBeenLastCalledWith(true);
  });
});
