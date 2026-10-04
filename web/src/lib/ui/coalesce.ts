// SPDX-License-Identifier: AGPL-3.0-or-later

// Live events often come in bursts (a request opens, the log grows, the agent list changes):
// one reload after the burst is enough.

/** Wait after the last live event before a page reloads its data. */
export const RELOAD_WAIT_MS = 150;

export interface Coalesced {
  /** call schedules fn; further calls within the wait push it back. */
  call(): void;
  cancel(): void;
}

/** coalesce runs fn once when calls have stopped for waitMs. */
export function coalesce(fn: () => void, waitMs: number): Coalesced {
  let timer: ReturnType<typeof setTimeout> | undefined;
  return {
    call() {
      clearTimeout(timer);
      timer = setTimeout(fn, waitMs);
    },
    cancel() {
      clearTimeout(timer);
    },
  };
}
