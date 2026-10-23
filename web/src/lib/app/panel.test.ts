// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { followPanel, type PanelEnv } from './panel.ts';

const ORIGIN = 'https://ha.example.org';
const ID = '0123456789abcdef0123456789abcdef';

function env() {
  const listeners: ((e: MessageEvent) => void)[] = [];
  const parent = { postMessage: vi.fn() } as unknown as Window & { postMessage: ReturnType<typeof vi.fn> };
  const self = {
    addEventListener: (_: string, l: (e: MessageEvent) => void) => listeners.push(l),
    removeEventListener: (_: string, l: (e: MessageEvent) => void) => listeners.splice(listeners.indexOf(l), 1),
  } as unknown as Window;
  const send = (data: unknown, from: { source?: unknown; origin?: string } = {}) =>
    [...listeners].forEach((l) => l({ data, source: from.source ?? parent, origin: from.origin ?? ORIGIN } as MessageEvent));
  const e: PanelEnv = { self, parent, origin: ORIGIN };
  return { e, parent, send, listeners };
}

const properties = (path: unknown) => ({ type: 'home-assistant/properties', narrow: true, route: { prefix: '/app/local_home_mandate', path } });

describe('followPanel', () => {
  it('subscribes to the panel and opens the request its route names (issue #53)', () => {
    const { e, parent, send } = env();
    const open = vi.fn();
    followPanel(e, open);
    expect(parent.postMessage).toHaveBeenCalledWith({ type: 'home-assistant/subscribe-properties' }, ORIGIN);
    send(properties(`/requests/${ID}`));
    expect(open).toHaveBeenCalledWith(ID);
    // The same route again (e.g. narrow changed): the person may have moved on meanwhile.
    send(properties(`/requests/${ID}`));
    expect(open).toHaveBeenCalledTimes(1);
    send(properties(''));
    send(properties(`/requests/${ID}`));
    expect(open).toHaveBeenCalledTimes(2);
  });

  it('follows nothing but a well-formed request from its own parent', () => {
    const { e, send } = env();
    const open = vi.fn();
    followPanel(e, open);
    send(properties(`/requests/${ID}`), { source: {} });
    send(properties(`/requests/${ID}`), { origin: 'https://evil.example' });
    for (const path of [`/requests/${ID.toUpperCase()}`, `/requests/${ID}/x`, `/requests/${ID.slice(1)}`, '/mandates/m-1', `//evil/${ID}`, 42, null]) {
      send(properties(path));
    }
    send({ type: 'home-assistant/navigate', route: { path: `/requests/${ID}` } });
    send('home-assistant/properties');
    send(null);
    expect(open).not.toHaveBeenCalled();
  });

  it('does nothing when the UI is not framed, and ends when asked', () => {
    const lone = env();
    const open = vi.fn();
    followPanel({ ...lone.e, parent: lone.e.self }, open);
    expect(lone.listeners).toHaveLength(0);

    const { e, parent, send, listeners } = env();
    const stop = followPanel(e, open);
    stop();
    expect(listeners).toHaveLength(0);
    expect(parent.postMessage).toHaveBeenLastCalledWith({ type: 'home-assistant/unsubscribe-properties' }, ORIGIN);
    send(properties(`/requests/${ID}`));
    expect(open).not.toHaveBeenCalled();
  });
});
