<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Modal dialog; a bottom sheet below 768 px (design README sections 5 and 7). It is moved to
  <body> and the app behind it becomes inert. Focus moves in on open (to `initial`, else the
  first focusable element; destructive dialogs pass their safe button), cannot leave, and
  returns to the trigger on close (or to <main> if the trigger is gone). Esc closes. A
  click on the backdrop closes only non-destructive dialogs, and only if it started there.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { tick } from 'svelte';
  import { focusables, trapTab } from '../ui/focus.ts';
  import { appRoot, portal } from '../ui/portal.ts';

  interface Props {
    open: boolean;
    /** Id of the element that names the dialog (usually its heading). */
    labelledby: string;
    /** Id of the text that explains the consequence; give it for destructive dialogs. */
    describedby?: string;
    destructive?: boolean;
    /** lg: 600 px for dialogs with lists (save summary). */
    size?: 'md' | 'lg';
    /** Element to focus on open; destructive dialogs pass their safe button. */
    initial?: HTMLElement | null;
    onclose: () => void;
    children: Snippet;
  }

  let { open, labelledby, describedby, destructive = false, size = 'md', initial = null, onclose, children }: Props = $props();

  let panel: HTMLElement | undefined = $state();
  let pressedOnBackdrop = false;

  function focusInside() {
    const target = initial ?? (panel ? focusables(panel)[0] : undefined) ?? panel;
    target?.focus();
  }

  $effect(() => {
    if (!open) return;
    const trigger = document.activeElement;
    const app = appRoot();
    const wasInert = app?.inert ?? false;
    if (app) app.inert = true;
    const overflow = document.documentElement.style.overflow;
    document.documentElement.style.overflow = 'hidden';
    let cancelled = false;
    void tick().then(() => {
      if (!cancelled) focusInside();
    });
    return () => {
      cancelled = true;
      if (app) app.inert = wasInert;
      document.documentElement.style.overflow = overflow;
      returnFocus(trigger);
    };
  });

  function returnFocus(trigger: Element | null) {
    if (trigger instanceof HTMLElement && trigger.isConnected && !trigger.closest('[inert]')) {
      trigger.focus();
      return;
    }
    const main = document.querySelector('main');
    if (main instanceof HTMLElement) {
      if (!main.hasAttribute('tabindex')) main.tabIndex = -1;
      main.focus();
    }
  }

  function keydown(event: KeyboardEvent) {
    if (!open || !panel) return;
    if (event.key === 'Escape') {
      if (event.defaultPrevented) return; // e.g. a tooltip closed first
      event.preventDefault();
      onclose();
      return;
    }
    trapTab(panel, event);
  }

  /** Focus that ends up outside (lost to <body>, or clicked away) comes back in. */
  function focusin(event: FocusEvent) {
    if (open && panel && event.target instanceof Node && !panel.contains(event.target)) focusInside();
  }

  function pointerdown(event: PointerEvent) {
    pressedOnBackdrop = event.target === event.currentTarget;
    if (pressedOnBackdrop) event.preventDefault(); // keep focus in the dialog
  }

  function backdrop(event: MouseEvent) {
    const close = pressedOnBackdrop && event.target === event.currentTarget && !destructive;
    pressedOnBackdrop = false;
    if (close) onclose();
  }
</script>

<svelte:window onkeydown={keydown} onfocusin={focusin} />

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions (Esc is handled on the window) -->
  <div class="backdrop" use:portal onpointerdown={pointerdown} onclick={backdrop}>
    <div
      bind:this={panel}
      class="panel {size}"
      role={destructive ? 'alertdialog' : 'dialog'}
      aria-modal="true"
      aria-labelledby={labelledby}
      aria-describedby={describedby}
      tabindex="-1"
    >
      {@render children()}
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--hm-space-4);
    background: var(--hm-color-overlay);
  }
  .panel {
    inline-size: 100%;
    max-inline-size: 480px;
    max-block-size: calc(100dvh - 2 * var(--hm-space-4));
    overflow: auto;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    padding: var(--hm-space-6);
    border-radius: var(--hm-radius-xl);
    background: var(--hm-color-surface-raised);
    color: var(--hm-color-text);
    box-shadow: var(--hm-shadow-lg);
    animation: enter var(--hm-motion-duration-base) var(--hm-motion-easing-enter);
  }
  .lg {
    max-inline-size: 600px;
  }
  .panel:focus {
    outline: none;
  }
  @media (max-width: 767px) {
    .backdrop {
      align-items: flex-end;
      padding: 0;
    }
    .panel {
      max-inline-size: none;
      border-end-start-radius: 0;
      border-end-end-radius: 0;
      max-block-size: 90dvh;
    }
  }
  @media (forced-colors: active) {
    .panel {
      border: 2px solid CanvasText;
    }
  }
  @keyframes enter {
    from {
      opacity: 0;
      translate: 0 12px;
    }
  }
</style>
