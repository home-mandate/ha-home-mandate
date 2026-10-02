// SPDX-License-Identifier: AGPL-3.0-or-later

// Focus handling for dialogs and sheets (design README section 7): a focus trap that keeps
// Tab inside, and the list of elements that can take focus. aria-disabled buttons stay in
// the list on purpose: they must be reachable so screen readers can say why they are off.

const CANDIDATES = [
  'a[href]',
  'area[href]',
  'summary',
  '[contenteditable="true"]',
  '[contenteditable=""]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]',
].join(',');

/** visible uses checkVisibility where the browser has it (display:none, visibility:hidden). */
function visible(el: HTMLElement): boolean {
  const check = (el as HTMLElement & { checkVisibility?: (o?: object) => boolean }).checkVisibility;
  return check ? check.call(el, { visibilityProperty: true }) : true;
}

/** tabbable: an explicit tabindex decides; editable regions and summary are tabbable by nature. */
function tabbable(el: HTMLElement): boolean {
  const explicit = el.getAttribute('tabindex');
  if (explicit !== null) return Number(explicit) >= 0;
  return el.tabIndex >= 0 || el.matches('[contenteditable], summary');
}

/** focusables returns the elements Tab can reach inside root, in document order. */
export function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(CANDIDATES)].filter(
    (el) => tabbable(el) && !el.closest('[hidden], [inert]') && visible(el),
  );
}

/** trapTab keeps Tab and Shift+Tab inside root; returns whether it moved focus itself. */
export function trapTab(root: HTMLElement, event: KeyboardEvent): boolean {
  if (event.key !== 'Tab') return false;
  const list = focusables(root);
  const first = list[0];
  const last = list.at(-1);
  if (!first || !last) {
    event.preventDefault();
    return true;
  }
  const active = document.activeElement;
  const outside = !(active instanceof HTMLElement) || !root.contains(active);
  if (outside || (event.shiftKey && active === first) || (!event.shiftKey && active === last)) {
    event.preventDefault();
    (outside || !event.shiftKey ? first : last).focus();
    return true;
  }
  return false;
}
