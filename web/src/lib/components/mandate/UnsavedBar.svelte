<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Save bar of the mandate and the template editor (issue #20): while there are unsaved
  changes it stays at the bottom of the viewport with their count, what they mean, Save and
  Discard, in addition to the state in the header. Fixed at the bottom; while it is shown the
  page keeps room of its height at the end and as scroll padding (lib/ui/savebar.ts,
  app.css), so the end of the page stays reachable above it and a focused field never lands
  behind it. Its appearance moves neither the page nor the focus.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { m } from '../../i18n.ts';
  import { reserveRoom } from '../../ui/savebar.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    /** Unsaved changes, at least 1. */
    count: number;
    /** What the changes mean until they are saved. */
    note: string;
    saveLabel: string;
    onsave: () => void;
    ondiscard: () => void;
  }

  let { count, note, saveLabel, onsave, ondiscard }: Props = $props();

  const uid = $props.id();
  let element: HTMLElement | undefined = $state();

  onMount(() => (element ? reserveRoom(element) : undefined));
</script>

<section bind:this={element} class="savebar" aria-labelledby="{uid}-count">
  <p class="text">
    <span class="icon"><Icon name="warning" /></span>
    <span><strong id="{uid}-count">{m.editor_unsaved({ count })}</strong> <span>{note}</span></span>
  </p>
  <div class="actions">
    <Button size="lg" onclick={ondiscard}>{m.unsaved_bar_discard()}</Button>
    <Button variant="primary" size="lg" onclick={onsave}>{saveLabel}</Button>
  </div>
</section>

<style>
  .savebar {
    position: fixed;
    inset-inline: 0;
    inset-block-end: 0;
    z-index: 20;
    box-sizing: border-box;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-2) var(--hm-space-4);
    padding-block: var(--hm-space-3);
    /* Lines up with the centred page content, like the header (issue #7). */
    padding-inline: var(--hm-bar-pad);
    background: var(--hm-color-surface);
    border-block-start: 2px solid var(--hm-color-warning-border);
    box-shadow: var(--hm-shadow-lg);
  }
  .text {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    flex: 1 1 320px;
    min-inline-size: 0;
    margin: 0;
    font-size: 15px;
    overflow-wrap: anywhere;
  }
  .icon {
    display: flex;
    padding-block-start: 2px;
    color: var(--hm-color-warning-fg);
  }
  strong {
    font-weight: var(--hm-font-weight-semibold);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
  @media (max-width: 767px) {
    .savebar {
      padding-block: var(--hm-space-2);
    }
    .text {
      font-size: var(--hm-font-size-sm);
    }
    .actions {
      flex: 1 1 100%;
    }
    .actions :global(.btn) {
      flex: 1 1 0;
      justify-content: center;
    }
  }
  @media (forced-colors: active) {
    .savebar {
      border-block-start: 2px solid CanvasText;
    }
  }
</style>
