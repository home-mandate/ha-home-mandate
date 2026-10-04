// SPDX-License-Identifier: AGPL-3.0-or-later

// Browser notifications for new approval requests (decisions F2 B1, S4): only while
// Home-Mandate is open in a tab that is hidden, only for requests the signed-in person may
// answer here, and only after the person switched them on in this browser. No Web Push,
// no service worker. The text is neutral like the bell: no agent, device or reason, so
// nothing an agent chose appears outside the page.

import type { ApprovalRequest } from '../api/types.ts';

export type NotifyState = 'unsupported' | 'blocked' | 'off' | 'on';

/** What the notifier needs from the browser; tests pass their own. */
export interface NotifyEnv {
  Notification?: typeof globalThis.Notification;
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
  hidden: () => boolean;
}

const KEY = 'hm-notify';
/** One notification at a time: a newer request replaces the older one. */
const TAG = 'hm-approval';

function stored(env: NotifyEnv): boolean {
  try {
    return env.storage?.getItem(KEY) === 'on';
  } catch {
    return false; // storage blocked: off
  }
}

function store(env: NotifyEnv, on: boolean): void {
  try {
    if (on) env.storage?.setItem(KEY, 'on');
    else env.storage?.removeItem(KEY);
  } catch {
    // Without storage the switch lasts for this page only.
  }
}

export class BrowserNotifier {
  state: NotifyState = $state('unsupported');
  readonly #env: NotifyEnv;

  constructor(env: NotifyEnv) {
    this.#env = env;
    this.state = this.#current(stored(env));
  }

  #current(wanted: boolean, permission = this.#env.Notification?.permission): NotifyState {
    if (!this.#env.Notification) return 'unsupported';
    if (permission === 'denied') return 'blocked';
    return wanted && permission === 'granted' ? 'on' : 'off';
  }

  /** enable asks the browser (only on a click) and remembers the choice in this browser. */
  async enable(): Promise<void> {
    const api = this.#env.Notification;
    if (!api) return;
    let permission: NotificationPermission;
    try {
      permission = api.permission === 'granted' ? 'granted' : await api.requestPermission();
    } catch {
      permission = 'default'; // refused by policy or the browser: stays off
    }
    store(this.#env, permission === 'granted');
    this.state = this.#current(permission === 'granted', permission);
  }

  disable(): void {
    store(this.#env, false);
    this.state = this.#current(false);
  }

  /**
   * notify shows a notification for a new request when it is switched on, the tab is
   * hidden and the person may answer the request here; a click brings the page back.
   */
  notify(request: ApprovalRequest, text: { title: string; body: string }, open: () => void): void {
    const api = this.#env.Notification;
    this.state = this.#current(this.state === 'on');
    if (!api || this.state !== 'on' || !this.#env.hidden() || !request.can_answer) return;
    try {
      const note = new api(text.title, { body: text.body, tag: TAG });
      note.onclick = () => {
        note.close();
        open();
      };
    } catch {
      // Some browsers only show notifications through a service worker (Android Chrome).
      this.state = 'unsupported';
    }
  }
}
