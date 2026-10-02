<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Status of a mandate as a pill (rectangular, kept apart from the round decision badges):
  active is a filled dot, not yet valid and expired are rings, revoked is a square.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import type { EffectiveStatus } from '../../mandate/dates.ts';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    status: EffectiveStatus;
    compact?: boolean;
  }

  let { status, compact = false }: Props = $props();

  const LOOK = {
    active: { tone: 'positive', shape: 'dot', label: () => m.status_active() },
    not_yet_valid: { tone: 'info', shape: 'ring', label: () => m.status_not_yet_valid() },
    expired: { tone: 'neutral', shape: 'ring', label: () => m.status_expired() },
    revoked: { tone: 'neutral', shape: 'square', label: () => m.status_revoked() },
  } as const;

  const look = $derived(LOOK[status]);
</script>

<StatusPill tone={look.tone} shape={look.shape} {compact}>{look.label()}</StatusPill>
