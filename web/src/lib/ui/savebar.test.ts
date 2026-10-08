// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { reserveRoom, SAVEBAR_CLASS, SAVEBAR_SIZE } from './savebar.ts';

/** FakeObserver stands in for ResizeObserver: tests resize the bar and fire the callback. */
class FakeObserver {
  static last: FakeObserver | null = null;
  observed: Element[] = [];
  disconnected = false;
  readonly callback: () => void;
  constructor(callback: () => void) {
    this.callback = callback;
    FakeObserver.last = this;
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
}

function setup(height: number) {
  const root = document.createElement('html');
  const bar = document.createElement('section');
  let h = height;
  bar.getBoundingClientRect = () => ({ height: h }) as DOMRect;
  return { root, bar, resize: (next: number) => (h = next) };
}

describe('reserveRoom', () => {
  it('marks the root and sets the bar height as a custom property, rounded up', () => {
    const { root, bar } = setup(72.4);
    reserveRoom(bar, root, FakeObserver as unknown as typeof ResizeObserver);
    expect(root.classList.contains(SAVEBAR_CLASS)).toBe(true);
    expect(root.style.getPropertyValue(SAVEBAR_SIZE)).toBe('73px');
    expect(FakeObserver.last?.observed).toEqual([bar]);
  });

  it('follows the bar when its height changes', () => {
    const { root, bar, resize } = setup(72);
    reserveRoom(bar, root, FakeObserver as unknown as typeof ResizeObserver);
    resize(140);
    FakeObserver.last?.callback();
    expect(root.style.getPropertyValue(SAVEBAR_SIZE)).toBe('140px');
  });

  it('gives the room back when the bar goes', () => {
    const { root, bar } = setup(72);
    const release = reserveRoom(bar, root, FakeObserver as unknown as typeof ResizeObserver);
    release();
    expect(root.classList.contains(SAVEBAR_CLASS)).toBe(false);
    expect(root.style.getPropertyValue(SAVEBAR_SIZE)).toBe('');
    expect(FakeObserver.last?.disconnected).toBe(true);
  });

  it('measures once where the browser has no ResizeObserver', () => {
    const { root, bar } = setup(80);
    const release = reserveRoom(bar, root, undefined);
    expect(root.style.getPropertyValue(SAVEBAR_SIZE)).toBe('80px');
    release();
    expect(root.style.getPropertyValue(SAVEBAR_SIZE)).toBe('');
  });
});
