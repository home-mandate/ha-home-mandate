<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Toggle chip (aria-pressed) for actions and weekdays; critical actions carry the shield. -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { m } from '../i18n.ts';
  import Icon from './Icon.svelte';
  import SelectionMark from './SelectionMark.svelte';

  interface Props {
    pressed: boolean;
    critical?: boolean;
    onchange?: (pressed: boolean) => void;
    children: Snippet;
  }

  let { pressed = $bindable(), critical = false, onchange, children }: Props = $props();

  function toggle() {
    pressed = !pressed;
    onchange?.(pressed);
  }
</script>

<button type="button" class="chip" aria-pressed={pressed} onclick={toggle}>
  <!-- A check or an x of the same size, so toggling never changes the chip's width. -->
  <SelectionMark on={pressed} />
  <span>{@render children()}</span>
  {#if critical}<span class="critical"><Icon name="critical" size={16} label={m.critical_label()} /></span>{/if}
</button>

<style>
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: var(--hm-size-control);
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-pill);
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
  /* Not bold and no thicker border (issue #47): the border only changes colour and an inset
     ring, which takes no room, thickens it; the check (an x when not pressed) marks it without
     colour. */
  .chip[aria-pressed='true'] {
    background: var(--hm-color-accent-subtle);
    border-color: var(--hm-color-accent);
    box-shadow: inset 0 0 0 var(--hm-border-width) var(--hm-color-accent);
  }
  .critical {
    display: flex;
    color: var(--hm-color-critical-fg);
  }
  @media (forced-colors: active) {
    /* An outline inside the chip: a thicker border would make it wider. */
    .chip[aria-pressed='true']:not(:focus-visible) {
      border-color: Highlight;
      outline: 2px solid Highlight;
      outline-offset: -2px;
    }
  }
</style>
