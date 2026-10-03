<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  An agent's OAuth client identity: a checked domain in mono, or a paired agent's own
  identifier marked "unverified" (ui/identity.ts). Nothing for an identifier that fits neither.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { clientIdentity } from '../ui/identity.ts';

  interface Props {
    client: string;
  }

  let { client }: Props = $props();

  const identity = $derived(clientIdentity(client));
</script>

{#if identity?.verified}<span class="domain">{identity.text}</span
  >{:else if identity}<span class="claimed"><bdi>{identity.text}</bdi> <span class="tag">{m.agent_claim_short()}</span></span
  >{/if}

<style>
  .domain {
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
    overflow-wrap: anywhere;
  }
  .claimed {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
    overflow-wrap: anywhere;
  }
  .tag {
    display: inline-block;
    padding-inline: 6px;
    border-radius: var(--hm-radius-sm);
    border: var(--hm-border-width) dotted var(--hm-color-border-strong);
    font-size: 12px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text-muted);
  }
</style>
