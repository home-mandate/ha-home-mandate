<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One audit entry in the list (design README 6.8): time and number, agent or event, device
  and action, decision and result. Administrative events have their icon and no decision
  columns. An entry from the first broken one on carries a warning mark and a red edge.
-->
<script lang="ts">
  import type { AuditEntry, DeviceCatalog } from '../../api/types.ts';
  import { approverText, decisionOf, deviceName, directoryText, templateText } from '../../audit/describe.ts';
  import { eventLabel, outcomeOf } from '../../audit/outcome.ts';
  import { formatTime, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { actionLabel } from '../../mandate/labels.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import AgentName from '../AgentName.svelte';
  import DecisionBadge from '../DecisionBadge.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    entry: AuditEntry;
    catalog: DeviceCatalog | null;
    ctx: FormatContext;
    href: string;
    current: boolean;
    broken: boolean;
    onselect?: (event: MouseEvent) => void;
  }

  let { entry, catalog, ctx, href, current, broken, onselect }: Props = $props();

  const SEPARATOR = ' · ';

  const decision = $derived(decisionOf(entry));
  const outcome = $derived(outcomeOf(entry));
  const what = $derived.by(() => {
    const request = entry.request;
    if (request) return deviceName(request.resource.entity_id, catalog) + SEPARATOR + actionLabel(request.resource.category, request.action);
    if (entry.directory) return directoryText(entry, catalog);
    if (entry.template) return templateText(entry);
    if (entry.approver) return approverText(entry);
    if (entry.actor?.name) return cleanUntrusted(entry.actor.name);
    return entry.agent ? cleanUntrusted(entry.agent.display_name ?? entry.agent.client_id) : '';
  });
</script>

<a class="row" class:broken {href} data-seq={entry.seq} aria-current={current ? 'true' : undefined} onclick={onselect}>
  <span class="when">
    <time datetime={entry.recorded_at}>{formatTime(new Date(entry.recorded_at), ctx)}</time>
    <span class="seq">{m.audit_seq_short({ number: entry.seq })}</span>
  </span>
  <span class="who">
    <span class="line">
      {#if broken}<span class="mark"><Icon name="warning" size={16} label={m.audit_entry_broken()} /></span>{/if}
      {#if entry.event === 'decision' && entry.agent}
        <AgentName name={entry.agent.display_name ?? entry.agent.client_id} />
      {:else}
        <span class="event"><Icon name="history" size={16} />{eventLabel(entry.event)}</span>
      {/if}
    </span>
    {#if what}<span class="what"><bdi>{what}</bdi></span>{/if}
  </span>
  {#if entry.event === 'decision'}
    <span class="decision">{#if decision}<DecisionBadge kind={decision} size="sm" />{/if}</span>
    <span class="result {outcome?.tone}">
      {#if outcome}<span>{outcome.text}</span>{#if outcome.why}<span class="why">{outcome.why}</span>{/if}{/if}
    </span>
  {/if}
</a>

<style>
  .row {
    display: grid;
    grid-template-columns: 58px minmax(0, 1.6fr) minmax(0, 1fr) minmax(0, 1.1fr);
    gap: var(--hm-space-3);
    align-items: start;
    padding: var(--hm-space-3);
    border-inline-start: 3px solid transparent;
    color: inherit;
    text-decoration: none;
    /* the sticky "new entries" pill never covers a focused row */
    scroll-margin-block-start: 64px;
  }
  .row:hover {
    background: var(--hm-color-surface-hover);
  }
  .row:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: -2px;
  }
  .row[aria-current='true'] {
    background: var(--hm-color-accent-subtle);
    border-inline-start-color: var(--hm-color-accent);
  }
  @media (forced-colors: active) {
    .row[aria-current='true'] {
      border-inline-start-color: Highlight;
      outline: 2px solid Highlight;
      outline-offset: -2px;
    }
  }
  .row.broken {
    border-inline-start-color: var(--hm-color-danger-fg);
  }
  @media (max-width: 767px) {
    .row {
      grid-template-columns: 52px minmax(0, 1fr);
    }
    .decision,
    .result {
      grid-column: 2;
    }
  }
  .when {
    display: flex;
    flex-direction: column;
    font-variant-numeric: tabular-nums;
  }
  .seq,
  .what,
  .why {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .who {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-inline-size: 0;
    overflow-wrap: anywhere;
  }
  .line,
  .event {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
  }
  .event {
    font-weight: var(--hm-font-weight-medium);
  }
  .mark {
    display: inline-flex;
    color: var(--hm-color-danger-fg);
  }
  .result {
    display: flex;
    flex-direction: column;
  }
  .result.positive {
    color: var(--hm-color-positive-fg);
  }
  .result.danger {
    color: var(--hm-color-danger-fg);
  }
  .result.warning {
    color: var(--hm-color-warning-fg);
  }
  .result.ask {
    color: var(--hm-color-ask-fg);
  }
</style>
