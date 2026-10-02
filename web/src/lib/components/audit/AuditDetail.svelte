<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Details of an audit entry (design README 6.8): the decision and what happened, then who,
  what, why in plain words with the code, the rule, the mandate version, who answered an
  approval (where and how fast) and the duration; technical details folded.
-->
<script lang="ts">
  import type { AuditEntry, DeviceCatalog } from '../../api/types.ts';
  import { decisionOf, deviceName } from '../../audit/describe.ts';
  import { answeredAfter, approvalText, eventLabel, outcomeOf, reasonText } from '../../audit/outcome.ts';
  import { formatDateTime, formatNumber, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { actionLabel } from '../../mandate/labels.ts';
  import { href } from '../../router.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import AgentName from '../AgentName.svelte';
  import DecisionBadge from '../DecisionBadge.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    entry: AuditEntry;
    catalog: DeviceCatalog | null;
    ctx: FormatContext;
    broken: boolean;
    headingId: string;
    /** 1 on the entry's own page, 2 next to the list. */
    level?: 1 | 2;
  }

  let { entry, catalog, ctx, broken, headingId, level = 2 }: Props = $props();

  const SEPARATOR = ' · ';
  const NONE = '–';
  const MS_PER_S = 1000;

  const decision = $derived(decisionOf(entry));
  const outcome = $derived(outcomeOf(entry));
  const approval = $derived.by(() => {
    const text = approvalText(entry);
    const at = entry.approval?.at;
    const asked = entry.request?.time;
    return text && at && asked ? text + SEPARATOR + m.request_answered_after({ duration: answeredAfter(asked, at) }) : text;
  });
  const duration = $derived.by(() => {
    const ms = entry.result?.duration_ms;
    if (ms === undefined) return null;
    return ms < MS_PER_S
      ? formatNumber(ms, ctx, { style: 'unit', unit: 'millisecond', unitDisplay: 'short' })
      : formatNumber(ms / MS_PER_S, ctx, { style: 'unit', unit: 'second', unitDisplay: 'short', maximumFractionDigits: 1 });
  });
  const actor = $derived(entry.actor ? cleanUntrusted(entry.actor.name ?? entry.actor.id) : null);
</script>

<div class="head">
  <svelte:element this={level === 1 ? 'h1' : 'h2'} id={headingId} class="title" tabindex="-1">{m.audit_detail_title({ number: entry.seq })}</svelte:element>
  <span class="when">{formatDateTime(new Date(entry.recorded_at), ctx)}</span>
</div>

{#if broken}<p class="broken"><Icon name="warning" size={16} />{m.audit_entry_broken()}</p>{/if}

<div class="summary">
  {#if decision}<DecisionBadge kind={decision} />{:else}<span class="event"><Icon name="history" />{eventLabel(entry.event)}</span>{/if}
  {#if outcome}<span class="outcome {outcome.tone}">{outcome.text}{#if outcome.why}{SEPARATOR}{outcome.why}{/if}</span>{/if}
</div>

<dl>
  {#if actor && entry.event !== 'decision'}
    <dt>{m.audit_field_actor()}</dt>
    <dd><bdi>{actor}</bdi></dd>
  {/if}
  {#if entry.agent}
    <dt>{m.audit_field_agent()}</dt>
    <dd><AgentName name={entry.agent.display_name ?? entry.agent.client_id} client={entry.agent.client_id} /></dd>
  {/if}
  {#if entry.request}
    <dt>{m.audit_field_device()}</dt>
    <dd><bdi>{deviceName(entry.request.resource.entity_id, catalog)}</bdi><code>{cleanUntrusted(entry.request.resource.entity_id)}</code></dd>
    <dt>{m.audit_field_action()}</dt>
    <dd>{actionLabel(entry.request.resource.category, entry.request.action)}</dd>
  {/if}
  {#if entry.evaluation}
    <dt>{m.audit_reason_label()}</dt>
    <dd>{reasonText(entry.evaluation.reason)}</dd>
    {#if entry.evaluation.rule_id}
      <dt>{m.audit_rule()}</dt>
      <dd><code>{cleanUntrusted(entry.evaluation.rule_id)}</code></dd>
    {/if}
  {/if}
  {#if entry.mandate}
    <dt>{m.audit_mandate_version()}</dt>
    <dd>
      <a href={href({ name: 'mandate_versions', id: entry.mandate.id })}
        >{entry.mandate.version ? m.version_label({ version: entry.mandate.version }) : m.versions_title()}</a
      >
    </dd>
  {/if}
  {#if approval}
    <dt>{m.audit_approval()}</dt>
    <dd>{approval}</dd>
  {/if}
  {#if duration}
    <dt>{m.audit_duration()}</dt>
    <dd>{duration}</dd>
  {/if}
</dl>

<details>
  <summary>{m.audit_technical()}</summary>
  <dl>
    <dt>{m.audit_seq()}</dt>
    <dd><code>{entry.seq}</code></dd>
    <dt>{m.audit_hash()}</dt>
    <dd><code>{entry.digest}</code></dd>
    <dt>{m.audit_prev_hash()}</dt>
    <dd><code>{entry.prev ?? NONE}</code></dd>
    {#if entry.evaluation}
      <dt>{m.audit_code()}</dt>
      <dd><code>{cleanUntrusted(entry.evaluation.reason)}</code></dd>
    {/if}
  </dl>
</details>

<style>
  .head {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .title {
    margin: 0;
    font-size: var(--hm-font-size-xl);
  }
  .title:focus {
    outline: none;
  }
  .when {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .broken {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-danger-bg);
    color: var(--hm-color-danger-fg);
  }
  .summary {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2);
  }
  .event {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    font-weight: 600;
  }
  .outcome.positive {
    color: var(--hm-color-positive-fg);
  }
  .outcome.danger {
    color: var(--hm-color-danger-fg);
  }
  .outcome.warning {
    color: var(--hm-color-warning-fg);
  }
  .outcome.ask {
    color: var(--hm-color-ask-fg);
  }
  dl {
    display: grid;
    grid-template-columns: minmax(0, auto) minmax(0, 1fr);
    gap: var(--hm-space-2) var(--hm-space-3);
    margin: 0;
  }
  dt {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  dd {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin: 0;
    overflow-wrap: anywhere;
  }
  code {
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  summary {
    cursor: pointer;
    min-block-size: var(--hm-size-touch);
    display: flex;
    align-items: center;
    font-weight: var(--hm-font-weight-medium);
  }
  details dl {
    margin-block-start: var(--hm-space-2);
  }
</style>
