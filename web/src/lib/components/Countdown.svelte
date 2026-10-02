<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Countdown of an approval request (role="timer"), on the server's clock (offsetMs from
  ui/countdown.ts). The bar shrinks; under 15 s nothing changes colour or blinks. Screen
  readers hear only 60, 30 and 10 seconds and the end.
-->
<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { getLocale } from '../paraglide/runtime.js';
  import { m } from '../i18n.ts';
  import { announcement, formatClock, remainingSeconds } from '../ui/countdown.ts';

  const TICK_MS = 250;

  interface Props {
    expiresAt: string;
    /** Total time of the request in seconds, for the bar. */
    totalSeconds: number;
    /** Browser clock minus server clock in ms. */
    offsetMs: number;
    size?: 'md' | 'lg';
    now?: () => number;
  }

  let { expiresAt, totalSeconds, offsetMs, size = 'md', now = Date.now }: Props = $props();

  let left = $state(0);
  let spoken = $state('');
  let last: number | null = null;

  function say(seconds: number) {
    spoken = seconds === 0 ? m.countdown_expired() : m.countdown_seconds_left({ count: seconds });
  }

  function update() {
    left = remainingSeconds(expiresAt, offsetMs, now());
    if (last === null && left > 0 && left <= 60) {
      say(left); // a request that starts under a minute says once how long it has
    } else {
      const at = announcement(last, left);
      if (at !== null) say(at);
    }
    last = left;
  }

  /** A new request (other expiry) starts over, including its announcements. */
  $effect(() => {
    void expiresAt;
    untrack(() => {
      last = null;
      spoken = '';
      update();
    });
  });

  const timer = setInterval(update, TICK_MS);
  onDestroy(() => clearInterval(timer));

  const share = $derived(totalSeconds > 0 ? Math.min(1, left / totalSeconds) : 0);
</script>

<div class="countdown {size}" class:expired={left === 0} role="timer" aria-live="off">
  <span class="value">{formatClock(left, getLocale())}</span>
  <span class="track" aria-hidden="true"><span class="fill" style:inline-size="{share * 100}%"></span></span>
  <span class="hm-visually-hidden" aria-live="polite">{spoken}</span>
</div>

<style>
  .countdown {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    color: var(--hm-color-ask-fg);
  }
  .value {
    font-family: var(--hm-font-mono);
    font-size: 30px;
    line-height: 1.1;
    font-weight: var(--hm-font-weight-semibold);
    font-variant-numeric: tabular-nums;
  }
  .lg .value {
    font-size: var(--hm-font-size-3xl);
  }
  .track {
    display: block;
    block-size: 6px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-sunken);
    overflow: hidden;
  }
  .fill {
    display: block;
    block-size: 100%;
    background: var(--hm-color-ask-solid);
  }
  .expired {
    color: var(--hm-color-text-subtle);
  }
  .expired .fill {
    background: var(--hm-color-border);
  }
  @media (forced-colors: active) {
    .track {
      border: 1px solid CanvasText;
    }
    .fill {
      forced-color-adjust: none;
      background: Highlight;
    }
  }
</style>
