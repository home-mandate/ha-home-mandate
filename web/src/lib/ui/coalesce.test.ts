// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { coalesce } from './coalesce.ts';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('coalesce', () => {
  it('runs once after a burst of calls, when they have stopped for the wait', () => {
    const fn = vi.fn();
    const c = coalesce(fn, 200);
    c.call();
    vi.advanceTimersByTime(150);
    c.call();
    c.call();
    vi.advanceTimersByTime(199);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledOnce();
  });

  it('runs again for calls after the run, and never after cancel', () => {
    const fn = vi.fn();
    const c = coalesce(fn, 200);
    c.call();
    vi.advanceTimersByTime(200);
    c.call();
    c.cancel();
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledOnce();
  });
});
