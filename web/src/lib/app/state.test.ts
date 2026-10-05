// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { ApiError, type ApiClient, type EventHandlers } from '../api/client.ts';
import { createMockClient } from '../api/mock.ts';
import type { EventsState } from '../api/events.ts';
import { approvalsOpenFixture, systemFixture } from '../api/fixtures.ts';
import { AppState } from './state.svelte.ts';

const flush = () => new Promise((r) => setTimeout(r, 0));

/** controllable wraps a client so tests can drive the event stream state. */
function controllable(api: ApiClient) {
  let handlers: EventHandlers | null = null;
  const reconnect = vi.fn();
  const wrapped: ApiClient = {
    ...api,
    events(h) {
      handlers = h;
      h.onState('connecting');
      return { reconnect, close: () => {} };
    },
  };
  return { api: wrapped, state: (s: EventsState) => handlers?.onState(s), reconnect };
}

describe('AppState', () => {
  it('loads session and system and connects the event stream', async () => {
    const app = new AppState(createMockClient(), () => Date.parse('2026-10-02T17:42:03Z'));
    expect(app.phase).toBe('loading');
    await app.start();
    expect(app.phase).toBe('ready');
    expect(app.session?.user.name).toBe('Markus');
    expect(app.system?.ha.connected).toBe(true);
    expect(app.connection).toBe('open');
    // Browser 3 s ahead of the mock server clock.
    expect(app.offsetMs).toBe(3000);
  });

  it('shows no access for a non-admin and an error for anything else', async () => {
    const denied = new AppState(createMockClient({ failures: { session: 'forbidden' } }));
    await denied.start();
    expect(denied.phase).toBe('forbidden');
    const broken = new AppState(createMockClient({ failures: { system: 'unavailable' } }));
    await broken.start();
    expect(broken.phase).toBe('error');
  });

  it('applies system events and passes the others to listeners', async () => {
    const api = createMockClient();
    const app = new AppState(api);
    await app.start();
    const seen: string[] = [];
    const stop = app.on('approval.opened', (e) => seen.push(e.request.id));
    const all: string[] = [];
    app.on('*', (e) => all.push(e.type));
    api.control.openApproval({ ...approvalsOpenFixture[0]!, id: 'apr-9' });
    api.control.setHaConnected(false);
    expect(seen).toEqual(['apr-9']);
    expect(app.system?.ha.connected).toBe(false);
    expect(all).toEqual(['approval.opened', 'system']);
    stop();
    api.control.openApproval({ ...approvalsOpenFixture[0]!, id: 'apr-10' });
    expect(seen).toEqual(['apr-9']);
  });

  it('switches the emergency stop and shows it at once', async () => {
    const app = new AppState(createMockClient());
    await app.start();
    await app.setEmergencyStop(true);
    expect(app.system?.emergency_stop).toMatchObject({ active: true, by_name: 'Markus' });
    await app.setEmergencyStop(false);
    expect(app.system?.emergency_stop.active).toBe(false);
  });

  it('shows the emergency stop even when it fires before the system status is loaded', async () => {
    const api = createMockClient();
    const app = new AppState(api);
    await app.setEmergencyStop(true);
    expect(app.system?.emergency_stop.active).toBe(true);
  });

  it('counts a rejected token as an outage and reloads after the reconnect', async () => {
    let t = 1000;
    const { api, state } = controllable(createMockClient());
    const app = new AppState(api, () => t);
    await app.start();
    state('open');
    const reloaded = vi.fn();
    app.on('reconnected', reloaded);
    t = 2000;
    state('csrf');
    expect(app.downSince).toBe(2000);
    await flush();
    state('open');
    await flush();
    expect(reloaded).toHaveBeenCalledOnce();
  });

  it('shows a stream that never opened as down', async () => {
    let t = 1000;
    const { api, state } = controllable(createMockClient());
    const app = new AppState(api, () => t);
    await app.start();
    t = 3000;
    state('closed');
    expect(app.downSince).toBe(3000);
  });

  it('retries a failed session renewal with growing delays', async () => {
    vi.useFakeTimers();
    try {
      const base = createMockClient();
      const { api, state, reconnect } = controllable(base);
      let fail = 2;
      const real = api.session.bind(api);
      api.session = async () => {
        if (fail-- > 0 && app.phase === 'ready') throw new Error('down');
        return real();
      };
      const app = new AppState(api);
      fail = 0;
      await app.start();
      fail = 2;
      state('csrf');
      await vi.advanceTimersByTimeAsync(0);
      expect(reconnect).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1000);
      expect(reconnect).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(2000);
      expect(reconnect).toHaveBeenCalledOnce();
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not connect when stopped while starting', async () => {
    const api = createMockClient();
    const events = vi.spyOn(api, 'events');
    const app = new AppState(api);
    const started = app.start();
    app.stop();
    await started;
    expect(events).not.toHaveBeenCalled();
  });

  it('records when the connection went down and reloads everything after it is back', async () => {
    let t = 1000;
    const base = createMockClient();
    const { api, state } = controllable(base);
    const app = new AppState(api, () => t);
    await app.start();
    state('open');
    expect(app.downSince).toBeNull();
    const reloaded = vi.fn();
    app.on('reconnected', reloaded);
    t = 5000;
    state('closed');
    expect(app.downSince).toBe(5000);
    state('connecting');
    expect(app.downSince).toBe(5000);
    base.control.setHaConnected(false); // missed while down
    t = 9000;
    state('open');
    await flush();
    expect(app.downSince).toBeNull();
    expect(app.lastUpdated).toBe(9000);
    expect(app.system?.ha.connected).toBe(false);
    expect(reloaded).toHaveBeenCalledOnce();
  });

  it('reloads the session after a rejected token and reconnects', async () => {
    const base = createMockClient();
    const { api, state, reconnect } = controllable(base);
    const session = vi.spyOn(api, 'session');
    const app = new AppState(api);
    await app.start();
    state('csrf');
    await flush();
    expect(session).toHaveBeenCalledTimes(2);
    expect(reconnect).toHaveBeenCalledOnce();
  });

  it('shows no access when the stream says the user lost admin rights', async () => {
    const { api, state } = controllable(createMockClient());
    const app = new AppState(api);
    await app.start();
    state('forbidden');
    expect(app.phase).toBe('forbidden');
  });

  it('offers to sign in when there is no session (direct mode)', async () => {
    const app = new AppState(createMockClient({ failures: { session: 'unauthenticated' } }));
    await app.start();
    expect(app.phase).toBe('signed_out');
  });

  it('offers to sign in when the stream says the session ended', async () => {
    const { api, state } = controllable(createMockClient({ direct: true }));
    const app = new AppState(api);
    await app.start();
    expect(app.session?.sign_out).toBe(true);
    state('signed_out');
    expect(app.phase).toBe('signed_out');
  });

  it('offers to sign in when the session ended while it was renewed', async () => {
    const base = createMockClient({ direct: true });
    const { api, state } = controllable(base);
    const app = new AppState(api);
    await app.start();
    api.session = async () => {
      throw new ApiError('unauthenticated', 401);
    };
    state('csrf');
    await flush();
    expect(app.phase).toBe('signed_out');
  });

  it('signs out and closes the stream', async () => {
    const api = createMockClient({ direct: true });
    const app = new AppState(api);
    await app.start();
    await app.signOut();
    expect(app.phase).toBe('signed_out');
    await expect(api.session()).rejects.toMatchObject({ code: 'unauthenticated' });
  });

  it('keeps the old system status when a reload after reconnect fails', async () => {
    const base = createMockClient();
    const { api, state } = controllable(base);
    let fail = false;
    const real = api.system.bind(api);
    api.system = async () => {
      if (fail) throw new Error('down');
      return real();
    };
    const app = new AppState(api);
    await app.start();
    fail = true;
    state('closed');
    state('open');
    await flush();
    expect(app.system).toMatchObject({ ha: systemFixture.ha });
    expect(app.phase).toBe('ready');
  });

  it('can be stopped', async () => {
    const api = createMockClient();
    const app = new AppState(api);
    await app.start();
    const seen = vi.fn();
    app.on('*', seen);
    app.stop();
    api.control.setHaConnected(false);
    expect(seen).not.toHaveBeenCalled();
  });
});
