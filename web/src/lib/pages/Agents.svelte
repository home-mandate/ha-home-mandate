<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Agents (design README 6.2): every admitted agent with its claimed name, client identity,
  mandate, last activity and status; a table on desktop and cards on mobile. Active agents
  come first. "Add agent" shows the two ways to sign in; without agents they show at once.
  A load error says that admitted agents keep working under their mandates.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { Agent } from '../api/types.ts';
  import AgentName from '../components/AgentName.svelte';
  import Button from '../components/Button.svelte';
  import ClientIdentity from '../components/ClientIdentity.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import AddWays from '../components/agents/AddWays.svelte';
  import AgentStatus from '../components/agents/AgentStatus.svelte';
  import MandateStatus from '../components/mandate/MandateStatus.svelte';
  import { formatDateTime, formatRelative } from '../format.ts';
  import { m } from '../i18n.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { DESKTOP, Media } from '../ui/media.svelte.ts';
  import { cleanUntrusted } from '../untrusted.ts';

  interface Props {
    app: AppState;
    /** Browser clock, ticking; relative times follow the server's time. */
    now: number;
  }

  let { app, now }: Props = $props();

  const SKELETON_ROWS = ['60%', '80%', '50%'];

  const id = $props.id();
  const desktop = new Media(DESKTOP);
  const list = new Loader<Agent[]>(() => app.api.agents());

  let adding = $state(false);
  let ways: HTMLElement | undefined = $state();

  const reload = () => void list.run();
  onMount(() => {
    const stop = [app.on('agents.changed', reload), app.on('mandates.changed', reload), app.on('reconnected', reload)];
    reload();
    return () => stop.forEach((off) => off());
  });

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const serverNow = $derived(new Date(now - app.offsetMs));
  // Active first; the server's order within each group.
  const agents = $derived(list.data ? [...list.data].sort((a, b) => Number(a.status === 'revoked') - Number(b.status === 'revoked')) : null);
  const showWays = $derived(adding || agents?.length === 0);

  async function add() {
    adding = !adding;
    if (!adding) return;
    await tick();
    ways?.querySelector('a')?.focus();
  }

  function last(agent: Agent): { text: string; title: string | undefined } {
    if (agent.last_active_at === null) return { text: m.agents_never_active(), title: undefined };
    const at = new Date(agent.last_active_at);
    return { text: formatRelative(at, serverNow, ctx), title: formatDateTime(at, ctx) };
  }
</script>

<div class="head">
  <h1>{m.agents_title()}</h1>
  {#if agents && agents.length > 0}
    <Button variant="primary" size="lg" icon="plus" aria-expanded={adding} aria-controls="{id}-ways" onclick={add}>{m.agents_add()}</Button>
  {/if}
</div>

{#if list.status === 'error'}
  <ErrorState title={m.agents_error_title()} body={m.agents_error_body()} onretry={reload} />
{:else if !agents}
  <div class="table skeleton" role="status" aria-busy="true" aria-label={m.common_loading()}>
    {#each SKELETON_ROWS as width (width)}<span class="bone" style:inline-size={width}></span>{/each}
  </div>
{:else}
  {#if agents.length === 0}
    <EmptyState icon="agent" title={m.agents_empty_title()} body={m.agents_empty_body()} />
  {/if}
  <div id="{id}-ways" bind:this={ways} hidden={!showWays}>
    {#if showWays}<AddWays headingId="{id}-ways-heading" />{/if}
  </div>

  {#if agents.length > 0 && desktop.matches}
    <div class="table">
      <table>
        <caption class="hm-visually-hidden">{m.agents_title()}</caption>
        <thead>
          <tr>
            <th scope="col">{m.agents_col_name()}</th>
            <th scope="col">{m.agents_col_client()}</th>
            <th scope="col">{m.agents_col_mandate()}</th>
            <th scope="col">{m.agents_col_last()}</th>
            <th scope="col">{m.agents_col_status()}</th>
          </tr>
        </thead>
        <tbody>
          {#each agents as agent (agent.client_id)}
            {@const seen = last(agent)}
            <tr class:revoked={agent.status === 'revoked'}>
              <th scope="row"><a href={href({ name: 'agent', id: agent.client_id })}><AgentName name={agent.display_name} /></a></th>
              <td><ClientIdentity client={agent.oauth_client} verified={agent.client_verified} /></td>
              <td>
                {#if agent.mandate}
                  <span class="mandate"
                    ><a href={href({ name: 'mandate', id: agent.mandate.id })}><bdi>{cleanUntrusted(agent.mandate.name)}</bdi></a
                    >{#if agent.status === 'active' && agent.mandate.status === 'revoked'}<MandateStatus status="revoked" compact />{/if}</span
                  >
                {:else}
                  <span class="muted">{m.agents_no_mandate()}</span>
                {/if}
              </td>
              <td class="muted" title={seen.title}>{seen.text}</td>
              <td><AgentStatus status={agent.status} /></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else if agents.length > 0}
    <ul class="mobile" role="list">
      {#each agents as agent (agent.client_id)}
        {@const seen = last(agent)}
        <li class:revoked={agent.status === 'revoked'}>
          <a href={href({ name: 'agent', id: agent.client_id })}>
            <span class="row"><span class="name"><AgentName name={agent.display_name} client={agent.oauth_client} verified={agent.client_verified} /></span><span class="chevron"><Icon name="chevron" /></span></span>
            <span class="hm-visually-hidden">, </span>
            <span class="facts">
              <AgentStatus status={agent.status} compact />
              <span><bdi>{agent.mandate ? cleanUntrusted(agent.mandate.name) : m.agents_no_mandate()}</bdi> · {seen.text}</span>
            </span>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-3) var(--hm-space-4);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
  }
  .table {
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    overflow: hidden;
  }
  table {
    inline-size: 100%;
    border-collapse: collapse;
    table-layout: fixed;
  }
  thead th {
    padding: var(--hm-space-3) var(--hm-space-4);
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
    text-align: start;
    color: var(--hm-color-text-muted);
    background: var(--hm-color-surface-sunken);
    border-block-end: var(--hm-border-width) solid var(--hm-color-border);
  }
  tbody th,
  td {
    padding: 14px var(--hm-space-4);
    font-size: 15px;
    font-weight: var(--hm-font-weight-regular);
    text-align: start;
    vertical-align: top;
    overflow-wrap: anywhere;
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  tbody tr:first-child th,
  tbody tr:first-child td {
    border-block-start: none;
  }
  tbody a {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
    text-decoration: none;
  }
  tbody a:hover {
    text-decoration: underline;
  }
  .mandate {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-1) var(--hm-space-2);
  }
  .muted {
    color: var(--hm-color-text-muted);
  }
  .revoked th,
  .revoked td,
  li.revoked a {
    color: var(--hm-color-text-muted);
  }
  .skeleton {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-5);
    padding: var(--hm-space-5) var(--hm-space-4);
  }
  .bone {
    display: block;
    block-size: 14px;
    border-radius: var(--hm-radius-sm);
    background: var(--hm-color-skeleton);
  }
  .mobile {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .mobile a {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 14px var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    color: var(--hm-color-text);
    text-decoration: none;
  }
  .row {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--hm-space-2);
  }
  .name {
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
    min-inline-size: 0;
  }
  .chevron {
    display: flex;
    padding-block-start: 2px;
    color: var(--hm-color-text-subtle);
  }
  .facts {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
</style>
