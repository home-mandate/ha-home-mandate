<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Link back to the page above, with the direction icon (mirrored in right-to-left layouts). -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  interface Props {
    href: string;
    children: Snippet;
  }

  let { href, children }: Props = $props();
</script>

<a class="back" {href}><Icon name="back" size={16} />{@render children()}</a>

<style>
  .back {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    min-block-size: var(--hm-size-touch);
    color: var(--hm-color-accent-text);
    font-weight: var(--hm-font-weight-medium);
  }
</style>
