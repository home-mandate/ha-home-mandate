<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Load error. It says what still applies ("existing mandates stay in force"): a load error
  must never look as if the protection were off.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    title: string;
    body: string;
    onretry?: () => void;
  }

  let { title, body, onretry }: Props = $props();
</script>

<section class="error" role="alert">
  <span class="circle"><Icon name="warning" size={32} /></span>
  <h2>{title}</h2>
  <p>{body}</p>
  {#if onretry}<Button variant="secondary" size="lg" icon="refresh" onclick={onretry}>{m.common_retry()}</Button>{/if}
</section>

<style>
  .error {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hm-space-3);
    padding: var(--hm-space-10) var(--hm-space-6);
    border-radius: var(--hm-radius-xl);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border);
    text-align: center;
  }
  .circle {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 72px;
    block-size: 72px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-warning-bg);
    color: var(--hm-color-warning-fg);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
    max-inline-size: 480px;
    font-size: 15px;
    color: var(--hm-color-text-muted);
  }
</style>
