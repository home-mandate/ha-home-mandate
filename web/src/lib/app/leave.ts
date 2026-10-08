// SPDX-License-Identifier: AGPL-3.0-or-later

// The browser's "leave site?" prompt while a mandate or template has unsaved changes
// (issue #20): a reload, closing the tab or leaving the page would otherwise discard the
// edit without a word. The listener exists only while there is something to lose; an idle
// beforeunload listener would still keep the page out of the back/forward cache.

type Target = Pick<Window, 'addEventListener' | 'removeEventListener'>;

function prompt(event: BeforeUnloadEvent): void {
  // Browsers show their own text; the page cannot set one.
  event.preventDefault();
}

export class LeaveGuard {
  readonly #target: Target;
  #active = false;

  constructor(target: Target) {
    this.#target = target;
  }

  get active(): boolean {
    return this.#active;
  }

  /** set registers the prompt while unsaved is true and removes it once it is false. */
  set(unsaved: boolean): void {
    if (unsaved === this.#active) return;
    this.#active = unsaved;
    if (unsaved) this.#target.addEventListener('beforeunload', prompt);
    else this.#target.removeEventListener('beforeunload', prompt);
  }
}
