<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Mandates (design README 6.4): templates to start from and the list of mandates, a table
  on desktop and cards on mobile. A load error says that existing mandates keep applying;
  it must never look as if protection were off.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { Agent, DeviceCatalog, MandateSummary, Rename, Template } from '../api/types.ts';
  import AgentName from '../components/AgentName.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import MandateStatus from '../components/mandate/MandateStatus.svelte';
  import NewMandateDialog from '../components/mandate/NewMandateDialog.svelte';
  import RenamesNotice from '../components/mandate/RenamesNotice.svelte';
  import TemplateCard from '../components/mandate/TemplateCard.svelte';
  import { formatDate } from '../format.ts';
  import { m } from '../i18n.ts';
  import { effectiveStatus } from '../mandate/dates.ts';
  import { parseDateTime } from '../engine/check.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { DESKTOP, Media } from '../ui/media.svelte.ts';
  import { toasts } from '../ui/toasts.ts';
  import { cleanUntrusted } from '../untrusted.ts';

  interface Props {
    app: AppState;
    /** Browser clock, ticking; the status follows the server's time. */
    now: number;
  }

  interface Data {
    mandates: MandateSummary[];
    templates: Template[];
    agents: Agent[];
    catalog: DeviceCatalog;
    renames: Rename[];
  }

  let { app, now }: Props = $props();

  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const SKELETON_ROWS = ['60%', '70%', '45%'];

  const id = $props.id();
  const desktop = new Media(DESKTOP);
  const list = new Loader<Data>(async () => {
    const api = app.api;
    // Only the mandates are essential. Without templates or agents the list still shows
    // (a new mandate cannot be created then); without Home Assistant the template cards
    // show ids instead of names.
    const [mandates, templates, agents, catalog, renames] = await Promise.all([
      api.mandates(),
      api
        .templates()
        .then((list) => Promise.all(list.map((t) => api.template(t.name).catch(() => null))))
        .then((list) => list.filter((t) => t !== null))
        .catch((): Template[] => []),
      api.agents().catch((): Agent[] => []),
      api.devices().catch(() => NO_CATALOG),
      api.renames().catch((): Rename[] => []),
    ]);
    return { mandates, templates, agents, catalog, renames };
  });

  let dialog = $state(false);
  let template = $state<string | null>(null);

  const reload = () => void list.run();
  onMount(() => {
    const stop = [
      app.on('mandates.changed', reload),
      app.on('templates.changed', reload),
      app.on('agents.changed', reload),
      app.on('devices.changed', reload),
      app.on('reconnected', reload),
    ];
    reload();
    return () => stop.forEach((off) => off());
  });

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const serverNow = $derived(now - app.offsetMs);
  const data = $derived(list.data);
  const eligible = $derived(data?.agents.filter((a) => a.status === 'active' && a.mandate?.status !== 'active') ?? []);

  /** The last moment the mandate applies, as a date in the household time zone. */
  function until(mandate: MandateSummary): string {
    const expires = parseDateTime(mandate.expires ?? undefined);
    return Number.isNaN(expires) ? m.mandates_valid_open() : formatDate(new Date(expires - 1), ctx);
  }

  let heading: HTMLElement | undefined = $state();

  /** A rename was resolved (text) or the attempt failed (null): reload, say so and keep the focus on the page. */
  async function resolved(text: string | null) {
    await list.run();
    if (text === null) return;
    toasts.show({ kind: 'success', text });
    await tick();
    heading?.focus();
  }

  function start(name: string | null) {
    template = name;
    dialog = true;
  }

  function created(id: string) {
    dialog = false;
    window.location.hash = href({ name: 'mandate', id });
  }
</script>

<div class="head">
  <h1 bind:this={heading} tabindex="-1">{m.mandates_title()}</h1>
  {#if data}<Button variant="primary" size="lg" icon="plus" onclick={() => start(null)}>{m.mandates_new()}</Button>{/if}
</div>

{#if list.status === 'error'}
  <ErrorState title={m.mandates_error_title()} body={m.mandates_error_body()} onretry={reload} />
{:else if !data}
  <div class="table skeleton" role="status" aria-busy="true" aria-label={m.common_loading()}>
    {#each SKELETON_ROWS as width (width)}<span class="bone" style:inline-size={width}></span>{/each}
  </div>
{:else}
  {#if data.renames.length > 0}
    <RenamesNotice renames={data.renames} api={app.api} locale={ctx.locale} onresolved={resolved} />
  {/if}

  {#if data.mandates.length === 0}
    <EmptyState icon="logo" title={m.mandates_empty_title()} body={m.mandates_empty_body()} />
  {/if}

  {#if data.templates.length > 0}
    <section class="templates" aria-labelledby="{id}-templates">
      <div class="templates-head">
        <h2 id="{id}-templates">{m.mandates_templates_heading()}</h2>
        <span>{m.mandates_templates_note()}</span>
      </div>
      <div class="cards">
        {#each data.templates as t (t.name)}
          <TemplateCard template={t} catalog={data.catalog} locale={ctx.locale} onuse={start} />
        {/each}
      </div>
    </section>
  {/if}

  {#if data.mandates.length > 0 && desktop.matches}
    <div class="table">
      <table>
        <caption class="hm-visually-hidden">{m.mandates_title()}</caption>
        <thead>
          <tr>
            <th scope="col">{m.mandates_col_name()}</th>
            <th scope="col">{m.mandates_col_agent()}</th>
            <th scope="col">{m.mandates_col_rules()}</th>
            <th scope="col">{m.mandates_col_valid_until()}</th>
            <th scope="col">{m.mandates_col_status()}</th>
          </tr>
        </thead>
        <tbody>
          {#each data.mandates as mandate (mandate.id)}
            <tr>
              <th scope="row"><a href={href({ name: 'mandate', id: mandate.id })}><bdi>{cleanUntrusted(mandate.name)}</bdi></a></th>
              <td><AgentName name={mandate.agent_display_name} /></td>
              <td class="muted">{m.mandates_rules_count({ count: mandate.rule_count })}</td>
              <td class="muted">{until(mandate)}</td>
              <td>
                <MandateStatus status={effectiveStatus(mandate, serverNow)} />
                {#if mandate.stale_references.length > 0}
                  <span class="stale"><Icon name="warning" /><span>{m.mandates_stale({ count: mandate.stale_references.length })}</span></span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else if data.mandates.length > 0}
    <ul class="mobile" role="list">
      {#each data.mandates as mandate (mandate.id)}
        <li>
          <a href={href({ name: 'mandate', id: mandate.id })}>
            <span class="row"><bdi class="name">{cleanUntrusted(mandate.name)}</bdi><span class="chevron"><Icon name="chevron" /></span></span>
            <span class="agent"><AgentName name={mandate.agent_display_name} /></span>
            <span class="facts">
              <MandateStatus status={effectiveStatus(mandate, serverNow)} compact />
              <span>{m.mandates_rules_count({ count: mandate.rule_count })} · {m.mandates_valid_until_date({ date: until(mandate) })}</span>
            </span>
            {#if mandate.stale_references.length > 0}
              <span class="stale"><Icon name="warning" /><span>{m.mandates_stale({ count: mandate.stale_references.length })}</span></span>
            {/if}
          </a>
        </li>
      {/each}
    </ul>
  {/if}

  <NewMandateDialog open={dialog} api={app.api} agents={eligible} templates={data.templates} {template} onclose={() => (dialog = false)} oncreated={created} />
{/if}

<style>
  .stale {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-1);
    margin-block-start: var(--hm-space-1);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-warning-fg);
  }
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
  .templates {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .templates-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hm-space-1) var(--hm-space-3);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-subtle);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr));
    gap: var(--hm-space-3);
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
  tbody th a {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
    text-decoration: none;
  }
  tbody th a:hover {
    text-decoration: underline;
  }
  .muted {
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
  }
  .chevron {
    display: flex;
    padding-block-start: 2px;
    color: var(--hm-color-text-subtle);
  }
  .agent {
    font-size: var(--hm-font-size-sm);
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
