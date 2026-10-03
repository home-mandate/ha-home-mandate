// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { approvalsOpenFixture } from '../api/fixtures.ts';
import type { ApprovalRequest } from '../api/types.ts';
import { BrowserNotifier, type NotifyEnv } from './notifier.svelte.ts';

const TEXT = { title: 'Approval waiting', body: 'A request is waiting in Home-Mandate.' };
const request = (canAnswer: boolean): ApprovalRequest => ({ ...(approvalsOpenFixture[0] as ApprovalRequest), can_answer: canAnswer });

function browser(permission: NotificationPermission, answer: NotificationPermission = 'granted', saved: string | null = null) {
  const shown: { title: string; options?: NotificationOptions; note: { onclick: (() => void) | null; close: () => void } }[] = [];
  const memory = new Map<string, string>(saved ? [['hm-notify', saved]] : []);
  let hidden = true;
  const Notification = Object.assign(
    function (this: unknown, title: string, options?: NotificationOptions) {
      const note = { onclick: null, close: vi.fn() };
      shown.push({ title, options, note });
      return note;
    },
    { permission, requestPermission: vi.fn(async () => answer) },
  ) as unknown as NonNullable<NotifyEnv['Notification']>;
  const env: NotifyEnv = {
    Notification,
    storage: { getItem: (k) => memory.get(k) ?? null, setItem: (k, v) => void memory.set(k, v), removeItem: (k) => void memory.delete(k) },
    hidden: () => hidden,
  };
  return { env, shown, memory, setHidden: (h: boolean) => (hidden = h), setPermission: (p: NotificationPermission) => Object.assign(Notification, { permission: p }) };
}

describe('BrowserNotifier', () => {
  it('is off until switched on, asks the browser only then, and remembers it', async () => {
    const b = browser('default');
    const n = new BrowserNotifier(b.env);
    expect(n.state).toBe('off');
    n.notify(request(true), TEXT, () => {});
    expect(b.shown).toHaveLength(0);
    await n.enable();
    expect(n.state).toBe('on');
    expect(b.memory.get('hm-notify')).toBe('on');
    expect(new BrowserNotifier(b.env).state).toBe('off'); // the permission is still "default" in this fake
  });

  it('notifies only for requests the person may answer here, and only while the tab is hidden', () => {
    const b = browser('granted', 'granted', 'on');
    const n = new BrowserNotifier(b.env);
    expect(n.state).toBe('on');
    n.notify(request(false), TEXT, () => {});
    b.setHidden(false);
    n.notify(request(true), TEXT, () => {});
    expect(b.shown).toHaveLength(0);
    b.setHidden(true);
    const open = vi.fn();
    n.notify(request(true), TEXT, open);
    expect(b.shown).toHaveLength(1);
    expect(b.shown[0]).toMatchObject({ title: TEXT.title, options: { body: TEXT.body, tag: 'hm-approval' } });
    b.shown[0]?.note.onclick?.();
    expect(open).toHaveBeenCalled();
    expect(b.shown[0]?.note.close).toHaveBeenCalled();
  });

  it('keeps the agent text out of the notification', () => {
    const b = browser('granted', 'granted', 'on');
    new BrowserNotifier(b.env).notify(request(true), TEXT, () => {});
    const text = JSON.stringify(b.shown[0]);
    const r = request(true);
    for (const claim of [r.agent.display_name, r.reason ?? '', r.device_name]) expect(text).not.toContain(claim);
  });

  it('reports a blocked permission and a browser without notifications', async () => {
    const blocked = browser('default', 'denied');
    const n = new BrowserNotifier(blocked.env);
    await n.enable();
    expect(n.state).toBe('blocked');
    expect(new BrowserNotifier({ hidden: () => true }).state).toBe('unsupported');
  });

  it('switches off and forgets; a permission revoked later stops it too', async () => {
    const b = browser('granted', 'granted', 'on');
    const n = new BrowserNotifier(b.env);
    n.disable();
    expect(n.state).toBe('off');
    expect(b.memory.has('hm-notify')).toBe(false);
    await n.enable();
    b.setPermission('denied');
    n.notify(request(true), TEXT, () => {});
    expect(n.state).toBe('blocked');
    expect(b.shown).toHaveLength(0);
  });

  it('never throws: a browser that refuses the constructor or the question', async () => {
    const throwing = Object.assign(
      function () {
        throw new TypeError('Illegal constructor');
      },
      { permission: 'granted', requestPermission: async () => 'granted' },
    ) as unknown as typeof globalThis.Notification;
    const memory = new Map([['hm-notify', 'on']]);
    const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: () => {}, removeItem: () => {} };
    const n = new BrowserNotifier({ Notification: throwing, storage, hidden: () => true });
    expect(() => n.notify(request(true), TEXT, () => {})).not.toThrow();
    expect(n.state).toBe('unsupported');
    const rejecting = Object.assign(function () {}, {
      permission: 'default',
      requestPermission: async () => Promise.reject(new Error('policy')),
    }) as unknown as typeof globalThis.Notification;
    const off = new BrowserNotifier({ Notification: rejecting, hidden: () => true });
    await expect(off.enable()).resolves.toBeUndefined();
    expect(off.state).toBe('off');
  });

  it('works without storage', async () => {
    const b = browser('granted');
    const env: NotifyEnv = {
      ...b.env,
      storage: {
        getItem: () => {
          throw new Error('blocked');
        },
        setItem: () => {
          throw new Error('blocked');
        },
        removeItem: () => {},
      },
    };
    const n = new BrowserNotifier(env);
    expect(n.state).toBe('off');
    await n.enable();
    expect(n.state).toBe('on');
  });
});
