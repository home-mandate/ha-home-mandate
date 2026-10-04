// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/client.ts';
import { Loader } from './loader.svelte.ts';

/** deferred gives a promise and the means to settle it later. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('Loader', () => {
  it('goes from loading to ready', async () => {
    const loader = new Loader(async () => [1, 2]);
    expect(loader.status).toBe('loading');
    await loader.run();
    expect(loader.status).toBe('ready');
    expect(loader.data).toEqual([1, 2]);
    expect(loader.code).toBeNull();
  });

  it('reports the code of a failed first load and recovers on the next run', async () => {
    let fail = true;
    const loader = new Loader(async () => {
      if (fail) throw new ApiError('unavailable', 0);
      return 'ok';
    });
    await loader.run();
    expect(loader).toMatchObject({ status: 'error', code: 'unavailable', data: null });
    fail = false;
    const run = loader.run();
    expect(loader.status).toBe('loading');
    await run;
    expect(loader).toMatchObject({ status: 'ready', code: null, data: 'ok' });
  });

  it('treats an unknown failure as internal', async () => {
    const loader = new Loader(async () => {
      throw new Error('boom');
    });
    await loader.run();
    expect(loader.code).toBe('internal');
  });

  it('keeps what it shows when a reload fails', async () => {
    let fail = false;
    const loader = new Loader(async () => {
      if (fail) throw new ApiError('unavailable', 0);
      return 'first';
    });
    await loader.run();
    fail = true;
    await loader.run();
    expect(loader).toMatchObject({ status: 'ready', data: 'first' });
  });

  it('ignores the answer of an earlier run that arrives late', async () => {
    const slow = deferred<string>();
    const fast = deferred<string>();
    const answers = [slow.promise, fast.promise];
    const loader = new Loader(() => answers.shift() as Promise<string>);
    const first = loader.run();
    const second = loader.run();
    fast.resolve('new');
    await second;
    slow.resolve('old');
    await first;
    expect(loader.data).toBe('new');
  });

  it('ignores a late failure and a late answer after set', async () => {
    const slow = deferred<string>();
    const loader = new Loader(() => slow.promise);
    const run = loader.run();
    loader.set('written');
    slow.reject(new ApiError('internal', 500));
    await run;
    expect(loader).toMatchObject({ status: 'ready', data: 'written', code: null });
  });
});
