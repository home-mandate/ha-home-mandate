// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { LeaveGuard } from './leave.ts';

const guards: LeaveGuard[] = [];
afterEach(() => guards.splice(0).forEach((g) => g.set(false)));

function guard(target: Pick<Window, 'addEventListener' | 'removeEventListener'> = window) {
  const g = new LeaveGuard(target);
  guards.push(g);
  return g;
}

/** leave fires beforeunload as a reload or closing the tab would; true if the browser would ask. */
function leave(): boolean {
  const event = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}

describe('LeaveGuard', () => {
  it('asks before leaving only while there are unsaved changes', () => {
    const g = guard();
    expect(leave()).toBe(false);
    g.set(true);
    expect(leave()).toBe(true);
    g.set(false);
    expect(leave()).toBe(false);
  });

  it('registers the listener once while active and removes it again', () => {
    const target = { addEventListener: vi.fn(), removeEventListener: vi.fn() };
    const g = guard(target);
    g.set(false);
    expect(target.addEventListener).not.toHaveBeenCalled();
    g.set(true);
    g.set(true);
    expect(target.addEventListener).toHaveBeenCalledTimes(1);
    expect(target.addEventListener).toHaveBeenCalledWith('beforeunload', expect.any(Function));
    g.set(false);
    g.set(false);
    expect(target.removeEventListener).toHaveBeenCalledTimes(1);
    expect(target.removeEventListener.mock.calls[0]?.[1]).toBe(target.addEventListener.mock.calls[0]?.[1]);
  });

  it('reports whether it is active', () => {
    const g = guard();
    expect(g.active).toBe(false);
    g.set(true);
    expect(g.active).toBe(true);
  });
});
