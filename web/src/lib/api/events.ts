// SPDX-License-Identifier: AGPL-3.0-or-later

// Live events over the WebSocket api/events (decision D6). The connection only receives;
// its single outgoing message is the CSRF token. The server answers an accepted token
// with a "system" event; only then does the connection count as working. It reconnects
// with growing delays (with jitter), except when the token is missing or rejected: then
// it waits for reconnect() after the session reload instead of hammering the server. The
// UI reloads what it shows after every (re)connect, so no change is lost while down.

import type { ServerEvent } from './types.ts';

/** Close codes of the server (4000–4999 are free for applications, RFC 6455). */
export const CLOSE_FORBIDDEN = 4403;
export const CLOSE_CSRF = 4419;

/**
 * connecting/open/closed: the connection; csrf: no or a rejected CSRF token, reload the
 * session and call reconnect(); forbidden: the user may not connect (no admin any more),
 * no further attempts.
 */
export type EventsState = 'connecting' | 'open' | 'closed' | 'csrf' | 'forbidden';

/** The part of WebSocket used here, so tests can drive a fake. */
export interface EventSocket {
  onopen: (() => void) | null;
  onmessage: ((e: { data: unknown }) => void) | null;
  onclose: ((e: { code: number }) => void) | null;
  send(data: string): void;
  close(): void;
}

export interface EventsOptions {
  url: string;
  csrf: () => string | null;
  onEvent: (event: ServerEvent) => void;
  onState: (state: EventsState) => void;
  socket?: (url: string) => EventSocket;
  /** Source of jitter in [0, 1); tests pass a constant. */
  random?: () => number;
}

export interface EventsConnection {
  /** Connects again at once, e.g. after the session (and CSRF token) was reloaded. */
  reconnect(): void;
  close(): void;
}

const EVENT_TYPES: ReadonlySet<string> = new Set<ServerEvent['type']>([
  'system',
  'approval.opened',
  'approval.closed',
  'audit.appended',
  'agents.changed',
  'mandates.changed',
  'templates.changed',
  'approvers.changed',
  'settings.changed',
]);
const MAX_MESSAGE = 256 * 1024;
const FIRST_DELAY_MS = 1000;
const MAX_DELAY_MS = 30_000;
const JITTER = 0.2;

/** eventsUrl returns the WebSocket URL of api/events relative to the page. */
export function eventsUrl(base: string): string {
  const url = new URL('api/events', base);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.hash = '';
  return url.href;
}

type Json = Record<string, unknown>;

const isObject = (v: unknown): v is Json => v !== null && typeof v === 'object' && !Array.isArray(v);
const isString = (v: unknown): v is string => typeof v === 'string';
const allStrings = (o: Json, keys: string[]) => keys.every((k) => isString(o[k]));

function validAgent(v: unknown): boolean {
  return isObject(v) && allStrings(v, ['client_id', 'display_name']);
}

function validRequest(v: unknown): boolean {
  return (
    isObject(v) &&
    allStrings(v, ['id', 'entity_id', 'device_name', 'action', 'created_at', 'expires_at']) &&
    validAgent(v.agent) &&
    (v.reason === null || isString(v.reason)) &&
    (v.area === null || isString(v.area)) &&
    typeof v.critical === 'boolean' &&
    typeof v.can_answer === 'boolean' &&
    Array.isArray(v.recipients) &&
    v.recipients.every(isString) &&
    Array.isArray(v.params) &&
    v.params.every((p) => isObject(p) && isString(p.name) && isString(p.value))
  );
}

function validHistoryEntry(v: unknown): boolean {
  return (
    isObject(v) &&
    Number.isInteger(v.seq) &&
    allStrings(v, ['entity_id', 'device_name', 'action', 'outcome', 'created_at', 'answered_at']) &&
    validAgent(v.agent) &&
    (v.by_name === null || isString(v.by_name))
  );
}

function validSystem(v: unknown): boolean {
  return (
    isObject(v) &&
    isObject(v.ha) &&
    typeof v.ha.connected === 'boolean' &&
    isObject(v.emergency_stop) &&
    typeof v.emergency_stop.active === 'boolean' &&
    isObject(v.chain) &&
    typeof v.chain.valid === 'boolean' &&
    isString(v.server_time)
  );
}

/** The payload checks per event type; events without payload need none. */
const PAYLOAD: Readonly<Record<ServerEvent['type'], (e: Json) => boolean>> = {
  system: (e) => validSystem(e.system),
  'approval.opened': (e) => validRequest(e.request),
  'approval.closed': (e) => isString(e.id) && validHistoryEntry(e.entry),
  'audit.appended': (e) => Number.isInteger(e.seq),
  'agents.changed': () => true,
  'mandates.changed': (e) => isString(e.id),
  'templates.changed': () => true,
  'approvers.changed': () => true,
  'settings.changed': () => true,
};

/**
 * parse accepts a message only if it is a known event with a payload of the right shape.
 * Text in it (device names, the agent's reason) is still untrusted and rendered escaped.
 */
function parse(data: unknown): ServerEvent | null {
  if (typeof data !== 'string' || data.length > MAX_MESSAGE) return null;
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (!isObject(value) || !isString(value.type) || !EVENT_TYPES.has(value.type)) return null;
  return PAYLOAD[value.type as ServerEvent['type']](value) ? (value as ServerEvent) : null;
}

export function connectEvents(options: EventsOptions): EventsConnection {
  const protocol = new URL(options.url).protocol;
  if (protocol !== 'wss:' && protocol !== 'ws:') throw new Error('events: not a WebSocket URL');
  const create = options.socket ?? ((url: string) => new WebSocket(url) as unknown as EventSocket);
  const random = options.random ?? Math.random;
  let socket: EventSocket | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let delay = FIRST_DELAY_MS;
  let stopped = false;
  /** Token refusals since the server last accepted one; from the second on, reconnect() waits. */
  let refused = 0;

  function schedule(): void {
    if (stopped || timer !== null) return;
    timer = setTimeout(
      () => {
        timer = null;
        open();
      },
      delay * (1 + JITTER * random()),
    );
    delay = Math.min(delay * 2, MAX_DELAY_MS);
  }

  function open(): void {
    const csrf = options.csrf();
    if (csrf === null) {
      options.onState('csrf'); // wait for reconnect() after the session reload
      return;
    }
    options.onState('connecting');
    let s: EventSocket;
    try {
      s = create(options.url);
    } catch {
      options.onState('closed');
      schedule();
      return;
    }
    socket = s;
    // Open only once the server accepted the token (its first event), not when the socket
    // opens: a server that refuses every token would otherwise cause a reload per attempt (S3).
    let accepted = false;
    s.onopen = () => {
      s.send(JSON.stringify({ csrf }));
    };
    s.onmessage = (e) => {
      const event = parse(e.data);
      if (!event) return;
      if (!accepted) {
        accepted = true;
        delay = FIRST_DELAY_MS;
        refused = 0;
        options.onState('open');
      }
      options.onEvent(event);
    };
    s.onclose = (e) => {
      if (socket !== s || stopped) return;
      socket = null;
      if (e.code === CLOSE_FORBIDDEN) {
        stopped = true;
        options.onState('forbidden');
        return;
      }
      if (e.code === CLOSE_CSRF) {
        refused++;
        options.onState('csrf'); // wait for reconnect() after the session reload
        return;
      }
      options.onState('closed');
      schedule();
    };
  }

  open();

  return {
    reconnect() {
      if (stopped) return;
      if (timer !== null) clearTimeout(timer);
      timer = null;
      socket?.close();
      socket = null;
      // After a refused token, a fresh session is worth one immediate try; refused again,
      // the next tries wait with growing delays like any other reconnect.
      if (refused > 1) {
        schedule();
        return;
      }
      delay = FIRST_DELAY_MS;
      open();
    },
    close() {
      stopped = true;
      if (timer !== null) clearTimeout(timer);
      socket?.close();
      socket = null;
    },
  };
}
