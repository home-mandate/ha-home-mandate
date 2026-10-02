<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Button of the design (Components "Buttons"): primary, secondary, text and danger. Disabled
  buttons use aria-disabled and stay focusable, so screen readers reach them and hear why;
  clicks are ignored while disabled or busy.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import { m } from '../i18n.ts';
  import Icon, { type IconName } from './Icon.svelte';
  import Spinner from './Spinner.svelte';

  interface Props extends Omit<HTMLButtonAttributes, 'disabled'> {
    variant?: 'primary' | 'secondary' | 'text' | 'danger';
    /** md: 40 px (desktop controls), lg: 44 px (dialogs, touch). */
    size?: 'md' | 'lg';
    disabled?: boolean;
    busy?: boolean;
    icon?: IconName;
    children: Snippet;
    element?: HTMLButtonElement;
  }

  let {
    variant = 'secondary',
    size = 'md',
    disabled = false,
    busy = false,
    icon,
    type = 'button',
    onclick,
    children,
    element = $bindable(),
    class: extra = '',
    ...rest
  }: Props = $props();

  function click(event: MouseEvent & { currentTarget: EventTarget & HTMLButtonElement }) {
    if (disabled || busy) {
      event.preventDefault();
      return;
    }
    onclick?.(event);
  }
</script>

<button
  bind:this={element}
  {...rest}
  {type}
  class="btn {variant} {size} {extra}"
  aria-disabled={disabled || busy ? 'true' : undefined}
  aria-busy={busy ? 'true' : undefined}
  onclick={click}
>
  {#if busy}
    <Spinner />
    <span>{m.common_loading()}</span>
  {:else}
    {#if icon}<Icon name={icon} />{/if}
    <span>{@render children()}</span>
  {/if}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-control);
    padding-block: 0;
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid transparent;
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    text-align: start;
    cursor: pointer;
  }
  .lg {
    min-block-size: var(--hm-size-touch);
  }
  .primary {
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .primary:hover {
    background: var(--hm-color-accent-hover);
  }
  .primary:active {
    background: var(--hm-color-accent-pressed);
  }
  .secondary {
    background: var(--hm-color-surface);
    color: var(--hm-color-text);
    border-color: var(--hm-color-border-strong);
  }
  .secondary:hover {
    background: var(--hm-color-surface-hover);
  }
  .secondary:active {
    background: var(--hm-color-surface-pressed);
  }
  .text {
    background: transparent;
    color: var(--hm-color-accent-text);
  }
  .text:hover {
    background: var(--hm-color-surface-hover);
  }
  .text:active {
    background: var(--hm-color-surface-pressed);
  }
  .danger {
    background: var(--hm-color-danger-solid);
    color: var(--hm-color-on-danger);
  }
  .danger:hover,
  .danger:active {
    background: var(--hm-color-danger-solid-hover);
  }
  .btn:active {
    box-shadow: inset 0 1px 2px rgb(0 0 0 / 0.2);
  }
  .btn[aria-disabled='true'][aria-busy='true'] {
    cursor: progress;
  }
  .btn[aria-disabled='true']:not([aria-busy='true']) {
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-disabled);
    border-color: var(--hm-color-border);
    box-shadow: none;
    cursor: not-allowed;
  }
  .text[aria-disabled='true']:not([aria-busy='true']) {
    background: transparent;
    border-color: transparent;
  }
  @media (forced-colors: active) {
    .btn {
      border-color: ButtonText;
    }
    .btn[aria-disabled='true']:not([aria-busy='true']) {
      border-color: GrayText;
      color: GrayText;
    }
  }
</style>
