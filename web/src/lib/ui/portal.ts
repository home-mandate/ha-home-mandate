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

/** Frame the page scrolls in while a save bar is docked (App.svelte, issue #20). */
export const SCROLL_ID = 'hm-scroll';
/** Place below that frame for a bar that must never cover the page. */
export const DOCK_ID = 'hm-dock';

/**
 * Svelte action: moves an element into the dock below the page; it stays where it is when
 * there is no dock (a page rendered on its own).
 */
export function dock(node: HTMLElement): { destroy(): void } {
  document.getElementById(DOCK_ID)?.append(node);
  return {
    destroy() {
      node.remove();
    },
  };
}
