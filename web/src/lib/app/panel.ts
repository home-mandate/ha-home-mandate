// SPDX-License-Identifier: AGPL-3.0-or-later

// The app's panel in Home Assistant (issue #53): a notification of a request links to
// /app/<slug>/requests/<id>. Home Assistant's frontend loads the UI in an iframe at the
// Ingress address alone and hands the rest of its route to an app that subscribes
// (home-assistant/subscribe-properties, src/panels/app/ha-panel-app.ts). Only the parent
// window of the same origin is listened to, and only a request's ID of the form the
// server gives is followed, so the parent can lead the UI to a request's page and nowhere
// else.

/** The route tail of a request's page (cmd/home-mandate, panelRequestPath). */
const REQUEST_TAIL = /^\/requests\/([0-9a-f]{32})$/;

export interface PanelEnv {
  self: Window;
  parent: Window;
  origin: string;
}

/**
 * followPanel subscribes to the panel's properties when the UI is framed and calls open
 * with a request's ID each time the panel's route changes to a request's page. It returns
 * the function that ends it.
 */
export function followPanel(env: PanelEnv, open: (id: string) => void): () => void {
  if (env.parent === env.self) return () => {};
  let last: string | null = null;
  const onMessage = (event: MessageEvent) => {
    if (event.source !== env.parent || event.origin !== env.origin) return;
    const data: unknown = event.data;
    if (typeof data !== 'object' || data === null || (data as { type?: unknown }).type !== 'home-assistant/properties') return;
    const path = (data as { route?: { path?: unknown } }).route?.path;
    if (typeof path !== 'string' || path === last) return;
    last = path;
    const id = REQUEST_TAIL.exec(path)?.[1];
    if (id) open(id);
  };
  env.self.addEventListener('message', onMessage);
  env.parent.postMessage({ type: 'home-assistant/subscribe-properties' }, env.origin);
  return () => {
    env.self.removeEventListener('message', onMessage);
    env.parent.postMessage({ type: 'home-assistant/unsubscribe-properties' }, env.origin);
  };
}
