<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Icon-only button, only for non-decisions (close, more, copy in tight rows). 44×44 hit area,
  aria-label, and a tooltip with the same text after 600 ms of hover or focus. The tooltip
  can be hovered, and Esc hides it without closing a surrounding dialog (WCAG 1.4.13).
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import Icon, { type IconName } from './Icon.svelte';

  const TOOLTIP_DELAY_MS = 600;

  interface Props extends Omit<HTMLButtonAttributes, 'disabled'> {
    icon: IconName;
    label: string;
    element?: HTMLButtonElement;
  }

  let { icon, label, type = 'button', element = $bindable(), onfocus, onblur, ...rest }: Props = $props();

  let tip = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function show() {
    clearTimeout(timer);
    timer = setTimeout(() => (tip = true), TOOLTIP_DELAY_MS);
  }
  function hide() {
    clearTimeout(timer);
    tip = false;
  }

  /** Capture phase: runs before a dialog's Esc handler, which then sees defaultPrevented. */
  $effect(() => {
    if (!tip) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      hide();
    };
    window.addEventListener('keydown', escape, true);
    return () => window.removeEventListener('keydown', escape, true);
  });

  onDestroy(() => clearTimeout(timer));
</script>

<!-- svelte-ignore a11y_no_static_element_interactions (hover area of the tooltip, no action) -->
<span class="wrap" onpointerenter={show} onpointerleave={hide}>
  <button
    bind:this={element}
    {...rest}
    {type}
    aria-label={label}
    onfocus={(e) => {
      show();
      onfocus?.(e);
    }}
    onblur={(e) => {
      hide();
      onblur?.(e);
    }}
  >
    <Icon name={icon} />
  </button>
  {#if tip}<span class="tip" aria-hidden="true">{label}</span>{/if}
</span>

<style>
  .wrap {
    position: relative;
    display: inline-flex;
  }
  button {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: var(--hm-size-touch);
    block-size: var(--hm-size-touch);
    border-radius: var(--hm-radius-md);
    border: none;
    background: transparent;
    color: var(--hm-color-text-muted);
    cursor: pointer;
  }
  button:hover,
  button:focus-visible {
    background: var(--hm-color-surface-hover);
    color: var(--hm-color-text);
  }
  .tip {
    position: absolute;
    inset-block-start: 100%;
    inset-inline-start: 50%;
    translate: -50% 0;
    z-index: 10;
    margin-block-start: 6px;
    padding: var(--hm-space-1) var(--hm-space-2);
    border-radius: 6px;
    background: var(--hm-color-text);
    color: var(--hm-color-surface);
    font-size: var(--hm-font-size-xs);
    white-space: nowrap;
  }
  /* Bridges the 6 px gap so the pointer can move onto the tooltip. */
  .tip::before {
    content: '';
    position: absolute;
    inset-inline: 0;
    inset-block-start: -6px;
    block-size: 6px;
  }
  :global([dir='rtl']) .tip {
    translate: 50% 0;
  }
  @media (forced-colors: active) {
    .tip {
      border: 1px solid CanvasText;
    }
  }
</style>
