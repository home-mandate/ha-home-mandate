<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Save bar of the mandate and the template editor (issue #20): while there are unsaved
  changes it stays at the bottom of the viewport with their count, what they mean, Save and
  Discard, in addition to the state in the header. It sits in the dock below the page
  (App.svelte): while it is shown the page scrolls in its own frame above it, so the bar
  never covers content, a focused field or the end of the page, on desktop and phone.
-->
<script module lang="ts">
  /** Class on the root element while a save bar is shown (app.css, App.svelte). */
  export const SAVEBAR_CLASS = 'hm-savebar';
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import { m } from '../../i18n.ts';
  import { dock, SCROLL_ID } from '../../ui/portal.ts';
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

  // The page moves from the window into its frame and back: it keeps its scroll position.
  onMount(() => {
    const root = document.documentElement;
    const frame = document.getElementById(SCROLL_ID);
    const top = root.scrollTop;
    root.classList.add(SAVEBAR_CLASS);
    if (frame) frame.scrollTop = top;
    return () => {
      const back = frame?.scrollTop ?? 0;
      root.classList.remove(SAVEBAR_CLASS);
      if (frame) root.scrollTop = back;
    };
  });
</script>

<section use:dock class="savebar" aria-labelledby="{uid}-count">
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
