// SPDX-License-Identifier: AGPL-3.0-or-later

// Svelte action: moves an element to document.body, so a modal dialog sits outside the app
// root and the app can be made inert while it is open.
export function portal(node: HTMLElement): { destroy(): void } {
  document.body.append(node);
  return {
    destroy() {
      node.remove();
    },
  };
}

/** The element made inert behind a modal; the app root in production. */
export function appRoot(): HTMLElement | null {
  return document.getElementById('app');
}
