<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Renders the toast queue (lib/ui/toasts.ts) bottom-center, max 480 px. The two live regions
  (polite for success and undo, assertive for errors) exist before any toast arrives, so
  screen readers announce new ones reliably. Timers pause while the pointer or focus is on
  the toasts. While toasts are shown, the page keeps that much room below (scroll-padding),
  so they cover no focused control; Escape closes the newest error unless a dialog is open
  (review a11y M5).
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { toasts as defaultToasts, type Toast, type Toasts } from '../ui/toasts.ts';
  import Icon from './Icon.svelte';

  interface Props {
    toasts?: Toasts;
  }

  let { toasts = defaultToasts }: Props = $props();

  let list: readonly Toast[] = $state([]);
  $effect(() => toasts.subscribe((l) => (list = l)));

  const polite = $derived(list.filter((t) => t.kind !== 'error'));
  const errors = $derived(list.filter((t) => t.kind === 'error'));

  /** Gap between the toasts and a focused control scrolled into view. */
  const ROOM_PX = 16;
  let host: HTMLElement | undefined = $state();

  $effect(() => {
    const root = document.documentElement.style;
    if (list.length === 0 || !host) {
      root.scrollPaddingBlockEnd = '';
      return;
    }
    root.scrollPaddingBlockEnd = `${Math.ceil(host.getBoundingClientRect().height) + ROOM_PX}px`;
    return () => (root.scrollPaddingBlockEnd = '');
  });

  function keydown(event: KeyboardEvent) {
    if (event.key !== 'Escape' || errors.length === 0) return;
    // A dialog or sheet owns Escape while it is open.
    if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return;
    const newest = errors.at(-1);
    if (newest) toasts.dismiss(newest.id);
  }
</script>

<svelte:window onkeydown={keydown} />

{#snippet toastView(toast: Toast)}
  <div class="toast {toast.kind}">
    <span class="icon"><Icon name={toast.kind === 'error' ? 'warning' : 'check'} /></span>
    <span class="text">{toast.text}</span>
    {#if toast.action}
      <button type="button" class="action" onclick={() => toasts.act(toast.id)}>{toast.action.label}</button>
    {/if}
    {#if toast.kind === 'error'}
      <button type="button" class="close" aria-label={m.common_close()} onclick={() => toasts.dismiss(toast.id)}>
        <Icon name="close" />
      </button>
    {/if}
  </div>
{/snippet}

<!-- svelte-ignore a11y_no_static_element_interactions (pausing only, no action) -->
<div
  bind:this={host}
  class="host"
  onpointerenter={() => toasts.pause()}
  onpointerleave={() => toasts.resume()}
  onfocusin={() => toasts.pause()}
  onfocusout={() => toasts.resume()}
>
  <div class="region" role="status">
    {#each polite as toast (toast.id)}{@render toastView(toast)}{/each}
  </div>
  <div class="region" role="alert">
    {#each errors as toast (toast.id)}{@render toastView(toast)}{/each}
  </div>
</div>

<style>
  .host,
  .region {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hm-space-2);
  }
  .host {
    position: fixed;
    inset-block-end: var(--hm-space-4);
    inset-inline: var(--hm-space-4);
    z-index: 100;
    pointer-events: none;
  }
  /* Above the save bar of the editors, never over its buttons. */
  :global(:root.hm-savebar) .host {
    inset-block-end: calc(var(--hm-savebar-size, 0px) + var(--hm-space-4));
  }
  .region {
    inline-size: 100%;
  }
  .toast {
    pointer-events: auto;
    inline-size: 100%;
    max-inline-size: 480px;
    box-sizing: border-box;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) 10px;
    padding: var(--hm-space-3) var(--hm-space-3) var(--hm-space-3) var(--hm-space-4);
    border-radius: 10px;
    background: var(--hm-color-text);
    color: var(--hm-color-surface);
    box-shadow: var(--hm-shadow-lg);
    font-size: 15px;
    animation: enter 150ms var(--hm-motion-easing-enter);
  }
  .icon {
    display: flex;
  }
  .error .icon {
    color: var(--hm-color-danger-solid);
  }
  .text {
    flex: 1 1 180px;
    overflow-wrap: anywhere;
  }
  .action,
  .close {
    min-block-size: var(--hm-size-touch);
    border-radius: 6px;
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    background: transparent;
    color: inherit;
    cursor: pointer;
  }
  .action {
    padding-inline: var(--hm-space-3);
    border: var(--hm-border-width) solid currentColor;
  }
  .close {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: var(--hm-size-touch);
    border: none;
  }
  @keyframes enter {
    from {
      opacity: 0;
      translate: 0 8px;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .toast {
      animation: none;
    }
  }
  @media (forced-colors: active) {
    .toast {
      border: 2px solid CanvasText;
    }
  }
</style>
