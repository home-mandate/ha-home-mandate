// SPDX-License-Identifier: AGPL-3.0-or-later

// A small publish/subscribe registry for AppState. Plain, not reactive: listeners are
// callbacks, not UI state.

export class Bus<Topic extends string> {
  readonly #listeners = new Map<Topic, Set<(payload: unknown) => void>>();

  on(topic: Topic, listener: (payload: unknown) => void): () => void {
    let set = this.#listeners.get(topic);
    if (!set) {
      set = new Set();
      this.#listeners.set(topic, set);
    }
    set.add(listener);
    return () => {
      set.delete(listener);
    };
  }

  emit(topic: Topic, payload?: unknown): void {
    for (const listener of this.#listeners.get(topic) ?? []) listener(payload);
  }

  clear(): void {
    this.#listeners.clear();
  }
}
