// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { createHold } from './hold.ts';

describe('createHold', () => {
  it('fires once after the full duration and announces 50 % and 100 %', () => {
    const hold = createHold(2000);
    hold.press(1000);
    expect(hold.tick(1500)).toEqual({ progress: 0.25, fired: false, announce: null });
    expect(hold.tick(2000)).toEqual({ progress: 0.5, fired: false, announce: 50 });
    expect(hold.tick(2500)).toEqual({ progress: 0.75, fired: false, announce: null });
    expect(hold.tick(3000)).toEqual({ progress: 1, fired: true, announce: 100 });
    expect(hold.tick(3500)).toEqual({ progress: 1, fired: false, announce: null });
  });

  it('resets when released early', () => {
    const hold = createHold(2000);
    hold.press(0);
    hold.tick(1500);
    hold.release();
    expect(hold.tick(5000)).toEqual({ progress: 0, fired: false, announce: null });
    hold.press(5000);
    expect(hold.tick(5600).progress).toBeCloseTo(0.3);
  });

  it('ignores a second press while held (key repeat)', () => {
    const hold = createHold(2000);
    hold.press(0);
    hold.press(1500);
    expect(hold.tick(2000).fired).toBe(true);
  });

  it('can be held again after firing only after release', () => {
    const hold = createHold(2000);
    hold.press(0);
    hold.tick(2000);
    hold.press(2100);
    expect(hold.tick(5000).fired).toBe(false);
    hold.release();
    hold.press(6000);
    expect(hold.tick(8000).fired).toBe(true);
  });

  it('fires at once with a zero duration (reduced motion keeps the hold, so never 0 in practice)', () => {
    const hold = createHold(0);
    hold.press(10);
    expect(hold.tick(10).fired).toBe(true);
  });

  it('runs on its own after a single activation by assistive technology, and stops on a second', () => {
    const hold = createHold(2000);
    hold.toggleAuto(0);
    expect(hold.held()).toBe(true);
    expect(hold.auto()).toBe(true);
    expect(hold.tick(2000).fired).toBe(true);
    const other = createHold(2000);
    other.toggleAuto(0);
    other.tick(1000);
    other.toggleAuto(1000);
    expect(other.held()).toBe(false);
    expect(other.tick(5000)).toEqual({ progress: 0, fired: false, announce: null });
  });

  it('reports whether it is held', () => {
    const hold = createHold(2000);
    expect(hold.held()).toBe(false);
    hold.press(0);
    expect(hold.held()).toBe(true);
    hold.release();
    expect(hold.held()).toBe(false);
  });
});
