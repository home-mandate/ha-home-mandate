<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Status of an agent: active or revoked (decision F3: no "waiting"). Shape and text, not colour alone. -->
<script lang="ts">
  import type { AgentStatus } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    status: AgentStatus;
    compact?: boolean;
  }

  let { status, compact = false }: Props = $props();
</script>

{#if status === 'active'}
  <StatusPill tone="positive" {compact}>{m.status_active()}</StatusPill>
{:else}
  <StatusPill tone="neutral" shape="square" {compact}>{m.status_revoked()}</StatusPill>
{/if}
