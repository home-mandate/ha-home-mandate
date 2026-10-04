<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Overview (design README 6.1): status at a glance, first steps without agents, pending
  approvals and the latest decisions. Approvals are answered on the phone; a person who may
  answer one here gets a link to the approvals page (decision F2). A load error says what
  keeps working; it must never look as if protection were off.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { Agent, ApprovalRequest, AuditEntry, DeviceCatalog } from '../api/types.ts';
  import ErrorState from '../components/ErrorState.svelte';
  import ClaimLegend from '../components/ClaimLegend.svelte';
  import Icon from '../components/Icon.svelte';
  import RequestCard from '../components/RequestCard.svelte';
  import ActivityList from '../components/overview/ActivityList.svelte';
  import Onboarding from '../components/overview/Onboarding.svelte';
  import StatusTiles, { type Tile } from '../components/overview/StatusTiles.svelte';
  import { formatDate, formatNumber, formatRelative, formatTime } from '../format.ts';
  import { m } from '../i18n.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { openedText } from '../approvals/live.ts';
  import { Announcer } from '../ui/announcer.svelte.ts';
  import { coalesce, RELOAD_WAIT_MS } from '../ui/coalesce.ts';

  interface Props {
    app: AppState;
    /** Browser clock, ticking. */
    now: number;
  }

  interface Data {
    agents: Agent[];
    open: ApprovalRequest[];
    activity: AuditEntry[] | null;
    catalog: DeviceCatalog | null;
  }

  let { app, now }: Props = $props();

  const ACTIVITY = 5;
  const SEPARATOR = ' · ';
  const id = $props.id();

  const overview = new Loader<Data>(async () => {
    const api = app.api;
    // Agents and approvals are essential; without the audit log or the device names the
    // page still shows what is pending.
    const [agents, approvals, activity, catalog] = await Promise.all([
      api.agents(),
      api.approvals(),
      api
        .audit({ group: 'decision', limit: ACTIVITY })
        .then((page) => page.entries)
        .catch(() => null),
      api.devices().catch(() => null),
    ]);
    return { agents, open: approvals.open, activity, catalog };
  });

  const reload = () => void overview.run();
  // Events come in bursts (a request opens and the log grows): one reload after them.
  const later = coalesce(reload, RELOAD_WAIT_MS);
  /** What screen readers hear about live changes (review L24). */
  const live = new Announcer();

  function opened(request: ApprovalRequest) {
    later.call();
    void live.say(openedText(request));
  }

  onMount(() => {
    const stop = [
      app.on('approval.opened', ({ request }) => opened(request)),
      app.on('approval.closed', later.call),
      app.on('audit.appended', later.call),
      app.on('agents.changed', later.call),
      app.on('reconnected', later.call),
    ];
    reload();
    return () => {
      later.cancel();
      stop.forEach((off) => off());
    };
  });

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const serverNow = $derived(now - app.offsetMs);
  const data = $derived(overview.data);
  const system = $derived(app.system);
  const stopped = $derived(system?.emergency_stop.active ?? false);
  const answerHere = $derived(data?.open.some((r) => r.can_answer) ?? false);

  function areaName(areaId: string | null): string | null {
    if (areaId === null) return null;
    return data?.catalog?.areas.find((a) => a.id === areaId)?.name ?? areaId;
  }

  function haTile(): Tile {
    const ha = system?.ha;
    const since = ha?.since ? new Date(ha.since) : null;
    if (ha?.connected) {
      return { label: m.overview_ha_label(), icon: 'home', value: m.overview_ha_connected(), tone: 'positive',
        hint: since ? m.overview_ha_connected_since({ date: formatDate(since, ctx) }) : '' };
    }
    return { label: m.overview_ha_label(), icon: 'warning', value: m.overview_ha_disconnected(), tone: 'danger',
      hint: since ? m.overview_ha_disconnected_since({ time: formatTime(since, ctx) }) : '' };
  }

  function agentsTile(agents: Agent[]): Tile {
    const approved = agents.filter((a) => a.status === 'active');
    const label = m.overview_agents_label();
    if (approved.length === 0) return { label, icon: 'agent', value: formatNumber(0, ctx), hint: m.overview_agents_none(), tone: 'muted' };
    const acting = stopped ? 0 : approved.filter((a) => a.mandate?.status === 'active').length;
    return { label, icon: 'agent', value: formatNumber(acting, ctx), tone: 'accent',
      hint: m.overview_agents_hint({ count: acting, total: formatNumber(approved.length, ctx) }) };
  }

  const tiles = $derived.by((): Tile[] | null => {
    if (!data || !system) return null;
    const pending = data.open.length;
    return [
      haTile(),
      stopped
        ? { label: m.overview_estop_label(), icon: 'power', value: m.overview_estop_on(), hint: m.overview_estop_on_hint(), tone: 'danger' }
        : { label: m.overview_estop_label(), icon: 'power', value: m.overview_estop_off(), hint: m.overview_estop_off_hint(), tone: 'muted' },
      agentsTile(data.agents),
      { label: m.overview_requests_label(), icon: 'ask', value: formatNumber(pending, ctx), tone: pending > 0 ? 'ask' : 'muted',
        hint: answerHere ? m.overview_requests_hint_ui() : m.overview_requests_hint() },
    ];
  });

  const chainChecked = $derived(
    system?.chain.checked_at ? m.audit_chain_checked({ relative: formatRelative(new Date(system.chain.checked_at), new Date(serverNow), ctx) }) : '',
  );
</script>

<div class="head">
  <h1>{m.overview_title()}</h1>
  <span class="tz">{m.common_timezone_note({ tz: ctx.timeZone })}</span>
</div>

<p class="hm-visually-hidden" role="status">{live.text}</p>

{#if overview.status === 'error'}
  <ErrorState title={m.overview_error_title()} body={m.overview_error_body()} onretry={reload} />
{:else}
  {#if data && data.agents.length === 0}<Onboarding />{/if}
  <StatusTiles {tiles} />

  {#if data}
    <div class="lists">
      <section class="requests" aria-labelledby="{id}-requests">
        <div class="section-head">
          <h2 id="{id}-requests">{m.requests_heading()} <span class="count">{formatNumber(data.open.length, ctx)}</span></h2>
        </div>
        {#if system && !system.ha.connected}
          <p class="note warning"><Icon name="warning" size={16} />{m.requests_ha_down()}</p>
        {/if}
        {#if data.open.length === 0}
          <div class="empty">
            <Icon name="ask" size={32} />
            <strong>{m.requests_empty_title()}</strong>
            <span>{m.requests_empty_body()}</span>
          </div>
        {:else}
          {#each data.open as request (request.id)}
            <RequestCard {request} areaName={areaName(request.area)} offsetMs={app.offsetMs} {ctx}>
              {#snippet children(titleId)}
                {#if request.can_answer}
                  <a class="answer" href={href({ name: 'requests' })} aria-describedby={titleId}>{m.request_answer_link()}</a>
                {/if}
              {/snippet}
            </RequestCard>
          {/each}
          <p class="note"><Icon name="info" size={16} />{answerHere ? m.request_ui_note() : m.request_phone_note()}</p>
        {/if}
      </section>

      <section class="activity" aria-labelledby="{id}-activity">
        <div class="section-head">
          <h2 id="{id}-activity">{m.activity_heading()}</h2>
          <a href={href({ name: 'audit', query: {} })}>{m.activity_open_audit()}<Icon name="chevron" size={16} /></a>
        </div>
        {#if data.activity === null}
          <p class="note">{m.audit_error_title()}</p>
        {:else if data.activity.length === 0}
          <div class="empty">
            <Icon name="list" size={32} />
            <strong>{m.activity_empty_title()}</strong>
            <span>{m.activity_empty_body()}</span>
          </div>
        {:else}
          <ActivityList entries={data.activity} catalog={data.catalog} {ctx} now={serverNow} />
        {/if}
        {#if system?.chain.valid}
          <p class="chain">
            <span class="ok"><Icon name="check" size={16} />{m.audit_chain_ok()}</span>
            <span>{chainChecked ? chainChecked + SEPARATOR : ''}{m.audit_retention()}</span>
          </p>
        {/if}
      </section>
    </div>
    <ClaimLegend />
  {/if}
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hm-space-2);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-3xl);
  }
  .tz,
  .count,
  .note,
  .chain {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .lists {
    display: grid;
    grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    gap: var(--hm-space-5);
  }
  @media (max-width: 1023px) {
    .lists {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  section {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    min-inline-size: 0;
  }
  .section-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hm-space-2);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
  }
  .section-head a,
  .answer {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    color: var(--hm-color-accent-text);
    font-weight: 600;
  }
  .note {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-2);
    margin: 0;
  }
  .warning {
    color: var(--hm-color-warning-fg);
  }
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hm-space-2);
    padding: var(--hm-space-5);
    border: var(--hm-border-width) dashed var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    color: var(--hm-color-text-muted);
    text-align: center;
  }
  .empty strong {
    color: var(--hm-color-text);
  }
  .chain {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--hm-space-2);
    margin: 0;
  }
  .ok {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    color: var(--hm-color-positive-fg);
  }
  .answer,
  .section-head a {
    min-block-size: var(--hm-size-touch);
  }
</style>
