// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ScrollSpy } from './spy.svelte.ts';

/** FakeObserver stands in for IntersectionObserver (jsdom has none); see() reports changes. */
type Report = (entries: { target: Element; isIntersecting: boolean }[]) => void;

class FakeObserver {
  static last: FakeObserver | null = null;
  readonly watched: Element[] = [];
  readonly report: Report;
  readonly options: { rootMargin?: string };
  disconnected = false;
  constructor(report: Report, options: { rootMargin?: string }) {
    this.report = report;
    this.options = options;
    FakeObserver.last = this;
  }
  observe(el: Element) {
    this.watched.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
  see(...changes: [Element, boolean][]) {
    this.report(changes.map(([target, isIntersecting]) => ({ target, isIntersecting })));
  }
}

const el = () => document.createElement('section');

describe('ScrollSpy', () => {
  it('marks the first section, in page order, that is in the upper part of the window', () => {
    const [a, b, c] = [el(), el(), el()];
    const spy = new ScrollSpy(['a', 'b', 'c']);
    spy.observe([['a', a], ['b', b], ['c', c]], FakeObserver as unknown as typeof IntersectionObserver);
    const io = FakeObserver.last as FakeObserver;
    expect(io.watched).toEqual([a, b, c]);
    expect(io.options.rootMargin).toBe('0px 0px -60% 0px');
    expect(spy.current).toBeNull();
    io.see([b, true], [c, true]);
    expect(spy.current).toBe('b');
    io.see([b, false]);
    expect(spy.current).toBe('c');
    io.see([c, false]);
    expect(spy.current).toBe('c'); // between sections: keep the last one
  });

  it('stops watching, and does nothing without IntersectionObserver', () => {
    const spy = new ScrollSpy(['a']);
    const stop = spy.observe([['a', el()]], FakeObserver as unknown as typeof IntersectionObserver);
    stop();
    expect(FakeObserver.last?.disconnected).toBe(true);
    expect(() => new ScrollSpy(['a']).observe([['a', el()]], undefined)()).not.toThrow();
  });

  it('keeps a chosen section marked until the person scrolls (review M1)', () => {
    const [a, b, c] = [el(), el(), el()];
    const spy = new ScrollSpy(['a', 'b', 'c']);
    spy.observe([['a', a], ['b', b], ['c', c]], FakeObserver as unknown as typeof IntersectionObserver);
    const io = FakeObserver.last as FakeObserver;
    spy.pin('c'); // e.g. "About" at the end, which never reaches the top of the window
    io.see([b, true], [c, true]);
    expect(spy.current).toBe('c');
    spy.release();
    expect(spy.current).toBe('b');
  });
});
