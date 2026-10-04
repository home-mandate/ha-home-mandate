// SPDX-License-Identifier: AGPL-3.0-or-later

// Loading state of a page: loading, ready or error. A reload keeps showing what is there;
// if it fails the page stays as it was (the event stream brings the next chance). Only the
// answer of the latest run counts, so a slow earlier request cannot overwrite a newer one.

import { ApiError } from '../api/client.ts';
import type { ApiErrorCode } from '../api/types.ts';

export type LoadStatus = 'loading' | 'ready' | 'error';

export class Loader<T> {
  status: LoadStatus = $state('loading');
  data: T | null = $state.raw(null);
  /** Code of the failed first load; null while loading or ready. */
  code: ApiErrorCode | null = $state(null);

  readonly #load: () => Promise<T>;
  #run = 0;

  constructor(load: () => Promise<T>) {
    this.#load = load;
  }

  /** run loads (again); it resolves when this run is done, whatever the outcome. */
  async run(): Promise<void> {
    const run = ++this.#run;
    if (this.data === null) {
      this.status = 'loading';
      this.code = null;
    }
    try {
      const data = await this.#load();
      if (run !== this.#run) return;
      this.data = data;
      this.status = 'ready';
      this.code = null;
    } catch (err) {
      if (run !== this.#run || this.data !== null) return;
      this.status = 'error';
      this.code = err instanceof ApiError ? err.code : 'internal';
    }
  }

  /** set replaces the data, e.g. with the answer of a write. */
  set(data: T): void {
    this.#run++;
    this.data = data;
    this.status = 'ready';
    this.code = null;
  }
}
