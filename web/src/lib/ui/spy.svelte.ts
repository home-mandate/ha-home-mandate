// SPDX-License-Identifier: AGPL-3.0-or-later

// Scroll spy for a section index (settings, review 5e): marks the first section, in page
// order, that is in the upper part of the window. Between sections the last mark stays.

/** Only the top 40 % of the window count, so the mark changes when a heading nears the top. */
const BAND = '0px 0px -60% 0px';

export class ScrollSpy {
  /** Key of the marked section; null until the first measurement. */
  current: string | null = $state(null);
  readonly #order: readonly string[];
  #visible: readonly string[] = [];

  constructor(order: readonly string[]) {
    this.#order = order;
  }

  /** observe watches the sections ([key, element] pairs) and returns a function that stops it. */
  observe(sections: readonly (readonly [string, Element])[], Observer: typeof IntersectionObserver | undefined = globalThis.IntersectionObserver): () => void {
    if (!Observer) return () => {};
    const io = new Observer(
      (entries) => {
        for (const entry of entries) {
          const key = sections.find(([, el]) => el === entry.target)?.[0];
          if (key === undefined) continue;
          const others = this.#visible.filter((k) => k !== key);
          this.#visible = entry.isIntersecting ? [...others, key] : others;
        }
        const first = this.#order.find((key) => this.#visible.includes(key));
        if (first !== undefined) this.current = first;
      },
      { rootMargin: BAND },
    );
    for (const [, el] of sections) io.observe(el);
    return () => io.disconnect();
  }
}
