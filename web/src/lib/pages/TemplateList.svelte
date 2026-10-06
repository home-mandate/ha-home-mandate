<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Templates (Mandates → Templates; docs/ARCHITECTURE.md section 6): the base templates that
  ship with Home-Mandate, hidden ones included and marked, and the household's own. Each
  card says in plain words what the template allows and leads to the editor; a new template
  starts empty there. A template only shapes mandates made from it later.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { DeviceCatalog, Template } from '../api/types.ts';
  import BackLink from '../components/BackLink.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import PlainWords from '../components/mandate/PlainWords.svelte';
  import TemplateBadges from '../components/mandate/TemplateBadges.svelte';
  import { formatDate } from '../format.ts';
  import { m } from '../i18n.ts';
  import { templateDescription, templateTitle } from '../mandate/template.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { cleanUntrusted, isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
  }

  interface Data {
    templates: Template[];
    catalog: DeviceCatalog;
  }

  let { app }: Props = $props();

  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const uid = $props.id();
  const list = new Loader<Data>(async () => {
    const api = app.api;
    const [summaries, catalog] = await Promise.all([api.templates(), api.devices().catch(() => NO_CATALOG)]);
    // One removed between the list and its own request is left out.
    const loaded = await Promise.all(summaries.map((t) => api.template(t.name).catch(() => null)));
    return { templates: loaded.filter((t): t is Template => t !== null), catalog };
  });

  const reload = () => void list.run();
  onMount(() => {
    const stop = [app.on('templates.changed', reload), app.on('devices.changed', reload), app.on('reconnected', reload)];
    reload();
    return () => stop.forEach((off) => off());
  });

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const builtins = $derived(list.data?.templates.filter((t) => t.builtin) ?? []);
  const own = $derived(list.data?.templates.filter((t) => !t.builtin) ?? []);

  const SEPARATOR = ' · ';

  /** How many rules, and for the household's own templates when and by whom. */
  function meta(t: Template): string {
    const rules = m.mandates_rules_count({ count: t.draft.rules.length });
    return t.builtin ? rules : `${rules}${SEPARATOR}${created(t)}`;
  }

  function created(t: Template): string {
    const at = t.created_at ? new Date(t.created_at) : null;
    const date = at && !Number.isNaN(at.getTime()) ? formatDate(at, ctx) : '';
    const by = cleanUntrusted(t.created_by_name);
    return by ? m.templates_created_by({ date, name: isolate(by) }) : m.templates_created({ date });
  }
</script>

<BackLink href={href({ name: 'mandates' })} label={m.editor_back()} />
<div class="head">
  <h1>{m.templates_title()}</h1>
  <a class="new" href={href({ name: 'template', template: null })}><Icon name="plus" />{m.templates_new()}</a>
</div>
<p class="intro">{m.templates_intro()}</p>

{#snippet card(t: Template, data: Data)}
  <li>
    <article aria-labelledby="{uid}-{t.name}">
      <div class="title">
        <h3 id="{uid}-{t.name}"><a href={href({ name: 'template', template: t.name })}><bdi>{templateTitle(t)}</bdi></a></h3>
        <TemplateBadges template={t} />
      </div>
      {#if templateDescription(t)}<p class="description">{templateDescription(t)}</p>{/if}
      <p class="meta">{meta(t)}</p>
      <PlainWords draft={t.draft} catalog={data.catalog} locale={ctx.locale} />
    </article>
  </li>
{/snippet}

{#if list.status === 'error'}
  <ErrorState title={m.templates_error_title()} body={m.templates_error_body()} onretry={reload} />
{:else if !list.data}
  <div role="status" aria-busy="true" aria-label={m.common_loading()}><Skeleton lines={['40%', '70%', '55%']} /></div>
{:else}
  {@const data = list.data}
  <section aria-labelledby="{uid}-builtin">
    <div class="section-head">
      <h2 id="{uid}-builtin">{m.templates_builtin_heading()}</h2>
      <p>{m.templates_builtin_note()}</p>
    </div>
    <ul class="cards" role="list">
      {#each builtins as t (t.name)}{@render card(t, data)}{/each}
    </ul>
  </section>

  <section aria-labelledby="{uid}-own">
    <div class="section-head">
      <h2 id="{uid}-own">{m.templates_own_heading()}</h2>
      {#if own.length === 0}<p>{m.templates_own_none()}</p>{/if}
    </div>
    {#if own.length > 0}
      <ul class="cards" role="list">
        {#each own as t (t.name)}{@render card(t, data)}{/each}
      </ul>
    {/if}
  </section>
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
  .new {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    text-decoration: none;
    color: var(--hm-color-text-on-accent);
    background: var(--hm-color-accent);
    border: var(--hm-border-width) solid var(--hm-color-accent);
  }
  .intro {
    margin: 0;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  section {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .section-head {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
  }
  .section-head p {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .cards {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
    gap: var(--hm-space-3);
  }
  article {
    display: flex;
    flex-direction: column;
    gap: 10px;
    block-size: 100%;
    box-sizing: border-box;
    padding: 18px var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    box-shadow: var(--hm-shadow-sm);
  }
  .title {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-1) var(--hm-space-2);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  h3 a {
    color: var(--hm-color-accent-text);
    text-decoration: none;
  }
  h3 a:hover {
    text-decoration: underline;
  }
  .description,
  .meta {
    margin: 0;
    font-size: 15px;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .meta {
    font-size: var(--hm-font-size-sm);
  }
</style>
