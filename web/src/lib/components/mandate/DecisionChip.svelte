<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  A chip in the decision language with its own text (template cards, tally of the preview).
  Like every decision mark it is never colour alone: icon shape, word and border style.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { CellDecision } from '../../engine/analysis.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    kind: CellDecision;
    children: Snippet;
  }

  let { kind, children }: Props = $props();
</script>

<span class="chip {kind}"><Icon name={kind} size={16} /><span class="text">{@render children()}</span></span>

<style>
  .chip {
    display: inline-flex;
    align-items: flex-start;
    gap: 5px;
    padding-block: 1px;
    padding-inline: 6px 9px;
    border-radius: var(--hm-radius-lg);
    border: var(--hm-border-width) solid;
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-medium);
    line-height: var(--hm-line-height-normal);
  }
  .chip :global(svg) {
    flex-shrink: 0;
    margin-block-start: 2px;
  }
  .text {
    overflow-wrap: anywhere;
  }
  .allow {
    color: var(--hm-color-allow-fg);
    background: var(--hm-color-allow-bg);
    border-color: var(--hm-color-allow-border);
  }
  .ask {
    color: var(--hm-color-ask-fg);
    background: var(--hm-color-ask-bg);
    border-color: var(--hm-color-ask-border);
  }
  .deny {
    color: var(--hm-color-deny-fg);
    background: var(--hm-color-deny-bg);
    border-color: var(--hm-color-deny-border);
  }
  .default {
    color: var(--hm-color-default-fg);
    background: var(--hm-color-default-bg);
    border-color: var(--hm-color-default-border);
    border-style: dashed;
  }
</style>
