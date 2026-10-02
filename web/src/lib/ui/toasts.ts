// SPDX-License-Identifier: AGPL-3.0-or-later

// Toast queue (design: Components "Toast"): success disappears after 4 s, undo after 8 s,
// errors stay until dismissed. All timers pause while the pointer or focus is on the toasts
// (WCAG 2.2.1), so nobody loses an Undo while reaching for it. Never the only feedback for
// a dangerous action.

export const TOAST_SUCCESS_MS = 4000;
export const TOAST_UNDO_MS = 8000;
const MAX_TOASTS = 3;

export type ToastKind = 'success' | 'undo' | 'error';

export interface ToastInput {
  kind: ToastKind;
  text: string;
  action?: { label: string; run: () => void };
}

export interface Toast extends ToastInput {
  id: number;
}

export interface Toasts {
  show(input: ToastInput): number;
  dismiss(id: number): void;
  /** Runs the toast's action once and closes it. */
  act(id: number): void;
  /** Stops all timers; nested (pointer and focus), every pause needs a resume. */
  pause(): void;
  resume(): void;
  list(): readonly Toast[];
  subscribe(listener: (list: readonly Toast[]) => void): () => void;
}

interface Timer {
  handle: ReturnType<typeof setTimeout> | null;
  remaining: number;
  started: number;
}

const lifetime = (kind: ToastKind) => (kind === 'success' ? TOAST_SUCCESS_MS : kind === 'undo' ? TOAST_UNDO_MS : null);

export function createToasts(): Toasts {
  let toasts: readonly Toast[] = [];
  let next = 1;
  let paused = 0;
  const listeners = new Set<(list: readonly Toast[]) => void>();
  const timers = new Map<number, Timer>();

  const set = (list: readonly Toast[]) => {
    toasts = list;
    listeners.forEach((l) => l(toasts));
  };

  function run(id: number, timer: Timer) {
    timer.started = Date.now();
    timer.handle = setTimeout(() => api.dismiss(id), timer.remaining);
  }

  /** evict makes room: the oldest non-error toast first, errors only if nothing else. */
  function evict() {
    while (toasts.length >= MAX_TOASTS) {
      const victim = toasts.find((t) => t.kind !== 'error') ?? toasts[0];
      if (!victim) return;
      api.dismiss(victim.id);
    }
  }

  const api: Toasts = {
    show(input) {
      evict();
      const id = next++;
      set([...toasts, { ...input, id }]);
      const ms = lifetime(input.kind);
      if (ms !== null) {
        const timer: Timer = { handle: null, remaining: ms, started: 0 };
        timers.set(id, timer);
        if (paused === 0) run(id, timer);
      }
      return id;
    },
    dismiss(id) {
      const timer = timers.get(id);
      if (timer?.handle) clearTimeout(timer.handle);
      timers.delete(id);
      if (toasts.some((t) => t.id === id)) set(toasts.filter((t) => t.id !== id));
    },
    act(id) {
      const toast = toasts.find((t) => t.id === id);
      if (!toast) return;
      api.dismiss(id);
      toast.action?.run();
    },
    pause() {
      if (paused++ > 0) return;
      for (const timer of timers.values()) {
        if (timer.handle === null) continue;
        clearTimeout(timer.handle);
        timer.handle = null;
        timer.remaining = Math.max(0, timer.remaining - (Date.now() - timer.started));
      }
    },
    resume() {
      if (paused === 0 || --paused > 0) return;
      for (const [id, timer] of timers) if (timer.handle === null) run(id, timer);
    },
    list: () => toasts,
    subscribe(listener) {
      listeners.add(listener);
      listener(toasts);
      return () => {
        listeners.delete(listener);
      };
    },
  };
  return api;
}

/** The toasts of the app; components show and the ToastHost renders them. */
export const toasts = createToasts();
