// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { setLocale } from '../paraglide/runtime.js';
import Countdown from './Countdown.svelte';
import HoldButton from './HoldButton.svelte';

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

  it('uses the offset between browser and server clock', () => {
    // Browser 10 s ahead: at browser time 10 s it is 0 s on the server → 70 s left.
    clock = 10_000;
    render(Countdown, { expiresAt, totalSeconds: 120, offsetMs: 10_000, now });
    expect(screen.getByRole('timer').textContent).toContain('1:10');
  });
});

describe('HoldButton', () => {
  function setup() {
    const onfire = vi.fn();
    const { container } = render(HoldButton, { label: 'Hold to stop', done: 'Emergency stop on', onfire, now });
    const button = screen.getByRole('button', { name: /Hold to stop/ });
    const live = () => container.querySelector('[role="status"]')?.textContent;
    return { onfire, button, live };
  }

  it('fires after holding Space for 2 s, announcing 50 % and 100 %', async () => {
    const { onfire, button, live } = setup();
    await fireEvent.keyDown(button, { key: ' ' });
    await advance(1000);
    await vi.waitFor(() => expect(live()).toBe('Keep holding …'));
    expect(onfire).not.toHaveBeenCalled();
    await advance(1000);
    await vi.waitFor(() => expect(onfire).toHaveBeenCalledOnce());
    expect(live()).toBe('Emergency stop on');
  });

  it('does not fire when released early, and key repeat does not restart the hold', async () => {
    const { onfire, button } = setup();
    await fireEvent.keyDown(button, { key: 'Enter' });
    await advance(1500);
    await fireEvent.keyDown(button, { key: 'Enter', repeat: true });
    await fireEvent.keyUp(button, { key: 'Enter' });
    await advance(5000);
    expect(onfire).not.toHaveBeenCalled();
  });

  const primary = { button: 0, isPrimary: true, pointerId: 1 };

  it('fires with a held primary pointer and resets when the pointer slides off', async () => {
    const { onfire, button } = setup();
    button.getBoundingClientRect = () => ({ left: 0, right: 100, top: 0, bottom: 50 }) as DOMRect;
    await fireEvent.pointerDown(button, primary);
    await advance(1000);
    await fireEvent.pointerMove(button, { ...primary, clientX: 150, clientY: 10 });
    await advance(3000);
    expect(onfire).not.toHaveBeenCalled();
    await fireEvent.pointerDown(button, primary);
    await fireEvent.pointerMove(button, { ...primary, clientX: 50, clientY: 10 });
    await advance(2100);
    await vi.waitFor(() => expect(onfire).toHaveBeenCalledOnce());
  });

  it('ignores the right button, other fingers, and stops when released early', async () => {
    const { onfire, button } = setup();
    await fireEvent.pointerDown(button, { ...primary, button: 2 });
    await fireEvent.pointerDown(button, { ...primary, isPrimary: false, pointerId: 2 });
    await advance(3000);
    expect(onfire).not.toHaveBeenCalled();
    await fireEvent.pointerDown(button, primary);
    await fireEvent.pointerUp(button, { ...primary, pointerId: 2 }); // another finger
    await advance(1000);
    await fireEvent.pointerUp(button, primary);
    await fireEvent.click(button, { detail: 1 }); // the click after a hold is no activation
    await advance(3000);
    expect(onfire).not.toHaveBeenCalled();
  });

  it('runs on its own after a click from assistive technology, and a second click stops it', async () => {
    const { onfire, button } = setup();
    await fireEvent.click(button, { detail: 0 });
    await advance(1000);
    await fireEvent.click(button, { detail: 0 });
    await advance(3000);
    expect(onfire).not.toHaveBeenCalled();
    await fireEvent.click(button, { detail: 0 });
    await advance(2100);
    await vi.waitFor(() => expect(onfire).toHaveBeenCalledOnce());
  });

  it('stops when the tab is hidden', async () => {
    const { onfire, button } = setup();
    await fireEvent.pointerDown(button, primary);
    await advance(500);
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    document.dispatchEvent(new Event('visibilitychange'));
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    await advance(3000);
    expect(onfire).not.toHaveBeenCalled();
  });

  it('gives every instance its own hint id', () => {
    setup();
    render(HoldButton, { label: 'Second', done: 'x', onfire: () => {}, now });
    const ids = [...document.querySelectorAll('.hint')].map((e) => e.id);
    expect(new Set(ids).size).toBe(2);
  });

  it('ignores other keys and stops on blur', async () => {
    const { onfire, button } = setup();
    await fireEvent.keyDown(button, { key: 'a' });
    await fireEvent.keyDown(button, { key: ' ' });
    await advance(500);
    await fireEvent.blur(button);
    await advance(3000);
    expect(onfire).not.toHaveBeenCalled();
  });
});
