// SPDX-License-Identifier: AGPL-3.0-or-later

import { flushSync } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DESKTOP, Media } from './media.svelte.ts';

afterEach(() => vi.unstubAllGlobals());

/** fakeMatchMedia stands in for the browser: tests set `matches` and fire the change. */
function fakeMatchMedia(initial: boolean) {
  const listeners = new Set<() => void>();
  const state = { matches: initial, queries: [] as string[] };
  vi.stubGlobal('matchMedia', (query: string) => {
    state.queries.push(query);
    return {
      get matches() {
        return state.matches;
      },
      addEventListener: (_: string, listener: () => void) => listeners.add(listener),
      removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
    };
  });
  return {
    state,
    listeners,
    set(matches: boolean) {
      state.matches = matches;
      listeners.forEach((l) => l());
    },
  };
}

describe('Media', () => {
  it('uses the fallback where there is no matchMedia', () => {
    expect(new Media(DESKTOP).matches).toBe(true);
    expect(new Media(DESKTOP, false).matches).toBe(false);
  });

  it('reads the query and follows its changes while something depends on it', async () => {
    const fake = fakeMatchMedia(false);
    const media = new Media(DESKTOP);
    expect(fake.state.queries).toEqual([DESKTOP]);
    const seen: boolean[] = [];
    const stop = $effect.root(() => {
      $effect(() => {
        seen.push(media.matches);
      });
    });
    flushSync();
    fake.set(true);
    flushSync();
    expect(seen).toEqual([false, true]);
    stop();
    // The subscription ends a tick after its last reader is gone.
    await new Promise((r) => setTimeout(r, 0));
    expect(fake.listeners.size).toBe(0);
  });
});
