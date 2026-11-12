<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Whether an agent's tokens are DPoP-bound (issue #65): "Bound (DPoP)" when they work only
  with the key that stays with the agent's client, "Unbound" for Bearer tokens. With
  explain, an unbound agent gets the plain words of the list's legend (BindingLegend, #156).
-->
<script lang="ts">
  import type { Agent } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    binding: Agent['binding'];
    compact?: boolean;
    explain?: boolean;
  }

  let { binding, compact = false, explain = false }: Props = $props();
</script>

{#if binding === 'dpop'}
  <StatusPill tone="positive" shape="square" {compact}>{m.agent_binding_dpop()}</StatusPill>
{:else}
  <StatusPill tone="neutral" shape="ring" {compact}>{m.agent_binding_none()}</StatusPill>
  {#if explain}<p class="hint">{m.agent_binding_legend_none()}</p>{/if}
{/if}

<style>
  .hint {
    margin: var(--hm-space-2) 0 0;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
</style>
