<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One agent (design README 6.2, view=detail; decisions G3, G5, G7): identity, mandate,
  activity with its latest log entries, and "End access". A revoked agent shows who
  revoked it and when, and offers to remove it from the lists (#21); a removed one offers
  nothing. An active agent without a valid token (after an emergency stop) says how to
  reconnect it or clean it up (#22). An unknown ID looks like any page
  that does not exist. The cards are named groups, not landmarks: five regions on one page
  would crowd the landmark list.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { Agent, AuditEntry, DeviceCatalog, Template } from '../api/types.ts';
  import AgentName from '../components/AgentName.svelte';
  import BackLink from '../components/BackLink.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import ClientIdentity from '../components/ClientIdentity.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import FullPageState from '../components/FullPageState.svelte';
  import Icon from '../components/Icon.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import AgentMandate from '../components/agents/AgentMandate.svelte';
  import AgentStatus from '../components/agents/AgentStatus.svelte';
  import RevokeDialog from '../components/agents/RevokeDialog.svelte';
  import RemoveDialog from '../components/RemoveDialog.svelte';
  import ActivityList from '../components/overview/ActivityList.svelte';
  import { formatDateTime, formatNumber, formatRelative } from '../format.ts';
  import { m } from '../i18n.ts';
  import { offeredTemplates } from '../mandate/template.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { MARK } from '../ui/sentence.ts';
  import { cleanUntrusted, isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
    /** client_id from the route. */
    id: string;
    /** Browser clock, ticking. */
    now: number;
  }

  interface Data {
    agent: Agent | null;
    templates: Template[];
    log: AuditEntry[];
    catalog: DeviceCatalog | null;
  }

  let { app, id, now }: Props = $props();

  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const LOG_ENTRIES = 5;
  /** Redirect addresses come from the agent's metadata: cleaned, cut and at most this many shown. */
  const REDIRECTS_SHOWN = 10;
  const REDIRECT_MAX = 200;
  const PAIRED = 'pair:';

  const uid = $props.id();
  const data = new Loader<Data>(async () => {
    const api = app.api;
    const [agents, templates, log, catalog] = await Promise.all([
      api.agents(),
      offeredTemplates(api).catch((): Template[] => []),
      api.audit({ agent: id, limit: LOG_ENTRIES }).then((p) => p.entries).catch((): AuditEntry[] => []),
      api.devices().catch(() => null),
    ]);
    return { agent: agents.find((a) => a.client_id === id) ?? null, templates, log, catalog };
  });

  let revoking = $state(false);
  let busy = $state(false);
  let error = $state('');
  let endButton: HTMLButtonElement | undefined = $state();
  let removing = $state(false);
  let removeButton: HTMLButtonElement | undefined = $state();

  const reload = () => void data.run();
  onMount(() => {
    const stop = [
      app.on('agents.changed', reload),
      app.on('mandates.changed', reload),
      app.on('templates.changed', reload),
      app.on('audit.appended', reload),
      app.on('reconnected', reload),
    ];
    reload();
    return () => stop.forEach((off) => off());
  });

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const serverNow = $derived(now - app.offsetMs);
  const agent = $derived(data.data?.agent ?? null);

  function when(at: string | null): string {
    return at === null ? m.agents_never_active() : formatRelative(new Date(at), new Date(serverNow), ctx);
  }

  /** revoked shows the revoked agent at once (the banner says so) and moves the focus to the heading. */
  async function revoked(result: Agent) {
    const current = data.data;
    if (current) data.set({ ...current, agent: result });
    revoking = false;
    await tick();
    document.getElementById(`${uid}-title`)?.focus();
    void data.run();
  }

  /** remove: revoke and remove the agent and its mandates in one step (#21). */
  async function revoke(remove = false) {
    const target = agent;
    if (busy || !target) return;
    busy = true;
    error = '';
    try {
      const result = remove
        ? await app.api.removeAgent({ client_id: target.client_id, revoke: true, mandates: true })
        : await app.api.revokeAgent(target.client_id);
      if (result.status !== 'revoked') throw new Error('not revoked');
      busy = false;
      await revoked(result);
    } catch {
      // The answer may be lost although the revoke went through: ask for the real state.
      const now = await app.api
        .agents()
        .then((list) => list.find((a) => a.client_id === target.client_id) ?? null)
        .catch(() => null);
      busy = false;
      if (now?.status === 'revoked') await revoked(now);
      else error = m.revoke_failed();
    }
  }

  function closeDialog() {
    if (busy) return;
    revoking = false;
    error = '';
    endButton?.focus();
  }

  /** remove takes a revoked agent, and with mandates its revoked mandates, off the lists (#21). */
  async function remove(mandates: boolean) {
    const target = agent;
    if (busy || !target) return;
    busy = true;
    error = '';
    try {
      const result = await app.api.removeAgent({ client_id: target.client_id, mandates });
      busy = false;
      removing = false;
      await revoked(result);
    } catch {
      busy = false;
      error = m.remove_failed();
      void data.run();
    }
  }

  function closeRemove() {
    if (busy) return;
    removing = false;
    error = '';
    removeButton?.focus();
  }
</script>

<BackLink href={href({ name: 'agents' })} label={m.agents_title()} />

{#if data.status === 'error'}
  <ErrorState title={m.agents_error_title()} body={m.agents_error_body()} onretry={reload} />
{:else if !data.data}
  <Skeleton lines={['40%', '70%', '55%']} />
{:else if !agent}
  <FullPageState icon="search" badge="info" title={m.notfound_title()} body={m.notfound_body()} cta={{ href: href({ name: 'agents' }), label: m.agents_title() }} />
{:else}
  <div class="head">
    <h1 id="{uid}-title" tabindex="-1"><AgentName name={agent.display_name} /></h1>
    <AgentStatus status={agent.status} connected={agent.connected} removed={agent.removed_at !== null} />
  </div>

  {#if agent.removed_at !== null}
    <Banner
      kind="info"
      body={agent.removed_by_name
        ? m.agent_removed_banner({ date: formatDateTime(new Date(agent.removed_at), ctx), admin: isolate(agent.removed_by_name) })
        : m.agent_removed_banner_system({ date: formatDateTime(new Date(agent.removed_at), ctx) })}
    />
  {:else if agent.status === 'revoked'}
    <Banner
      kind="info"
      body={m.agent_revoked_banner({
        date: agent.revoked_at ? formatDateTime(new Date(agent.revoked_at), ctx) : '',
        admin: isolate(agent.revoked_by_name ?? ''),
      })}
    />
  {:else if !agent.connected}
    <Banner kind="info" title={m.agent_signed_out_title()} body={m.agent_signed_out_body()} />
  {/if}

  <div class="grid">
    <div class="column">
      <div class="card" role="group" aria-labelledby="{uid}-identity">
        <h2 id="{uid}-identity">{m.agent_detail_identity()}</h2>
        <dl>
          <dt>{m.agents_col_client()}</dt>
          <dd><ClientIdentity client={agent.oauth_client} verified={agent.client_verified} /></dd>
          <dt>{m.agent_detail_way()}</dt>
          <dd>{agent.client_id.startsWith(PAIRED) ? m.agent_way_code() : m.agent_way_browser()}</dd>
          {#if agent.redirect_uris.length > 0}
            <dt>{m.agent_detail_redirect()}</dt>
            <dd class="mono">
              {#each agent.redirect_uris.slice(0, REDIRECTS_SHOWN) as uri, i (i)}<bdi title={cleanUntrusted(uri)}>{cleanUntrusted(uri, REDIRECT_MAX)}</bdi><br />{/each}
              {#if agent.redirect_uris.length > REDIRECTS_SHOWN}<span class="muted">+{formatNumber(agent.redirect_uris.length - REDIRECTS_SHOWN, ctx)}</span>{/if}
            </dd>
          {/if}
          <dt>{m.agent_detail_approved()}</dt>
          <dd>{m.agent_detail_approved_value({ date: formatDateTime(new Date(agent.created_at), ctx), admin: isolate(agent.created_by_name ?? agent.created_by) })}</dd>
        </dl>
      </div>
      <div class="card">
        <AgentMandate api={app.api} {agent} templates={data.data.templates} catalog={data.data.catalog ?? NO_CATALOG} {ctx} headingId="{uid}-mandate" />
      </div>
    </div>

    <div class="column">
      <div class="card" role="group" aria-labelledby="{uid}-activity">
        <h2 id="{uid}-activity">{m.agent_detail_last()}</h2>
        <p class="big">
          {#if agent.last_active_at}<time datetime={agent.last_active_at} title={formatDateTime(new Date(agent.last_active_at), ctx)}
              >{when(agent.last_active_at)}</time
            >{:else}{when(null)}{/if}
        </p>
        <p class="muted">{m.agent_requests_today({ count: agent.requests_today })}</p>
      </div>
      <div class="card" role="group" aria-labelledby="{uid}-log">
        <div class="log-head">
          <h2 id="{uid}-log">{m.agent_detail_log()}</h2>
          <a href={href({ name: 'audit', query: { agent: [agent.client_id] } })}>{m.common_show_all()}<Icon name="chevron" size={16} /></a>
        </div>
        {#if data.data.log.length > 0}
          <ActivityList entries={data.data.log} catalog={data.data.catalog} {ctx} now={serverNow} showAgent={false} />
        {:else}
          <p class="muted">{m.agents_never_active()}</p>
        {/if}
      </div>
    </div>
  </div>

  {#if agent.status === 'active'}
    <div class="card end" role="group" aria-labelledby="{uid}-end">
      <h2 id="{uid}-end">{m.agent_detail_end()}</h2>
      <p class="muted">{m.agent_detail_end_desc()}</p>
      <Button variant="danger" bind:element={endButton} onclick={() => (revoking = true)}>{m.revoke_button()}</Button>
    </div>
    <RevokeDialog open={revoking} name={agent.display_name} {busy} {error} onclose={closeDialog} onrevoke={(r) => void revoke(r)} />
  {:else if agent.removed_at === null}
    <div class="card end" role="group" aria-labelledby="{uid}-remove">
      <h2 id="{uid}-remove">{m.agent_detail_remove()}</h2>
      <p class="muted">{m.agent_detail_remove_desc()}</p>
      <Button variant="danger" bind:element={removeButton} onclick={() => (removing = true)}>{m.remove_more()}</Button>
    </div>
    <RemoveDialog
      open={removing}
      title={m.remove_agent_title({ agent: MARK })}
      name={agent.display_name}
      body={[m.agent_detail_remove_desc(), m.remove_keeps()]}
      option={m.remove_agent_mandates()}
      confirm={m.remove_button()}
      {busy}
      {error}
      onclose={closeRemove}
      onconfirm={(mandates) => void remove(mandates)}
    />
  {/if}
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  h1:focus {
    outline: none;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 380px), 1fr));
    gap: var(--hm-space-5);
    align-items: start;
  }
  .column {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-5);
    min-inline-size: 0;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: 18px var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .end {
    align-items: flex-start;
    border-color: var(--hm-color-danger-border);
  }
  h2 {
    margin: 0;
    font-size: 17px;
    font-weight: var(--hm-font-weight-semibold);
  }
  dl {
    display: grid;
    grid-template-columns: minmax(120px, auto) minmax(0, 1fr);
    gap: 0 var(--hm-space-4);
    margin: 0;
  }
  dt,
  dd {
    padding-block: 10px;
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  dt:first-of-type,
  dt:first-of-type + dd {
    border-block-start: none;
  }
  dt {
    color: var(--hm-color-text-muted);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .mono {
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-sm);
  }
  .big {
    margin: 0;
    font-size: 22px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .muted {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  .log-head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--hm-space-1) var(--hm-space-3);
  }
  .log-head a {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    min-block-size: var(--hm-size-touch);
    color: var(--hm-color-accent-text);
    font-weight: var(--hm-font-weight-medium);
  }
</style>
