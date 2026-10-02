<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  An open approval request (design README 6.1 and 6.9): who wants what, the agent's name
  marked as its claim with the checked client identity next to it, the reason as the
  agent's claim, whom it reached and the countdown on the server clock. Actions (a link or
  the answer buttons) come from the page as children.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { ApprovalRequest } from '../api/types.ts';
  import { formatList, formatTime, type FormatContext } from '../format.ts';
  import { m } from '../i18n.ts';
  import { actionLabel } from '../mandate/labels.ts';
  import { clientIdentity } from '../ui/identity.ts';
  import { MARK, around } from '../ui/sentence.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import AgentName from './AgentName.svelte';
  import Countdown from './Countdown.svelte';
  import DecisionBadge from './DecisionBadge.svelte';
  import ReasonBox from './ReasonBox.svelte';

  interface Props {
    request: ApprovalRequest;
    /** Name of the request's area, already resolved; null without area. */
    areaName: string | null;
    /** Browser clock minus server clock in ms. */
    offsetMs: number;
    ctx: FormatContext;
    now?: () => number;
    children?: Snippet;
  }

  let { request, areaName, offsetMs, ctx, now = Date.now, children }: Props = $props();

  const SEPARATOR = ' · ';

  const title = $derived(
    around(m.request_title({ agent: MARK, action: actionLabel(undefined, request.action), device: cleanUntrusted(request.device_name) })),
  );
  const identity = $derived(clientIdentity(request.agent.client_id));
  const time = $derived(formatTime(new Date(request.created_at), ctx));
  const where = $derived(areaName ? cleanUntrusted(areaName) + SEPARATOR + time : time);
  const total = $derived(Math.max(1, Math.round((Date.parse(request.expires_at) - Date.parse(request.created_at)) / 1000)));
  const recipients = $derived(formatList(request.recipients.map((r) => cleanUntrusted(r)), ctx));
</script>

<article class="card">
  <div class="body">
    <div class="meta">
      <DecisionBadge kind="ask" size="sm" />
      {#if request.critical}<DecisionBadge kind="critical" size="sm" />{/if}
      <span class="where">{where}</span>
    </div>
    <h3>{title[0]}<AgentName name={request.agent.display_name} />{title[1]}</h3>
    {#if identity}
      <div class="client">
        <span>{m.agent_client_id()}</span>
        {#if identity.verified}<code>{identity.text}</code>{:else}<bdi>{identity.text}</bdi>
          <span class="tag">{m.agent_claim_short()}</span>{/if}
      </div>
    {/if}
    {#if request.reason}<ReasonBox reason={request.reason} />{/if}
    {#if request.recipients.length > 0}<span class="sent">{m.request_sent_to({ names: recipients })}</span>{/if}
    {#if children}<div class="actions">{@render children()}</div>{/if}
  </div>
  <Countdown expiresAt={request.expires_at} totalSeconds={total} {offsetMs} size="lg" {now} />
</article>

<style>
  .card {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--hm-space-4);
    padding: var(--hm-space-4);
    border: var(--hm-border-width) solid var(--hm-color-ask-border);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
  }
  @media (max-width: 767px) {
    .card {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    min-inline-size: 0;
  }
  .meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2);
  }
  .where,
  .sent,
  .client {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .client {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hm-space-2);
  }
  code {
    font-family: var(--hm-font-mono);
    color: var(--hm-color-text);
    overflow-wrap: anywhere;
  }
  .tag {
    font-size: var(--hm-font-size-xs);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
    margin-block-start: var(--hm-space-1);
  }
</style>
