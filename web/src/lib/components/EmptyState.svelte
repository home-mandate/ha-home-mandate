<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Empty state: icon in a circle, a title, one sentence on the consequence ("without a
  mandate everything is forbidden"), at most one primary action. No illustrations.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    icon: IconName;
    title: string;
    body: string;
    action?: Snippet;
  }

  let { icon, title, body, action }: Props = $props();
</script>

<section class="empty">
  <span class="circle"><Icon name={icon} size={32} /></span>
  <h2>{title}</h2>
  <p>{body}</p>
  {#if action}{@render action()}{/if}
</section>

<style>
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hm-space-3);
    padding: var(--hm-space-10) var(--hm-space-6);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) dashed var(--hm-color-border);
    text-align: center;
  }
  .circle {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 72px;
    block-size: 72px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-muted);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
    line-height: var(--hm-line-height-tight);
  }
  p {
    margin: 0;
    max-inline-size: 480px;
    font-size: 15px;
    color: var(--hm-color-text-muted);
  }
</style>
