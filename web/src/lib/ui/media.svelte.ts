// SPDX-License-Identifier: AGPL-3.0-or-later

// A media query as reactive state, for layouts that differ in structure and not only in
// style: a table on desktop and cards on mobile, the preview next to the rules or behind a
// tab. Rendering only one of them keeps assistive technology from reading both.

import { createSubscriber } from 'svelte/reactivity';

/** From here up the desktop layout applies (design README section 4, breakpoints). */
export const DESKTOP = '(min-width: 768px)';
/** From here up the editor shows rules and preview side by side. */
export const WIDE = '(min-width: 1024px)';

export class Media {
  readonly #list: MediaQueryList | null;
  readonly #fallback: boolean;
  readonly #subscribe: () => void;

  /** fallback applies where the environment has no matchMedia. */
  constructor(query: string, fallback = true) {
    this.#fallback = fallback;
    this.#list = typeof window.matchMedia === 'function' ? window.matchMedia(query) : null;
    this.#subscribe = createSubscriber((update) => {
      this.#list?.addEventListener('change', update);
      return () => this.#list?.removeEventListener('change', update);
    });
  }

  get matches(): boolean {
    this.#subscribe();
    return this.#list?.matches ?? this.#fallback;
  }
}
