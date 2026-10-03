// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { setLocale } from '../paraglide/runtime.js';
import Countdown from './Countdown.svelte';

let clock = 0;
const now = () => clock;

beforeEach(() => {
  setLocale('en', { reload: false });
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'requestAnimationFrame', 'cancelAnimationFrame'] });
  clock = 0;
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

/** advance moves the test clock and the fake timers together. */
async function advance(ms: number) {
  clock += ms;
  vi.advanceTimersByTime(ms);
  await Promise.resolve();
}

describe('Countdown', () => {
  const expiresAt = new Date(70_000).toISOString();

  it('counts down on the server clock and announces 60, 30, 10 and the end only', async () => {
    const { container } = render(Countdown, { expiresAt, totalSeconds: 120, offsetMs: 0, now });
    const live = () => container.querySelector('[aria-live="polite"]')?.textContent;
    expect(screen.getByRole('timer').textContent).toContain('1:10');
    expect(live()).toBe('');
    await advance(10_000);
    await vi.waitFor(() => expect(live()).toBe('60 seconds left'));
    await advance(5_000);
    expect(live()).toBe('60 seconds left');
    await advance(25_000);
    await vi.waitFor(() => expect(live()).toBe('30 seconds left'));
    await advance(30_000);
    await vi.waitFor(() => expect(live()).toBe('Time is up'));
    expect(container.querySelector('.countdown')?.classList.contains('expired')).toBe(true);
    expect(screen.getByRole('timer').textContent).toContain('0:00');
  });

  it('is named "expires in" with the time in words, and says nothing when it appears', async () => {
    clock = 30_000; // 40 s left: under a minute from the start
    const { container } = render(Countdown, { expiresAt, totalSeconds: 120, offsetMs: 0, now });
    const live = () => container.querySelector('[aria-live="polite"]')?.textContent;
    expect(screen.getByRole('timer', { name: 'expires in 40 seconds' })).toBeTruthy();
    expect(screen.getByText('expires in')).toBeTruthy();
    await advance(5_000);
    expect(live()).toBe('');
    expect(screen.getByRole('timer', { name: 'expires in 35 seconds' })).toBeTruthy();
    await advance(5_000);
    await vi.waitFor(() => expect(live()).toBe('30 seconds left'));
  });

  it('names the request in its announcements when given a subject (review a11y M8)', async () => {
    clock = 30_000;
    const { container } = render(Countdown, { expiresAt, totalSeconds: 120, offsetMs: 0, now, subject: 'Haustür' });
    const live = () => container.querySelector('[aria-live="polite"]')?.textContent?.replace(/[\u2068\u2069]/g, '');
    await advance(10_000);
    await vi.waitFor(() => expect(live()).toBe('Approval for Haustür: 30 seconds left'));
  });

  it('uses the offset between browser and server clock', () => {
    // Browser 10 s ahead: at browser time 10 s it is 0 s on the server → 70 s left.
    clock = 10_000;
    render(Countdown, { expiresAt, totalSeconds: 120, offsetMs: 10_000, now });
    expect(screen.getByRole('timer').textContent).toContain('1:10');
  });
});
