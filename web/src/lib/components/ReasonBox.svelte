<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The reason an agent gives for an approval request: labelled as its unverified claim. -->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import Icon from './Icon.svelte';

  interface Props {
    reason: string;
  }

  let { reason }: Props = $props();
</script>

<figure class="reason">
  <figcaption><Icon name="claim" size={16} />{m.agent_claim_label()} · {m.agent_reason()}</figcaption>
  <blockquote><bdi>{cleanUntrusted(reason)}</bdi></blockquote>
</figure>

<style>
  .reason {
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    padding: 10px var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) dotted var(--hm-color-border-strong);
  }
  figcaption {
    display: flex;
    align-items: center;
    gap: var(--hm-space-1);
    font-size: 12px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text-muted);
  }
  blockquote {
    margin: 0;
    font-size: 15px;
    overflow-wrap: anywhere;
  }
</style>
