// SPDX-License-Identifier: AGPL-3.0-or-later

// Room for the fixed save bar of the editors (issue #20). While the bar is shown, the root
// element carries a class and the bar's height as a custom property; app.css turns them into
// padding at the end of the page (so the end stays reachable above the bar) and
// scroll-padding (so focused or scrolled-to elements never land behind it). The property is
// written through the CSSOM, not a style attribute, which the CSP allows. Neither changes the
// scroll position: the page grows only at its end.

/** Class on the root element while a save bar is shown. */
export const SAVEBAR_CLASS = 'hm-savebar';
/** Custom property with the bar's height in px. */
export const SAVEBAR_SIZE = '--hm-savebar-size';

/**
 * reserveRoom keeps the bar's height on root while the bar is shown, following its size
 * (wrapping text, zoom); the returned function gives the room back.
 */
export function reserveRoom(bar: HTMLElement, root: HTMLElement = document.documentElement, Observer: typeof ResizeObserver | undefined = globalThis.ResizeObserver): () => void {
  const measure = () => root.style.setProperty(SAVEBAR_SIZE, `${Math.ceil(bar.getBoundingClientRect().height)}px`);
  root.classList.add(SAVEBAR_CLASS);
  measure();
  const observer = Observer ? new Observer(measure) : null;
  observer?.observe(bar);
  return () => {
    observer?.disconnect();
    root.classList.remove(SAVEBAR_CLASS);
    root.style.removeProperty(SAVEBAR_SIZE);
  };
}
