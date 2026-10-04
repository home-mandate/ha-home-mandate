<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Loading placeholder: plain blocks, no shimmer (design README section 6). -->
<script lang="ts">
  interface Props {
    /** Widths of the lines, e.g. ['70%', '40%']. */
    lines?: string[];
  }

  let { lines = ['70%'] }: Props = $props();
</script>

<span class="skeleton" aria-hidden="true">
  {#each lines as width, i (i)}<span class="line" style:inline-size={width}></span>{/each}
</span>

<style>
  .skeleton {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .line {
    display: block;
    block-size: 14px;
    border-radius: var(--hm-radius-sm);
    background: var(--hm-color-skeleton);
  }
</style>
