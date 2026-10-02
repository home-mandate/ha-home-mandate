// SPDX-License-Identifier: AGPL-3.0-or-later

// Hold-to-confirm (emergency stop, design README section 5): the action fires only after the
// button was held for the whole duration with pointer, Space or Enter; releasing early
// resets. Assistive technology (VoiceOver, TalkBack, voice control) can only click: one
// activation starts the same 2 s run on its own ("auto"), a second one stops it. Screen
// readers hear 50 % and 100 %, not every frame.

export interface HoldTick {
  /** 0..1 */
  progress: number;
  /** True exactly once, on the tick that completes the hold. */
  fired: boolean;
  announce: 50 | 100 | null;
}

export interface Hold {
  press(now: number): void;
  release(): void;
  tick(now: number): HoldTick;
  held(): boolean;
  /** Starts the run without holding, or stops a running one. */
  toggleAuto(now: number): void;
  auto(): boolean;
}

export function createHold(durationMs: number): Hold {
  let start: number | null = null;
  let announced = 0;
  let fired = false;
  let automatic = false;

  return {
    press(now) {
      if (start !== null) return; // key repeat while held
      start = now;
      announced = 0;
      fired = false;
    },
    release() {
      start = null;
      announced = 0;
      automatic = false;
    },
    tick(now) {
      if (start === null) return { progress: 0, fired: false, announce: null };
      const progress = durationMs <= 0 ? 1 : Math.min(1, Math.max(0, (now - start) / durationMs));
      let announce: 50 | 100 | null = null;
      if (progress >= 0.5 && announced < 50 && progress < 1) {
        announced = 50;
        announce = 50;
      }
      const firesNow = progress >= 1 && !fired;
      if (firesNow) {
        fired = true;
        announced = 100;
        announce = 100;
      }
      return { progress, fired: firesNow, announce };
    },
    held: () => start !== null,
    toggleAuto(now) {
      if (start !== null) {
        this.release();
        return;
      }
      this.press(now);
      automatic = true;
    },
    auto: () => automatic,
  };
}
