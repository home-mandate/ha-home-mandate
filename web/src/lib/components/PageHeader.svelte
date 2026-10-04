<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Title row of a page: h1 and optional meta (e.g. the time zone note) at the inline end. -->
<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    meta?: Snippet;
  }

  let { title, meta }: Props = $props();
</script>

<div class="title-row">
  <h1>{title}</h1>
  {#if meta}<span class="meta">{@render meta()}</span>{/if}
</div>

<style>
  .title-row {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hm-space-1) var(--hm-space-4);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .meta {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
</style>
