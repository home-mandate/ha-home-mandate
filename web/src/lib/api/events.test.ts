// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CLOSE_CSRF, CLOSE_FORBIDDEN, CLOSE_SIGNED_OUT, connectEvents, eventsUrl, type EventsState } from './events.ts';
import type { ServerEvent } from './types.ts';

/** FakeSocket stands in for WebSocket; tests drive its lifecycle. */
class FakeSocket {
  static all: FakeSocket[] = [];
  readonly sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  readonly url: string;
  constructor(url: string) {
    this.url = url;
    FakeSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
  }
  open() {
    this.onopen?.();
  }
  message(data: unknown) {
    this.onmessage?.({ data });
  }
  drop(code = 1006) {
    this.onclose?.({ code });
  }
}

const latest = () => FakeSocket.all.at(-1) as FakeSocket;

function connect(csrf: string | null = 'token') {
  const events: ServerEvent[] = [];
  const states: EventsState[] = [];
  const conn = connectEvents({
    url: 'wss://ha.example/api/hassio_ingress/f00d/api/events',
    csrf: () => csrf,
    onEvent: (e) => events.push(e),
    onState: (s) => states.push(s),
    socket: (url) => new FakeSocket(url),
    random: () => 0,
  });
  return { conn, events, states };
}

beforeEach(() => {
  FakeSocket.all = [];
  vi.useFakeTimers();
});
afterEach(() => vi.useRealTimers());

describe('eventsUrl', () => {
  it('turns the page base into a WebSocket URL with the Ingress prefix', () => {
    expect(eventsUrl('https://ha.example/api/hassio_ingress/f00d/index.html#/')).toBe(
      'wss://ha.example/api/hassio_ingress/f00d/api/events',
    );
    expect(eventsUrl('http://localhost:8099/')).toBe('ws://localhost:8099/api/events');
  });
});

describe('connectEvents', () => {
  it('sends the CSRF token as the first and only message, and reports open once the server accepted it', () => {
    const { states } = connect();
    latest().open();
    expect(latest().sent).toEqual(['{"csrf":"token"}']);
    // An open socket is not yet an accepted token (security review S3).
    expect(states).toEqual(['connecting']);
    latest().message('{"type":"agents.changed"}');
    latest().message('{"type":"agents.changed"}');
    expect(states).toEqual(['connecting', 'open']);
  });

  it('passes known events on and ignores everything else', () => {
    const { events } = connect();
    latest().open();
    latest().message('{"type":"agents.changed"}');
    latest().message('{"type":"mandates.changed","id":"m1"}');
    latest().message('{"type":"devices.changed"}');
    latest().message('not json');
    latest().message('{"type":"unknown.thing"}');
    latest().message('{"no":"type"}');
    latest().message('[1,2]');
    latest().message(new ArrayBuffer(4));
    latest().message('x'.repeat(300_000));
    expect(events).toEqual([{ type: 'agents.changed' }, { type: 'mandates.changed', id: 'm1' }, { type: 'devices.changed' }]);
  });

  it('drops events whose payload has the wrong shape', () => {
    const { events } = connect();
    latest().open();
    latest().message('{"type":"mandates.changed","id":42}');
    latest().message('{"type":"audit.appended","seq":"7"}');
    latest().message('{"type":"approval.opened","request":"x"}');
    latest().message('{"type":"approval.opened","request":{"id":"a","device_name":{"x":1}}}');
    latest().message('{"type":"approval.closed","id":"a"}');
    // Service data must be name/value strings (S1), and can_answer a boolean.
    const request = { id: 'a', entity_id: 'light.k', device_name: 'K', action: 'turn_on', created_at: 'x', expires_at: 'y', agent: { client_id: 'c', display_name: 'C' }, reason: null, area: null, critical: false, recipients: [], can_answer: false };
    latest().message(JSON.stringify({ type: 'approval.opened', request: { ...request, params: [{ name: 'b', value: 1 }] } }));
    latest().message(JSON.stringify({ type: 'approval.opened', request: { ...request, params: null } }));
    latest().message(JSON.stringify({ type: 'approval.opened', request: { ...request, params: [], can_answer: 'yes' } }));
    latest().message('{"type":"system","system":null}');
    latest().message('{"type":"audit.appended","seq":7}');
    expect(events).toEqual([{ type: 'audit.appended', seq: 7 }]);
  });

  it('reconnects with growing delays, capped at 30 s, and resets after the first event', () => {
    const { states } = connect();
    latest().drop();
    expect(states.at(-1)).toBe('closed');
    const delays: number[] = [];
    for (let i = 0; i < 7; i++) {
      const before = FakeSocket.all.length;
      let waited = 0;
      while (FakeSocket.all.length === before) {
        vi.advanceTimersByTime(500);
        waited += 500;
      }
      delays.push(waited);
      latest().drop();
    }
    expect(delays).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000]);
    vi.advanceTimersByTime(30_000);
    latest().open();
    latest().drop();
    // Opening alone is not success: the server may still reject the token.
    let before = FakeSocket.all.length;
    vi.advanceTimersByTime(29_999);
    expect(FakeSocket.all.length).toBe(before);
    vi.advanceTimersByTime(1);
    latest().open();
    latest().message('{"type":"agents.changed"}');
    latest().drop();
    before = FakeSocket.all.length;
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.all.length).toBe(before + 1);
  });

  it('stops for good when the server says the user may not connect', () => {
    const { states } = connect();
    latest().open();
    latest().drop(CLOSE_FORBIDDEN);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(1);
    expect(states.at(-1)).toBe('forbidden');
  });

  it('stops for good when the server says the session ended', () => {
    const { states } = connect();
    latest().open();
    latest().drop(CLOSE_SIGNED_OUT);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(1);
    expect(states.at(-1)).toBe('signed_out');
  });

  it('reports a rejected CSRF token and waits for reconnect() after the session reload', () => {
    const { conn, states } = connect();
    latest().open();
    latest().drop(CLOSE_CSRF);
    expect(states.at(-1)).toBe('csrf');
    vi.advanceTimersByTime(120_000);
    expect(FakeSocket.all).toHaveLength(1);
    conn.reconnect();
    expect(FakeSocket.all).toHaveLength(2);
  });

  it('waits with growing delays when the token is refused again and again, even on reconnect() (S3)', () => {
    const { conn } = connect();
    const refuse = () => {
      latest().open();
      latest().drop(CLOSE_CSRF);
    };
    refuse();
    conn.reconnect(); // the first refusal: a fresh session is worth one immediate try
    expect(FakeSocket.all).toHaveLength(2);
    refuse();
    conn.reconnect();
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.all).toHaveLength(3);
    refuse();
    conn.reconnect();
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.all).toHaveLength(3);
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.all).toHaveLength(4);
    // Accepted at last: the next refusal is tried again at once.
    latest().open();
    latest().message('{"type":"agents.changed"}');
    latest().drop(CLOSE_CSRF);
    conn.reconnect();
    expect(FakeSocket.all).toHaveLength(5);
  });

  it('does not open without a CSRF token and waits for reconnect()', () => {
    let token: string | null = null;
    const states: EventsState[] = [];
    const conn = connectEvents({ url: 'wss://x/api/events', csrf: () => token, onEvent: () => {}, onState: (s) => states.push(s), socket: (u) => new FakeSocket(u) });
    expect(states).toEqual(['csrf']);
    vi.advanceTimersByTime(120_000);
    expect(FakeSocket.all).toHaveLength(0);
    token = 'later';
    conn.reconnect();
    expect(FakeSocket.all).toHaveLength(1);
  });

  it('adds up to 20 % jitter to the delay', () => {
    connectEvents({ url: 'wss://x/api/events', csrf: () => 't', onEvent: () => {}, onState: () => {}, socket: (u) => new FakeSocket(u), random: () => 1 });
    latest().drop();
    vi.advanceTimersByTime(1199);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it('refuses URLs that are not WebSocket URLs, so the token goes nowhere else', () => {
    expect(() =>
      connectEvents({ url: 'https://evil.example/api/events', csrf: () => 't', onEvent: () => {}, onState: () => {}, socket: (u) => new FakeSocket(u) }),
    ).toThrow();
    expect(FakeSocket.all).toHaveLength(0);
  });

  it('closes and stops reconnecting on close()', () => {
    const { conn } = connect();
    latest().open();
    conn.close();
    expect(latest().closed).toBe(true);
    latest().drop();
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it('reconnects at once on request, e.g. after the session was reloaded', () => {
    const { conn } = connect();
    latest().drop();
    conn.reconnect();
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it('reports a socket that cannot even be created as closed and retries', () => {
    let fail = true;
    const states: EventsState[] = [];
    connectEvents({
      url: 'wss://x/api/events',
      csrf: () => 't',
      onEvent: () => {},
      onState: (s) => states.push(s),
      random: () => 0,
      socket: (u) => {
        if (fail) throw new Error('blocked');
        return new FakeSocket(u);
      },
    });
    expect(states).toEqual(['connecting', 'closed']);
    fail = false;
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.all).toHaveLength(1);
  });
});
