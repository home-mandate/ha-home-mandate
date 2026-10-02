// SPDX-License-Identifier: AGPL-3.0-or-later

// App-wide state of the UI: the session, the system status (header, banners), the live
// event stream and the clock offset to the server. Pages subscribe to events with on();
// after the stream comes back they hear "reconnected" and reload, so nothing is lost while
// the connection was down (decision D6, design README section 7).

import { ApiError, type ApiClient } from '../api/client.ts';
import type { EventsConnection, EventsState } from '../api/events.ts';
import type { ServerEvent, Session, SystemStatus } from '../api/types.ts';
import { clockOffset } from '../ui/countdown.ts';
import { Bus } from './bus.ts';

export type Phase = 'loading' | 'ready' | 'forbidden' | 'error';

type EventType = ServerEvent['type'];
type Listener<E> = (event: E) => void;
type Topic = EventType | '*' | 'reconnected';

const RENEW_FIRST_MS = 1000;
const RENEW_MAX_MS = 30_000;

export class AppState {
  phase: Phase = $state('loading');
  session: Session | null = $state(null);
  system: SystemStatus | null = $state(null);
  connection: EventsState = $state('connecting');
  /** Browser time when the stream was lost; null while connected. */
  downSince: number | null = $state(null);
  /** Browser time of the last full reload after a reconnect. */
  lastUpdated: number | null = $state(null);
  /** Browser clock minus server clock, in ms. */
  offsetMs = $state(0);

  readonly #api: ApiClient;
  readonly #now: () => number;
  readonly #bus = new Bus<Topic>();
  #stream: EventsConnection | null = null;
  #stopped = false;
  #renewDelay = RENEW_FIRST_MS;

  constructor(api: ApiClient, now: () => number = Date.now) {
    this.#api = api;
    this.#now = now;
  }

  get api(): ApiClient {
    return this.#api;
  }

  async start(): Promise<void> {
    try {
      this.session = await this.#api.session();
      this.#setSystem(await this.#api.system());
    } catch (err) {
      this.phase = err instanceof ApiError && err.code === 'forbidden' ? 'forbidden' : 'error';
      return;
    }
    if (this.#stopped) return;
    this.phase = 'ready';
    this.#stream = this.#api.events({
      onEvent: (event) => this.#dispatch(event),
      onState: (state) => this.#onState(state),
    });
  }

  stop(): void {
    this.#stopped = true;
    this.#stream?.close();
    this.#stream = null;
    this.#bus.clear();
  }

  /** on subscribes to an event type, to every event ('*') or to 'reconnected'. */
  on<T extends EventType>(topic: T, listener: Listener<Extract<ServerEvent, { type: T }>>): () => void;
  on(topic: '*', listener: Listener<ServerEvent>): () => void;
  on(topic: 'reconnected', listener: Listener<void>): () => void;
  on(topic: Topic, listener: Listener<never>): () => void {
    return this.#bus.on(topic, listener as (payload: unknown) => void);
  }

  /** Switches the emergency stop; the header and banners show the new state at once. */
  async setEmergencyStop(active: boolean): Promise<void> {
    const stop = await this.#api.setEmergencyStop(active);
    if (this.system) {
      this.system = { ...this.system, emergency_stop: stop };
      return;
    }
    // Fired before the status was loaded: load it now, so the frame cannot look inactive.
    try {
      this.#setSystem(await this.#api.system());
    } catch {
      // The banner follows with the next system event or reload.
    }
  }

  #emit(topic: Topic, event?: unknown): void {
    this.#bus.emit(topic, event);
  }

  #setSystem(system: SystemStatus): void {
    this.system = system;
    this.offsetMs = clockOffset(system.server_time, this.#now());
  }

  #dispatch(event: ServerEvent): void {
    if (event.type === 'system') this.#setSystem(event.system);
    this.#emit(event.type, event);
    this.#emit('*', event);
  }

  #onState(state: EventsState): void {
    this.connection = state;
    if (state === 'forbidden') {
      this.phase = 'forbidden';
      return;
    }
    if (state === 'open') {
      const back = this.downSince !== null;
      this.downSince = null;
      this.#renewDelay = RENEW_FIRST_MS;
      if (back) void this.#reload();
      return;
    }
    // connecting, closed, csrf: an outage until the stream is open again.
    if (state !== 'connecting' && this.downSince === null) this.downSince = this.#now();
    if (state === 'csrf') void this.#renewSession();
  }

  /** Loads a fresh session (and CSRF token), retrying with growing delays, then reconnects. */
  async #renewSession(): Promise<void> {
    try {
      this.session = await this.#api.session();
    } catch (err) {
      if (err instanceof ApiError && err.code === 'forbidden') {
        this.phase = 'forbidden';
        return;
      }
      const delay = this.#renewDelay;
      this.#renewDelay = Math.min(delay * 2, RENEW_MAX_MS);
      setTimeout(() => {
        if (!this.#stopped) void this.#renewSession();
      }, delay);
      return;
    }
    this.#stream?.reconnect();
  }

  async #reload(): Promise<void> {
    try {
      this.#setSystem(await this.#api.system());
    } catch {
      // Keep the last known status; the stream will deliver the next one.
    }
    this.lastUpdated = this.#now();
    this.#emit('reconnected');
  }
}
