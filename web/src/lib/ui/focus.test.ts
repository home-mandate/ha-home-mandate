// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { focusables, trapTab } from './focus.ts';

afterEach(() => document.body.replaceChildren());

function setup(html: string): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = html;
  document.body.append(root);
  return root;
}

const tab = (shiftKey = false) => new KeyboardEvent('keydown', { key: 'Tab', shiftKey, cancelable: true });

describe('focusables', () => {
  it('lists enabled, tabbable elements in order, including aria-disabled ones', () => {
    const root = setup(`
      <button id="a">a</button>
      <button id="b" disabled>b</button>
      <a id="c" href="#x">c</a>
      <a id="d">no href</a>
      <input id="e" />
      <input id="f" type="hidden" />
      <div id="g" tabindex="0">g</div>
      <div id="h" tabindex="-1">h</div>
      <button id="i" aria-disabled="true">i</button>
      <select id="j"></select>
      <textarea id="k"></textarea>
      <div hidden><button id="l">l</button></div>
      <details><summary id="m">m</summary></details>
      <div id="n" contenteditable="true">n</div>
      <div inert><button id="o">o</button></div>
    `);
    expect(focusables(root).map((e) => e.id)).toEqual(['a', 'c', 'e', 'g', 'i', 'j', 'k', 'm', 'n']);
  });
});

describe('focusables and visibility', () => {
  it('skips elements the browser reports as not visible', () => {
    const root = setup('<button id="a">a</button><button id="b">b</button>');
    const b = root.querySelector('#b') as HTMLElement & { checkVisibility?: () => boolean };
    b.checkVisibility = () => false;
    expect(focusables(root).map((e) => e.id)).toEqual(['a']);
  });
});

describe('trapTab', () => {
  it('wraps Tab from the last to the first element and Shift+Tab back', () => {
    const root = setup('<button id="a">a</button><button id="b">b</button>');
    const [a, b] = focusables(root) as [HTMLElement, HTMLElement];
    b.focus();
    const forward = tab();
    expect(trapTab(root, forward)).toBe(true);
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(a);
    const back = tab(true);
    trapTab(root, back);
    expect(document.activeElement).toBe(b);
  });

  it('leaves Tab inside the dialog to the browser', () => {
    const root = setup('<button id="a">a</button><button id="b">b</button><button id="c">c</button>');
    (root.querySelector('#b') as HTMLElement).focus();
    const e = tab();
    expect(trapTab(root, e)).toBe(false);
    expect(e.defaultPrevented).toBe(false);
  });

  it('pulls focus back in when it is outside', () => {
    const root = setup('<button id="a">a</button>');
    const outside = document.createElement('button');
    document.body.append(outside);
    outside.focus();
    trapTab(root, tab());
    expect(document.activeElement?.id).toBe('a');
  });

  it('ignores other keys and keeps focus on the root without focusables', () => {
    const root = setup('<p>nothing</p>');
    root.tabIndex = -1;
    expect(trapTab(root, new KeyboardEvent('keydown', { key: 'a' }))).toBe(false);
    root.focus();
    const e = tab();
    expect(trapTab(root, e)).toBe(true);
    expect(e.defaultPrevented).toBe(true);
  });
});
