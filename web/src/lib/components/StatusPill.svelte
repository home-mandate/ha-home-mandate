<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Status pill: rectangular (radius 6) so it never looks like a round decision badge. The dot
  shape (filled, ring, square) varies in addition to the colour.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    tone: 'positive' | 'neutral' | 'danger' | 'warning' | 'info';
    shape?: 'dot' | 'ring' | 'square';
    compact?: boolean;
    children: Snippet;
  }

  let { tone, shape = 'dot', compact = false, children }: Props = $props();
</script>

<span class="pill {tone}" class:compact><span class="mark {shape}" aria-hidden="true"></span>{@render children()}</span>

<style>
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: 26px;
    padding-inline: 10px;
    border-radius: 6px;
    border: var(--hm-border-width) solid;
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-medium);
    white-space: nowrap;
  }
  .compact {
    min-block-size: 24px;
    padding-inline: var(--hm-space-2);
  }
  .positive {
    color: var(--hm-color-positive-fg);
    background: var(--hm-color-positive-bg);
    border-color: var(--hm-color-allow-border);
  }
  .neutral {
    color: var(--hm-color-text-muted);
    background: var(--hm-color-surface-sunken);
    border-color: var(--hm-color-border);
  }
  .danger {
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-danger-bg);
    border-color: var(--hm-color-danger-border);
  }
  .warning {
    color: var(--hm-color-warning-fg);
    background: var(--hm-color-warning-bg);
    border-color: var(--hm-color-warning-border);
  }
  .info {
    color: var(--hm-color-info-fg);
    background: var(--hm-color-info-bg);
    border-color: var(--hm-color-info-border);
  }
  .mark {
    inline-size: 8px;
    block-size: 8px;
    flex-shrink: 0;
    box-sizing: border-box;
  }
  .dot {
    border-radius: var(--hm-radius-pill);
    background: currentColor;
  }
  .ring {
    border-radius: var(--hm-radius-pill);
    border: 2px solid currentColor;
  }
  .square {
    border-radius: 2px;
    background: currentColor;
  }
</style>
