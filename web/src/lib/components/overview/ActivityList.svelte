<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Recent activity (design README 6.1): the latest decisions with badge, critical mark, the
  agent's name as its claim, device and action, and the time relative (absolute on hover).
-->
<script lang="ts">
  import type { AuditEntry, DeviceCatalog } from '../../api/types.ts';
  import { decisionOf, deviceName, isCriticalRequest } from '../../audit/describe.ts';
  import { formatDateTime, formatRelative, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { actionLabel } from '../../mandate/labels.ts';
  import AgentName from '../AgentName.svelte';
  import DecisionBadge from '../DecisionBadge.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    entries: AuditEntry[];
    catalog: DeviceCatalog | null;
    ctx: FormatContext;
    /** Server time in ms, for relative times. */
    now: number;
    /** False on an agent's own page, where every entry is that agent's. */
    showAgent?: boolean;
  }

  let { entries, catalog, ctx, now, showAgent = true }: Props = $props();

  const SEPARATOR = ' · ';

  function what(entry: AuditEntry): string {
    const request = entry.request;
    if (!request) return '';
    return deviceName(request.resource.entity_id, catalog) + SEPARATOR + actionLabel(request.resource.category, request.action);
  }
</script>

<ul role="list">
  {#each entries as entry (entry.seq)}
    {@const decision = decisionOf(entry)}
    <li>
      <span class="badge">
        {#if decision}<DecisionBadge kind={decision} size="sm" />{/if}
        {#if isCriticalRequest(entry)}<span class="critical" title={m.critical_label()}><Icon name="critical" size={16} label={m.critical_label()} /></span>{/if}
      </span>
      <span class="what">
        {#if showAgent && entry.agent}<AgentName name={entry.agent.display_name ?? entry.agent.client_id} />{SEPARATOR}{/if}{what(entry)}
      </span>
      <time datetime={entry.recorded_at} title={formatDateTime(new Date(entry.recorded_at), ctx)}
        >{formatRelative(new Date(entry.recorded_at), new Date(now), ctx)}</time
      >
    </li>
  {/each}
</ul>

<style>
  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--hm-space-3);
    padding-block: var(--hm-space-2);
  }
  li + li {
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  @media (max-width: 767px) {
    li {
      grid-template-columns: minmax(0, 1fr) auto;
    }
    .what {
      grid-column: 1 / -1;
    }
  }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
  }
  .critical {
    display: inline-flex;
    color: var(--hm-color-danger-fg);
  }
  .what {
    min-inline-size: 0;
    overflow-wrap: anywhere;
  }
  time {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
    white-space: nowrap;
  }
</style>
