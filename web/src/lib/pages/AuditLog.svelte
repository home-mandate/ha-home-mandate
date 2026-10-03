<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Audit log, events (design README 6.8): chain status with "verify now", filters in the
  URL, entries grouped by household day, "load more" with a stable cursor, details next to
  the list on desktop and on their own page on mobile. New entries never move the list;
  a pill offers them. A load error says that entries are still being written.
-->
<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
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
  /** Most entries loaded again when coming back from an entry. */
  const MAX_RESTORE = 500;
  const NEW_CHECK_MS = 500;
  const SEPARATOR = ' · ';

  const id = $props.id();
  const desktop = new Media(DESKTOP);

  let filters: AuditFilters = $state(parseFilters(untrack(() => query)));
  let fresh = $state(0);
  let more = $state(false);
  let verifying = $state(false);
  let picked: AuditEntry | null = $state.raw(null);
  /** The selected entry is neither in the list nor on the server (any more). */
  let missing = $state(false);
  let entriesSection: HTMLElement | undefined = $state();
  /** Generation of the list: answers of an older one (filter changed, reloaded) are dropped. */
  let gen = 0;
  /** Entry that is being loaded alone, so the selection fetches it once. */
  let requested: number | null = null;

  const serverNow = () => Date.now() - app.offsetMs;

  /** The list without the desktop selection, as a hash: what an entry page leads back to. */
  const listHash = () => href({ name: 'audit', query: toQuery({ ...filters, seq: null }) });

  // Coming back from an entry's own page to the same list: load as far as before, then focus that entry.
  const restore = untrack(() => {
    const back = app.auditReturn;
    app.auditReturn = null;
    return back && back.list === listHash() ? back : null;
  });
  let want = restore ? Math.min(restore.count, MAX_RESTORE) : PAGE;

  const list = new Loader<Page>(async () => {
    const g = gen;
    const query = toAuditQuery(filters, serverNow());
    const first = await app.api.audit({ ...query, limit: PAGE });
    let entries = first.entries;
    let next = first.next_before;
    // Stops when the list was replaced meanwhile (filter change, refresh).
    while (entries.length < want && next !== null && g === gen) {
      const page = await app.api.audit({ ...query, limit: PAGE, before: next });
      entries = [...entries, ...page.entries];
      next = page.next_before;
    }
    want = PAGE;
    return { entries, total: first.total, next };
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
      const g = gen;
      try {
        const page = await app.api.audit({ ...toAuditQuery(filters, serverNow()), limit: 1 });
        if (g !== gen) return;
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
    void list.run().then(async () => {
      if (!restore) return;
      await tick();
      const row = entriesSection?.querySelector<HTMLElement>(`[data-seq="${restore.seq}"]`);
      row?.focus();
      row?.scrollIntoView({ block: 'center' });
    });
    void meta.run();
    return () => {
      clearTimeout(timer);
      stop.forEach((off) => off());
    };
  });

  /** apply changes the filters, keeps them in the URL (without a navigation) and reloads. */
  function apply(next: AuditFilters) {
    gen++;
    fresh = 0;
    filters = next;
    picked = null;
    missing = false;
    window.history.replaceState(null, '', href({ name: 'audit', query: toQuery(next) }));
    list.data = null;
    void list.run();
  }

  /** refresh loads the list from the top (the "new entries" pill) and moves the focus to it. */
  async function refresh() {
    gen++;
    fresh = 0;
    await list.run();
    await tick();
    entriesSection?.focus();
  }

  async function select(event: MouseEvent, entry: AuditEntry) {
    // Open in a new tab or window stays what the browser does, and this page keeps no way back.
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (!desktop.matches) {
      // Mobile: the link opens the entry's own page; remember the way back.
      app.auditReturn = { list: listHash(), seq: entry.seq, count: list.data?.entries.length ?? 0 };
      return;
    }
    event.preventDefault();
    filters = { ...filters, seq: entry.seq };
    picked = entry;
    missing = false;
    window.history.replaceState(null, '', href({ name: 'audit', query: toQuery(filters) }));
    await tick();
    document.getElementById(`${id}-detail`)?.focus();
  }

  async function loadMore() {
    const page = list.data;
    if (!page || page.next === null || more) return;
    more = true;
    const g = gen;
    try {
      const next = await app.api.audit({ ...toAuditQuery(filters, serverNow()), limit: PAGE, before: page.next });
      if (g !== gen || list.data !== page) return; // the list changed meanwhile
      list.set({ entries: [...page.entries, ...next.entries], total: page.total, next: next.next_before });
      const first = next.entries[0];
      await tick();
      if (first) entriesSection?.querySelector<HTMLElement>(`[data-seq="${first.seq}"]`)?.focus();
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
      app.setChain({ valid: result.valid, broken_at_seq: result.broken_at_seq, checked_at: result.checked_at });
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
  /** Name of the exact device or area filter in the catalog, if it is there. */
  const deviceName = $derived.by(() => {
    const device = filters.device;
    const catalog = meta.data?.catalog;
    if (device === null || !catalog) return null;
    const name = catalog.devices.find((d) => d.entity_id === device)?.name ?? catalog.areas.find((a) => a.id === device)?.name;
    return name ? cleanUntrusted(name) : null;
  });

  // The selected entry: from the list, or loaded alone once (e.g. from a bookmark).
  $effect(() => {
    const seq = filters.seq;
    const known = seq === null ? undefined : list.data?.entries.find((e) => e.seq === seq);
    const ready = list.status === 'ready';
    untrack(() => {
      if (seq === null) return;
      if (known) {
        picked = known;
        missing = false;
        return;
      }
      if (!ready || picked?.seq === seq || requested === seq) return;
      requested = seq;
      app.api
        .audit({ before: seq + 1, limit: 1 })
        .then((page) => {
          if (filters.seq !== seq) return;
          const entry = page.entries[0];
          if (entry?.seq === seq) picked = entry;
          missing = entry?.seq !== seq;
        })
        .catch(() => {
          if (filters.seq === seq) missing = true;
        })
        .finally(() => {
          if (requested === seq) requested = null;
        });
    });
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
      <span role="status">
        {#if brokenAt !== null}
          <span><Icon name="warning" size={16} />{m.audit_chain_broken({ number: brokenAt })}</span>
        {:else if chain.valid}
          <span class="ok"><Icon name="check" size={16} />{m.audit_chain_ok()}</span>
        {/if}
      </span>
      {#if checked}<span class="muted">{checked}</span>{/if}
      <Button variant="text" icon="refresh" busy={verifying} onclick={verify}>{m.audit_verify_now()}</Button>
    </div>
  {/if}
</div>

<AuditTabs current="events" pending={meta.data?.pending ?? null} locale={ctx.locale} />

<AuditFilterBar
  {filters}
  agents={meta.data?.agents ?? []}
  {deviceName}
  total={list.data?.total ?? null}
  compact={!desktop.matches}
  onchange={apply}
/>

{#if list.status === 'error'}
  <ErrorState title={m.audit_error_title()} body={m.audit_error_body()} onretry={() => void list.run()} />
{:else}
  <div class="body" class:split={desktop.matches}>
    <section class="entries" aria-label={m.audit_tab_events()} aria-busy={list.status === 'loading'} tabindex="-1" bind:this={entriesSection}>
      <div class="pill-slot" role="status">
        {#if fresh > 0}
          <button type="button" class="pill" onclick={() => void refresh()}>
            <Icon name="arrow" size={16} />{m.audit_new_entries({ count: fresh })}
          </button>
        {/if}
      </div>
      {#if list.data && list.data.entries.length === 0}
        <EmptyState icon="search" title={m.audit_empty_title()} body={m.audit_empty_body()}>
          {#snippet action()}<Button
              onclick={() => {
                apply(parseFilters({}));
                entriesSection?.focus();
              }}>{m.filter_reset()}</Button
            >{/snippet}
        </EmptyState>
      {/if}
      {#each days as day (day.key)}
        <h2>{day.label}</h2>
        <ol role="list">
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
      {#if list.data && list.data.next !== null}
        <Button busy={more} onclick={loadMore}>{m.load_more()}</Button>
      {/if}
      <p class="foot">{m.audit_retention()}{SEPARATOR}{m.common_timezone_note({ tz: ctx.timeZone })}</p>
    </section>

    {#if desktop.matches}
      {#if picked}
        <aside aria-labelledby="{id}-detail">
          <AuditDetail entry={picked} catalog={meta.data?.catalog ?? null} {ctx} broken={isBroken(picked)} headingId="{id}-detail" />
        </aside>
      {:else if missing}
        <p class="hint">{m.notfound_title()}</p>
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
  .entries:focus {
    outline: none;
  }
  .pill-slot {
    display: flex;
    justify-content: center;
    position: sticky;
    inset-block-start: var(--hm-space-2);
    z-index: 1;
  }
  .pill {
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
