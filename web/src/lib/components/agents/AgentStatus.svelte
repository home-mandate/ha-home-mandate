<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Status of an agent: active or revoked (decision F3: no "waiting"); a revoked agent removed
  from the lists says so (#21), an active one without a valid token says that it is not
  signed in (after an emergency stop until it is reconnected, #22). Shape and text, not
  colour alone.
-->
<script lang="ts">
  import type { AgentStatus } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    status: AgentStatus;
    /** The agent holds a valid token; only shown for an active agent. */
    connected?: boolean;
    removed?: boolean;
    compact?: boolean;
  }

  let { status, connected = true, removed = false, compact = false }: Props = $props();
</script>

{#if status === 'active' && connected}
  <StatusPill tone="positive" {compact}>{m.status_active()}</StatusPill>
{:else if status === 'active'}
  <StatusPill tone="warning" shape="ring" {compact}>{m.status_signed_out()}</StatusPill>
{:else if removed}
  <StatusPill tone="neutral" shape="ring" {compact}>{m.status_removed()}</StatusPill>
{:else}
  <StatusPill tone="neutral" shape="square" {compact}>{m.status_revoked()}</StatusPill>
{/if}
