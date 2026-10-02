<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Audit log, events (design README 6.8): chain status with "verify now", filters in the
  URL, entries grouped by household day, "load more" with a stable cursor, details next to
  the list on desktop and on their own page on mobile. New entries never move the list;
  a pill offers them. A load error says that entries are still being written.
-->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { AuditEntry, DeviceCatalog } from '../api/types.ts';
  import { groupByDay } from '../audit/days.ts';
  import { parseFilters, toAuditQuery, toQuery, type AuditFilters } from '../audit/filters.ts';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import AuditDetail from '../components/audit/AuditDetail.svelte';
  import AuditFilterBar from '../components/audit/AuditFilters.svelte';
  import AuditRow from '../components/audit/AuditRow.svelte';
  import AuditTabs from '../components/audit/AuditTabs.svelte';
  import { formatRelative } from '../format.ts';
  import { m } from '../i18n.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { DESKTOP, Media } from '../ui/media.svelte.ts';
  import { toasts } from '../ui/toasts.ts';
  import { cleanUntrusted } from '../untrusted.ts';

  interface Props {
    app: AppState;
    /** Browser clock, ticking. */
    now: number;
    /** Query of the #/audit route: the filters. */
    query: Record<string, string[]>;
  }

  interface Page {
    entries: AuditEntry[];
    total: number;
    next: number | null;
  }

  interface Meta {
    agents: { value: string; label: string }[];
    catalog: DeviceCatalog | null;
    pending: number | null;
  }

  let { app, now, query }: Props = $props();

  const PAGE = 50;
  const NEW_CHECK_MS = 500;
  const SEPARATOR = ' · ';

  const id = $props.id();
  const desktop = new Media(DESKTOP);

  let filters: AuditFilters = $state(parseFilters(untrack(() => query)));
  let fresh = $state(0);
  let more = $state(false);
  let verifying = $state(false);
  let picked: AuditEntry | null = $state.raw(null);

  const serverNow = () => Date.now() - app.offsetMs;

  const list = new Loader<Page>(async () => {
    const page = await app.api.audit({ ...toAuditQuery(filters, serverNow()), limit: PAGE });
    fresh = 0;
    return { entries: page.entries, total: page.total, next: page.next_before };
  });

  const meta = new Loader<Meta>(async () => {
    const api = app.api;
    const [agents, catalog, approvals] = await Promise.all([
      api.agents().catch(() => []),
      api.devices().catch(() => null),
      api.approvals().catch(() => null),
    ]);
    return {
      agents: agents.map((a) => ({ value: a.client_id, label: cleanUntrusted(a.display_name) })),
      catalog,
      pending: approvals ? approvals.open.length : null,
    };
  });

  let timer: ReturnType<typeof setTimeout> | undefined;

  /** checkNew counts entries that arrived since the list was loaded, without moving it. */
  function checkNew() {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      const shown = list.data?.total;
      if (shown === undefined) return;
      try {
        const page = await app.api.audit({ ...toAuditQuery(filters, serverNow()), limit: 1 });
        fresh = Math.max(0, page.total - shown);
      } catch {
        // The next event tries again.
      }
    }, NEW_CHECK_MS);
  }

  onMount(() => {
    const stop = [
      app.on('audit.appended', checkNew),
      app.on('reconnected', checkNew),
      app.on('approval.opened', () => void meta.run()),
      app.on('approval.closed', () => void meta.run()),
      app.on('agents.changed', () => void meta.run()),
    ];
    void list.run();
    void meta.run();
    return () => {
      clearTimeout(timer);
      stop.forEach((off) => off());
    };
  });

  /** apply changes the filters, keeps them in the URL (without a navigation) and reloads. */
  function apply(next: AuditFilters) {
    filters = next;
    picked = null;
    window.history.replaceState(null, '', href({ name: 'audit', query: toQuery(next) }));
    list.data = null;
    void list.run();
  }

  function select(event: MouseEvent, entry: AuditEntry) {
    if (!desktop.matches) return; // mobile: the link opens the entry's own page
    event.preventDefault();
    filters = { ...filters, seq: entry.seq };
    picked = entry;
    window.history.replaceState(null, '', href({ name: 'audit', query: toQuery(filters) }));
  }

  async function loadMore() {
    const page = list.data;
    if (!page || page.next === null || more) return;
    more = true;
    try {
      const next = await app.api.audit({ ...toAuditQuery(filters, serverNow()), limit: PAGE, before: page.next });
      list.set({ entries: [...page.entries, ...next.entries], total: page.total, next: next.next_before });
    } catch {
      toasts.show({ kind: 'error', text: m.audit_error_title() });
    } finally {
      more = false;
    }
  }

  async function verify() {
    if (verifying) return;
    verifying = true;
    try {
      const result = await app.api.verifyAudit();
      if (app.system) {
        app.system = { ...app.system, chain: { valid: result.valid, broken_at_seq: result.broken_at_seq, checked_at: result.checked_at } };
      }
    } catch {
      toasts.show({ kind: 'error', text: m.audit_verify_failed() });
    } finally {
      verifying = false;
    }
  }

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const chain = $derived(app.system?.chain ?? null);
  const brokenAt = $derived(chain && !chain.valid ? chain.broken_at_seq : null);
  const days = $derived(list.data ? groupByDay(list.data.entries, ctx, now - app.offsetMs) : []);
  const checked = $derived(
    chain?.checked_at ? m.audit_chain_checked({ relative: formatRelative(new Date(chain.checked_at), new Date(now - app.offsetMs), ctx) }) : '',
  );
  const options = $derived.by(() => {
    const catalog = meta.data?.catalog;
    return {
      areas: (catalog?.areas ?? []).map((a) => ({ value: a.id, label: cleanUntrusted(a.name) })),
      devices: (catalog?.devices ?? []).map((d) => ({ value: d.entity_id, label: cleanUntrusted(d.name || d.entity_id) })),
    };
  });

  // The selected entry: from the list, or loaded alone (e.g. from a bookmark).
  $effect(() => {
    const seq = filters.seq;
    if (seq === null || picked?.seq === seq) return;
    const known = list.data?.entries.find((e) => e.seq === seq);
    if (known) {
      picked = known;
      return;
    }
    if (list.status !== 'ready') return;
    app.api
      .audit({ before: seq + 1, limit: 1 })
      .then((page) => {
        const entry = page.entries[0];
        if (entry?.seq === seq && filters.seq === seq) picked = entry;
      })
      .catch(() => {});
  });

  const isBroken = (entry: AuditEntry) => brokenAt !== null && entry.seq >= brokenAt;

  function rowHref(entry: AuditEntry): string {
    return desktop.matches ? href({ name: 'audit', query: toQuery({ ...filters, seq: entry.seq }) }) : href({ name: 'audit_entry', seq: entry.seq });
  }
</script>

<div class="head">
  <h1>{m.audit_title()}</h1>
  {#if chain}
    <div class="chain" class:bad={brokenAt !== null}>
      {#if brokenAt !== null}
        <span><Icon name="warning" size={16} />{m.audit_chain_broken({ number: brokenAt })}</span>
      {:else if chain.valid}
        <span class="ok"><Icon name="check" size={16} />{m.audit_chain_ok()}</span>
      {/if}
      {#if checked}<span class="muted">{checked}</span>{/if}
      <Button variant="text" icon="refresh" busy={verifying} onclick={verify}>{m.audit_verify_now()}</Button>
    </div>
  {/if}
</div>

<AuditTabs current="events" pending={meta.data?.pending ?? null} locale={ctx.locale} />

<AuditFilterBar
  {filters}
  agents={meta.data?.agents ?? []}
  areas={options.areas}
  devices={options.devices}
  total={list.data?.total ?? null}
  compact={!desktop.matches}
  onchange={apply}
/>

{#if list.status === 'error'}
  <ErrorState title={m.audit_error_title()} body={m.audit_error_body()} onretry={() => void list.run()} />
{:else}
  <div class="body" class:split={desktop.matches}>
    <section class="entries" aria-label={m.audit_tab_events()} aria-busy={list.status === 'loading'}>
      {#if fresh > 0}
        <button type="button" class="pill" onclick={() => void list.run()}>
          <Icon name="arrow" size={16} />{m.audit_new_entries({ count: fresh })}
        </button>
      {/if}
      {#if list.data && list.data.entries.length === 0}
        <EmptyState icon="search" title={m.audit_empty_title()} body={m.audit_empty_body()}>
          {#snippet action()}<Button onclick={() => apply(parseFilters({}))}>{m.filter_reset()}</Button>{/snippet}
        </EmptyState>
      {/if}
      {#each days as day (day.key)}
        <h2>{day.label}</h2>
        <ol>
          {#each day.entries as entry (entry.seq)}
            <li>
              <AuditRow
                {entry}
                catalog={meta.data?.catalog ?? null}
                {ctx}
                href={rowHref(entry)}
                current={filters.seq === entry.seq}
                broken={isBroken(entry)}
                onselect={(event) => select(event, entry)}
              />
            </li>
          {/each}
        </ol>
      {/each}
      {#if list.data?.next !== null && list.data}
        <Button busy={more} onclick={loadMore}>{m.load_more()}</Button>
      {/if}
      <p class="foot">{m.audit_retention()}{SEPARATOR}{m.common_timezone_note({ tz: ctx.timeZone })}</p>
    </section>

    {#if desktop.matches}
      {#if picked}
        <aside aria-labelledby="{id}-detail">
          <AuditDetail entry={picked} catalog={meta.data?.catalog ?? null} {ctx} broken={isBroken(picked)} headingId="{id}-detail" />
        </aside>
      {:else}
        <p class="hint">{m.audit_select_hint()}</p>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-3);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-3xl);
  }
  .chain {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
    font-size: var(--hm-font-size-sm);
  }
  .chain span {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
  }
  .ok {
    color: var(--hm-color-positive-fg);
  }
  .bad {
    color: var(--hm-color-danger-fg);
  }
  .muted,
  .foot,
  .hint {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .body {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--hm-space-5);
    align-items: start;
  }
  .body.split {
    grid-template-columns: minmax(0, 3fr) minmax(280px, 2fr);
  }
  .entries {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    min-inline-size: 0;
  }
  h2 {
    margin: var(--hm-space-3) 0 0;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text-muted);
  }
  ol {
    margin: 0;
    padding: 0;
    list-style: none;
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    overflow: hidden;
  }
  li + li {
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  aside {
    position: sticky;
    inset-block-start: var(--hm-space-4);
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-4);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
  }
  .pill {
    align-self: center;
    position: sticky;
    inset-block-start: var(--hm-space-2);
    z-index: 1;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-4);
    border: 0;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
  }
  .pill:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .foot {
    margin: var(--hm-space-2) 0 0;
  }
</style>
