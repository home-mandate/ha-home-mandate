// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createToasts, TOAST_SUCCESS_MS, TOAST_UNDO_MS } from './toasts.ts';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('createToasts', () => {
  it('shows a success toast for 4 s', () => {
    const toasts = createToasts();
    toasts.show({ kind: 'success', text: 'Gespeichert' });
    expect(toasts.list()).toHaveLength(1);
    vi.advanceTimersByTime(TOAST_SUCCESS_MS - 1);
    expect(toasts.list()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(toasts.list()).toEqual([]);
  });

  it('keeps an undo toast for 8 s and errors until dismissed', () => {
    const toasts = createToasts();
    toasts.show({ kind: 'undo', text: 'Regel gelöscht', action: { label: 'Rückgängig', run: () => {} } });
    const error = toasts.show({ kind: 'error', text: 'Fehler' });
    vi.advanceTimersByTime(TOAST_UNDO_MS);
    expect(toasts.list().map((t) => t.kind)).toEqual(['error']);
    vi.advanceTimersByTime(600_000);
    expect(toasts.list()).toHaveLength(1);
    toasts.dismiss(error);
    expect(toasts.list()).toEqual([]);
  });

  it('pauses all timers while the pointer or focus is on the toasts', () => {
    const toasts = createToasts();
    toasts.show({ kind: 'undo', text: 'Regel gelöscht', action: { label: 'Rückgängig', run: () => {} } });
    vi.advanceTimersByTime(6000);
    toasts.pause();
    toasts.pause(); // nested: pointer and focus at once
    vi.advanceTimersByTime(60_000);
    expect(toasts.list()).toHaveLength(1);
    toasts.resume();
    vi.advanceTimersByTime(60_000);
    expect(toasts.list()).toHaveLength(1);
    toasts.resume();
    vi.advanceTimersByTime(1999);
    expect(toasts.list()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(toasts.list()).toEqual([]);
  });

  it('starts a new toast paused while paused', () => {
    const toasts = createToasts();
    toasts.pause();
    toasts.show({ kind: 'success', text: 'x' });
    vi.advanceTimersByTime(10_000);
    expect(toasts.list()).toHaveLength(1);
    toasts.resume();
    vi.advanceTimersByTime(TOAST_SUCCESS_MS);
    expect(toasts.list()).toEqual([]);
  });

  it('ignores a resume without pause', () => {
    const toasts = createToasts();
    toasts.resume();
    toasts.show({ kind: 'success', text: 'x' });
    vi.advanceTimersByTime(TOAST_SUCCESS_MS);
    expect(toasts.list()).toEqual([]);
  });

  it('runs the action once and closes the toast', () => {
    const run = vi.fn();
    const toasts = createToasts();
    const id = toasts.show({ kind: 'undo', text: 'x', action: { label: 'Rückgängig', run } });
    toasts.act(id);
    toasts.act(id);
    expect(run).toHaveBeenCalledOnce();
    expect(toasts.list()).toEqual([]);
  });

  it('shows at most 3 toasts, dropping the oldest non-error first', () => {
    const toasts = createToasts();
    toasts.show({ kind: 'error', text: 'e1' });
    toasts.show({ kind: 'success', text: 's1' });
    toasts.show({ kind: 'error', text: 'e2' });
    toasts.show({ kind: 'error', text: 'e3' });
    expect(toasts.list().map((t) => t.text)).toEqual(['e1', 'e2', 'e3']);
    toasts.show({ kind: 'error', text: 'e4' });
    expect(toasts.list().map((t) => t.text)).toEqual(['e2', 'e3', 'e4']);
  });

  it('tells subscribers about every change', () => {
    const toasts = createToasts();
    const seen: number[] = [];
    const stop = toasts.subscribe((list) => seen.push(list.length));
    const id = toasts.show({ kind: 'error', text: 'x' });
    toasts.dismiss(id);
    stop();
    toasts.show({ kind: 'error', text: 'y' });
    expect(seen).toEqual([0, 1, 0]);
  });
});
