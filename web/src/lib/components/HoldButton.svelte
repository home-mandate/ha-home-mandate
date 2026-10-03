<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Press-and-hold button for the emergency stop (design README section 5): fires only after
  2 s of holding with the primary pointer, Space or Enter; releasing early, sliding off the
  button or hiding the tab resets. Assistive technology can only click: such a click (no
  pointer or key hold before it) starts the same 2 s run on its own, a second click stops
  it. The bar grows from the inline start. Screen readers hear 50 % and 100 %.
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import { m } from '../i18n.ts';
  import { createHold } from '../ui/hold.ts';
  import Icon from './Icon.svelte';

  /** --hm-motion-hold-confirm; not shortened for reduced motion, it is a safety delay. */
  const HOLD_MS = 2000;

  interface Props {
    label: string;
    onfire: () => void;
    /**
     * Announced at 100 %, before onfire's outcome is known: say that it is happening, not
     * that it happened (review a11y M4); the outcome is the caller's to announce.
     */
    done: string;
    element?: HTMLButtonElement;
    now?: () => number;
  }

  let { label, onfire, done, element = $bindable(), now = () => performance.now() }: Props = $props();

  const id = $props.id();
  const hold = createHold(HOLD_MS);
  let progress = $state(0);
  let spoken = $state('');
  let frame: number | undefined;
  let pointer: number | null = null;
  /** A pointer or key hold just ended; the click the browser may send after it is not an activation. */
  let handled = false;

  function endOfHold() {
    handled = true;
    setTimeout(() => (handled = false), 0);
  }

  function step() {
    const t = hold.tick(now());
    progress = t.progress;
    if (t.announce === 50) spoken = m.estop_hold_progress();
    if (t.announce === 100) spoken = done;
    if (t.fired) {
      stop(false);
      onfire();
      return;
    }
    if (hold.held()) frame = requestAnimationFrame(step);
  }

  function begin() {
    spoken = '';
    frame = requestAnimationFrame(step);
  }

  function stop(clearSpoken = true) {
    hold.release();
    pointer = null;
    if (frame !== undefined) cancelAnimationFrame(frame);
    frame = undefined;
    progress = 0;
    if (clearSpoken) spoken = '';
  }

  function pointerdown(event: PointerEvent) {
    if (event.button !== 0 || !event.isPrimary || hold.held()) return;
    pointer = event.pointerId;
    (event.currentTarget as Element).setPointerCapture?.(event.pointerId);
    hold.press(now());
    begin();
  }

  function pointerend(event: PointerEvent) {
    if (event.pointerId !== pointer) return;
    endOfHold();
    stop();
  }

  /** With the pointer captured, leaving the button only shows in pointermove. */
  function pointermove(event: PointerEvent) {
    if (event.pointerId !== pointer || !element) return;
    const r = element.getBoundingClientRect();
    const inside = event.clientX >= r.left && event.clientX <= r.right && event.clientY >= r.top && event.clientY <= r.bottom;
    if (!inside) stop();
  }

  function keydown(event: KeyboardEvent) {
    if (event.key !== ' ' && event.key !== 'Enter') return;
    event.preventDefault();
    if (event.repeat || hold.held()) return;
    hold.press(now());
    begin();
  }

  function keyup(event: KeyboardEvent) {
    if (event.key !== ' ' && event.key !== 'Enter') return;
    event.preventDefault();
    if (hold.auto()) return;
    endOfHold();
    stop();
  }

  function click() {
    if (handled) {
      handled = false;
      return;
    }
    hold.toggleAuto(now());
    if (hold.held()) begin();
    else stop();
  }

  function visibility() {
    if (document.hidden) stop();
  }

  onDestroy(() => stop());
</script>

<svelte:document onvisibilitychange={visibility} />

<button
  bind:this={element}
  type="button"
  class="hold"
  aria-describedby="{id}-hint"
  onpointerdown={pointerdown}
  onpointerup={pointerend}
  onpointercancel={pointerend}
  onpointermove={pointermove}
  onkeydown={keydown}
  onkeyup={keyup}
  onclick={click}
  onblur={() => stop()}
  oncontextmenu={(e) => e.preventDefault()}
>
  <span class="bar" style:inline-size="{progress * 100}%" aria-hidden="true"></span>
  <span class="content"><Icon name="power" />{label}</span>
</button>
<span id="{id}-hint" class="hint">{m.estop_hold_hint()}</span>
<span class="hm-visually-hidden" role="status">{spoken}</span>

<style>
  .hold {
    position: relative;
    overflow: hidden;
    inline-size: 100%;
    min-block-size: 56px;
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    border: none;
    font: inherit;
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-on-danger);
    background: var(--hm-color-danger-solid);
    cursor: pointer;
    touch-action: none;
    user-select: none;
  }
  .bar {
    position: absolute;
    inset-block: 0;
    inset-inline-start: 0;
    background: rgb(0 0 0 / 0.3);
  }
  .content {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
  }
  .hint {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  @media (forced-colors: active) {
    .hold {
      border: 2px solid ButtonText;
    }
    .bar {
      forced-color-adjust: none;
      background: Highlight;
    }
  }
</style>
